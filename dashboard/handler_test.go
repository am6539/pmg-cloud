package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHandlerMirror builds a Handler with a mirror pointing at the given test server URL.
func newHandlerMirror(t *testing.T, srvURL string) http.Handler {
	t.Helper()
	m := newTestMirror(t, srvURL, srvURL)
	return Handler(t.TempDir(), HandlerDeps{Mirror: m})
}

func TestHandler_HealthzHandler_ReturnsOK(t *testing.T) {
	h := HealthzHandler(t.TempDir(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		OK bool `json:"ok"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.OK)
}

func TestHandler_MalwareRefresh_Post_ReturnsStatus(t *testing.T) {
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validFeedJSON))
	}))
	defer srv.Close()

	h := newHandlerMirror(t, srv.URL)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/malware/refresh", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var status MalwareMirrorStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	assert.True(t, status.NPM.OK)
	assert.True(t, status.PyPI.OK)
}

func TestHandler_MalwareRefresh_GetMethodNotAllowed(t *testing.T) {
	h := newHandlerMirror(t, "http://127.0.0.1:0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/malware/refresh", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandler_MalwareRefresh_UpstreamError_ReturnsBadGateway(t *testing.T) {
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newHandlerMirror(t, srv.URL)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/malware/refresh", nil))
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func newEnrollHandler(t *testing.T) (http.Handler, *GroupStore, *EnrollmentStore) {
	t.Helper()
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	h := Handler(dataDir, HandlerDeps{
		Groups:     groups,
		Enrollment: enrollment,
		Audit:      NewAuditLog(dataDir),
	})
	return h, groups, enrollment
}

func doEnroll(t *testing.T, h http.Handler, token, hostname, localIP string) map[string]any {
	t.Helper()
	body := `{"token":"` + token + `","hostname":"` + hostname + `","os":"windows","arch":"amd64","pmg_version":"0.18.10","local_ip":"` + localIP + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/enroll", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestHandler_Enroll_ReenrollSameHostnameAndIP_UpdatesExistingAgent(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	second := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")

	assert.Equal(t, first["agent_id"], second["agent_id"], "re-enroll must reuse the same agent ID")
	assert.NotEqual(t, first["api_key"], second["api_key"], "re-enroll must issue a fresh API key")

	_, _, oldKeyOK := groups.ResolveKeyWithID(first["api_key"].(string))
	assert.False(t, oldKeyOK, "old API key must be revoked after re-enroll")
	_, _, newKeyOK := groups.ResolveKeyWithID(second["api_key"].(string))
	assert.True(t, newKeyOK, "new API key must resolve")

	active := enrollment.ListAgents()
	count := 0
	for _, a := range active {
		if a.Hostname == "HT-PC" {
			count++
		}
	}
	assert.Equal(t, 1, count, "re-enroll must not create a duplicate agent row")
}

func TestHandler_Enroll_DifferentLocalIP_CreatesSeparateAgents(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	second := doEnroll(t, h, plaintextToken, "HT-PC", "192.168.99.99")

	assert.NotEqual(t, first["agent_id"], second["agent_id"], "different local IP must not be treated as the same machine")

	active := enrollment.ListAgents()
	count := 0
	for _, a := range active {
		if a.Hostname == "HT-PC" {
			count++
		}
	}
	assert.Equal(t, 2, count)
}

func TestHandler_Enroll_ReenrollPreservesAdminAssignedLabel(t *testing.T) {
	h, groups, enrollment := newEnrollHandler(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plaintextToken, _, err := enrollment.CreateToken("agent", group.ID, "test", 0, 0)
	require.NoError(t, err)

	first := doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")
	require.NoError(t, enrollment.SetAgentLabel(first["agent_id"].(string), "HC-Hieu"))

	doEnroll(t, h, plaintextToken, "HT-PC", "169.254.27.36")

	updated, ok := enrollment.GetAgentByID(first["agent_id"].(string))
	require.True(t, ok)
	assert.Equal(t, "HC-Hieu", updated.Label)
}

func newTestSessionsWithAdmin(t *testing.T) (*SessionStore, string) {
	t.Helper()
	sessions := NewSessionStore()
	sid, err := sessions.Create(DashUser{ID: "u1", Username: "admin", Role: RoleAdmin})
	require.NoError(t, err)
	return sessions, sid
}

func doWithSession(t *testing.T, h http.Handler, method, path, sid, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sid})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newEnrollHandlerWithAdmin(t *testing.T) (http.Handler, *GroupStore, *EnrollmentStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	users, err := NewUserStore(dataDir, "seed-admin", "seed-password-123")
	require.NoError(t, err)
	sessions, sid := newTestSessionsWithAdmin(t)
	h := Handler(dataDir, HandlerDeps{
		Groups:     groups,
		Enrollment: enrollment,
		Users:      users,
		Sessions:   sessions,
		Audit:      NewAuditLog(dataDir),
	})
	return h, groups, enrollment, sid
}

func newEndpointsHandlerWithRoles(t *testing.T) (http.Handler, *EnrollmentStore, map[string]string, string) {
	t.Helper()
	dataDir := t.TempDir()
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	users, err := NewUserStore(dataDir, "seed-admin", "seed-password-123")
	require.NoError(t, err)
	sessions := NewSessionStore()
	sids := make(map[string]string)
	for _, role := range []string{RoleAdmin, RoleEditor, RoleViewer} {
		sid, createErr := sessions.Create(DashUser{ID: "user-" + role, Username: role, Role: role})
		require.NoError(t, createErr)
		sids[role] = sid
	}
	h := Handler(dataDir, HandlerDeps{Enrollment: enrollment, Users: users, Sessions: sessions, Audit: NewAuditLog(dataDir)})
	return h, enrollment, sids, dataDir
}

func newEndpointDeleteHandler(t *testing.T) (http.Handler, *EnrollmentStore, map[string]string, string) {
	t.Helper()
	dataDir := t.TempDir()
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	users, err := NewUserStore(dataDir, "seed-admin", "seed-password-123")
	require.NoError(t, err)
	sessions := NewSessionStore()
	sids := make(map[string]string)
	for _, role := range []string{RoleAdmin, RoleEditor} {
		sid, createErr := sessions.Create(DashUser{ID: "user-" + role, Username: role, Role: role})
		require.NoError(t, createErr)
		sids[role] = sid
	}
	h := Handler(dataDir, HandlerDeps{
		Enrollment: enrollment,
		Users:      users,
		Sessions:   sessions,
		Audit:      NewAuditLog(dataDir),
	})
	return h, enrollment, sids, dataDir
}

func readEndpointEvents(t *testing.T, h http.Handler, sid, endpointID string) []Event {
	t.Helper()
	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints/"+endpointID+"/events", sid, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var events []Event
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	return events
}

func TestHandler_DeleteObservedEndpointTelemetry_AdminSucceeds(t *testing.T) {
	h, _, sids, dataDir := newEndpointDeleteHandler(t)
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "observed", EndpointID: "ci-runner", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/endpoints/ci-runner/events", sids[RoleAdmin], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, readEndpointEvents(t, h, sids[RoleAdmin], "ci-runner"))
}

func TestHandler_DeleteEndpointTelemetry_ActiveManagedRejected(t *testing.T) {
	h, enrollment, sids, dataDir := newEndpointDeleteHandler(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "managed-host"}))
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "managed", EndpointID: "agent-1", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/endpoints/agent-1/events", sids[RoleAdmin], "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Len(t, readEndpointEvents(t, h, sids[RoleAdmin], "agent-1"), 1)
}

func TestHandler_DeleteEndpointTelemetry_HostnameMatchedManagedRejectedCaseInsensitive(t *testing.T) {
	h, enrollment, sids, dataDir := newEndpointDeleteHandler(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "Managed-Host"}))
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "managed", EndpointID: "managed-host", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/endpoints/managed-host/events", sids[RoleAdmin], "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Len(t, readEndpointEvents(t, h, sids[RoleAdmin], "managed-host"), 1)
}

func TestHandler_DeleteEndpointTelemetry_UnknownObservedReturns404(t *testing.T) {
	h, _, sids, _ := newEndpointDeleteHandler(t)
	rec := doWithSession(t, h, http.MethodDelete, "/api/endpoints/missing/events", sids[RoleAdmin], "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_DeleteObservedEndpointTelemetry_EditorForbidden(t *testing.T) {
	h, _, sids, dataDir := newEndpointDeleteHandler(t)
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "observed", EndpointID: "ci-runner", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/endpoints/ci-runner/events", sids[RoleEditor], "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Len(t, readEndpointEvents(t, h, sids[RoleAdmin], "ci-runner"), 1)
}

func TestHandler_DeleteUnknownAgentDoesNotDeleteObservedTelemetry(t *testing.T) {
	h, _, sids, dataDir := newEndpointDeleteHandler(t)
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "observed", EndpointID: "ci-runner", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/agents/ci-runner", sids[RoleAdmin], "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Len(t, readEndpointEvents(t, h, sids[RoleAdmin], "ci-runner"), 1)
}

func TestHandler_DeleteManagedAgentRemovesEnrollmentAndTelemetry(t *testing.T) {
	h, enrollment, sids, dataDir := newEndpointDeleteHandler(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "managed-host"}))
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "managed", EndpointID: "agent-1", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodDelete, "/api/agents/agent-1", sids[RoleAdmin], "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	agent, ok := enrollment.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.True(t, agent.Removed)
	assert.Empty(t, readEndpointEvents(t, h, sids[RoleAdmin], "agent-1"))
}

func TestHandler_Endpoints_AllDashboardRolesCanRead(t *testing.T) {
	h, enrollment, sids, _ := newEndpointsHandlerWithRoles(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "build-host", EnrolledAt: time.Now().UTC()}))

	for _, role := range []string{RoleAdmin, RoleEditor, RoleViewer} {
		t.Run(role, func(t *testing.T) {
			rec := doWithSession(t, h, http.MethodGet, "/api/endpoints", sids[role], "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var endpoints []EndpointInfo
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &endpoints))
			require.Len(t, endpoints, 1)
			assert.Equal(t, "agent-1", endpoints[0].AgentID)
			assert.Equal(t, "agent-1", endpoints[0].EndpointID)
		})
	}
}

func TestHandler_Endpoints_UnknownDashboardRoleIsForbidden(t *testing.T) {
	h, _, _, dataDir := newEndpointsHandlerWithRoles(t)
	users, err := NewUserStore(dataDir, "another-admin", "seed-password-123")
	require.NoError(t, err)
	sessions := NewSessionStore()
	sid, err := sessions.Create(DashUser{ID: "user-unknown", Username: "unknown", Role: "unknown"})
	require.NoError(t, err)
	h = Handler(dataDir, HandlerDeps{Users: users, Sessions: sessions})

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints", sid, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandler_Endpoints_RequiresGET(t *testing.T) {
	h, _, sids, _ := newEndpointsHandlerWithRoles(t)
	rec := doWithSession(t, h, http.MethodPost, "/api/endpoints", sids[RoleAdmin], "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandler_Endpoints_GroupFilterExcludesOtherZeroEventAgents(t *testing.T) {
	h, enrollment, sids, _ := newEndpointsHandlerWithRoles(t)
	now := time.Now().UTC()
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-g1", Hostname: "host-g1", GroupID: "g1", EnrolledAt: now}))
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-g2", Hostname: "host-g2", GroupID: "g2", EnrolledAt: now}))

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints?group_id=g1", sids[RoleAdmin], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var endpoints []EndpointInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &endpoints))
	require.Len(t, endpoints, 1)
	assert.Equal(t, "agent-g1", endpoints[0].AgentID)
}

func TestHandler_Endpoints_PeriodLimitsActivityButKeepsInventoryAndLifecycle(t *testing.T) {
	h, enrollment, sids, dataDir := newEndpointsHandlerWithRoles(t)
	now := time.Now().UTC()
	heartbeat := now.Add(-time.Hour)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "managed-host", GroupID: "g1", EnrolledAt: now.AddDate(0, 0, -30), LastSeen: &heartbeat}))
	old := now.AddDate(0, 0, -10)
	writeEventsFile(t, dataDir, old.Format("20060102"), []Event{
		{EventID: "managed-old", EndpointID: "agent-1", GroupID: "g1", InvocationID: "run-managed", EventType: "PACKAGE_DECISION", PackageName: "old-package", Action: "BLOCKED", ReceivedAt: old},
		{EventID: "observed-old", EndpointID: "ci-runner", GroupID: "g1", InvocationID: "run-observed", EventType: "PACKAGE_DECISION", PackageName: "old-package", Action: "BLOCKED", ReceivedAt: old.Add(time.Minute)},
	})

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints?days=7&group_id=g1", sids[RoleAdmin], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var endpoints []EndpointInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &endpoints))
	require.Len(t, endpoints, 2)
	byID := make(map[string]EndpointInfo)
	for _, endpoint := range endpoints {
		byID[endpoint.EndpointID] = endpoint
	}
	managed := byID["agent-1"]
	assert.Equal(t, heartbeat, managed.LastSeen)
	assert.Zero(t, managed.Sessions)
	assert.Zero(t, managed.TotalPackages)
	assert.Zero(t, managed.BlockedPackages)
	observed, ok := byID["ci-runner"]
	require.True(t, ok, "observed endpoint outside range must remain in inventory")
	assert.Equal(t, old.Add(time.Minute), observed.LastSeen)
	assert.Zero(t, observed.Sessions)
	assert.Zero(t, observed.TotalPackages)
	assert.Zero(t, observed.BlockedPackages)
}

// TestHandler_Endpoints_PeriodActivityMatchesDirectMergeCounters guards the
// /api/endpoints handler's single-tally optimization (tallyEndpointActivity)
// against the previous two-full-MergeAgentEndpoints-calls behavior: the
// sessions/total_packages/blocked_packages the handler overlays onto the
// all-time inventory must be byte-for-byte identical to what a second
// MergeAgentEndpoints call over the period-scoped events would have produced,
// for a representative case with multiple endpoints, multiple invocations
// per endpoint, and a mix of in-range/out-of-range events.
func TestHandler_Endpoints_PeriodActivityMatchesDirectMergeCounters(t *testing.T) {
	h, enrollment, sids, dataDir := newEndpointsHandlerWithRoles(t)
	now := time.Now().UTC()
	heartbeat := now.Add(-time.Hour)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "managed-host", GroupID: "g1", EnrolledAt: now.AddDate(0, 0, -30), LastSeen: &heartbeat}))

	old := now.AddDate(0, 0, -10)
	inRange1 := now.Add(-2 * time.Hour)
	inRange2 := now.Add(-time.Hour)
	periodEvents := []Event{
		// agent-1: two sessions in range, one blocked + one allowed package decision
		{EventID: "e1", EndpointID: "agent-1", GroupID: "g1", InvocationID: "run-1", EventType: "PACKAGE_DECISION", Action: "BLOCKED", ReceivedAt: inRange1},
		{EventID: "e2", EndpointID: "agent-1", GroupID: "g1", InvocationID: "run-2", EventType: "PACKAGE_DECISION", Action: "ALLOWED", ReceivedAt: inRange2},
		// ci-runner (observed): three package decisions across two invocations
		{EventID: "e3", EndpointID: "ci-runner", GroupID: "g1", InvocationID: "run-a", EventType: "PACKAGE_DECISION", Action: "BLOCKED", ReceivedAt: inRange1},
		{EventID: "e4", EndpointID: "ci-runner", GroupID: "g1", InvocationID: "run-a", EventType: "PACKAGE_DECISION", Action: "BLOCKED", ReceivedAt: inRange1.Add(time.Minute)},
		{EventID: "e5", EndpointID: "ci-runner", GroupID: "g1", InvocationID: "run-b", EventType: "PACKAGE_DECISION", Action: "ALLOWED", ReceivedAt: inRange2},
	}
	writeEventsFile(t, dataDir, now.Format("20060102"), periodEvents)
	outOfRange := []Event{
		{EventID: "old-1", EndpointID: "agent-1", GroupID: "g1", InvocationID: "run-old", EventType: "PACKAGE_DECISION", Action: "BLOCKED", ReceivedAt: old},
		{EventID: "old-2", EndpointID: "ci-runner", GroupID: "g1", InvocationID: "run-old-2", EventType: "PACKAGE_DECISION", Action: "BLOCKED", ReceivedAt: old},
	}
	writeEventsFile(t, dataDir, old.Format("20060102"), outOfRange)

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints?days=7&group_id=g1", sids[RoleAdmin], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var endpoints []EndpointInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &endpoints))
	byID := make(map[string]EndpointInfo)
	for _, endpoint := range endpoints {
		byID[endpoint.EndpointID] = endpoint
	}

	// Compute expected counters the old way: a second full MergeAgentEndpoints
	// call over just the period-scoped (in-range) events.
	var agents []Agent
	for _, a := range enrollment.ListAllAgents() {
		if a.GroupID == "g1" {
			agents = append(agents, a)
		}
	}
	expected := MergeAgentEndpoints(agents, periodEvents)
	expectedByID := make(map[string]EndpointInfo)
	for _, e := range expected {
		expectedByID[e.EndpointID] = e
	}

	require.Contains(t, byID, "agent-1")
	require.Contains(t, byID, "ci-runner")
	require.Contains(t, expectedByID, "agent-1")
	require.Contains(t, expectedByID, "ci-runner")

	for _, epID := range []string{"agent-1", "ci-runner"} {
		got := byID[epID]
		want := expectedByID[epID]
		assert.Equal(t, want.Sessions, got.Sessions, "endpoint %s sessions", epID)
		assert.Equal(t, want.TotalPackages, got.TotalPackages, "endpoint %s total_packages", epID)
		assert.Equal(t, want.BlockedPackages, got.BlockedPackages, "endpoint %s blocked_packages", epID)
	}
	// Sanity: the counts are non-trivial (not just both-zero, which would make
	// the comparison above vacuous).
	assert.Equal(t, 2, byID["agent-1"].Sessions)
	assert.Equal(t, 2, byID["agent-1"].TotalPackages)
	assert.Equal(t, 1, byID["agent-1"].BlockedPackages)
	assert.Equal(t, 2, byID["ci-runner"].Sessions)
	assert.Equal(t, 3, byID["ci-runner"].TotalPackages)
	assert.Equal(t, 2, byID["ci-runner"].BlockedPackages)
}

func TestHandler_EndpointEvents_UsesRequestedRange(t *testing.T) {
	h, _, sids, dataDir := newEndpointsHandlerWithRoles(t)
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -10)
	writeEventsFile(t, dataDir, old.Format("20060102"), []Event{{EventID: "old", EndpointID: "ep-1", ReceivedAt: old}})
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{{EventID: "recent", EndpointID: "ep-1", ReceivedAt: now}})

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints/ep-1/events?days=7", sids[RoleViewer], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var events []Event
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	require.Len(t, events, 1)
	assert.Equal(t, "recent", events[0].EventID)
}

func TestHandler_EndpointEvents_GroupFilterPreventsCrossGroupLeak(t *testing.T) {
	h, _, sids, dataDir := newEndpointsHandlerWithRoles(t)
	now := time.Now().UTC()
	writeEventsFile(t, dataDir, now.Format("20060102"), []Event{
		{EventID: "g1-event", EndpointID: "ep-1", GroupID: "g1", ReceivedAt: now},
		{EventID: "g2-event", EndpointID: "ep-1", GroupID: "g2", ReceivedAt: now.Add(time.Minute)},
	})

	rec := doWithSession(t, h, http.MethodGet, "/api/endpoints/ep-1/events?group_id=g1", sids[RoleViewer], "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var events []Event
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	require.Len(t, events, 1)
	assert.Equal(t, "g1-event", events[0].EventID)
	assert.Equal(t, "g1", events[0].GroupID)
}

func TestHandler_EndpointReads_InvalidCustomRangeReturns400(t *testing.T) {
	h, _, sids, _ := newEndpointsHandlerWithRoles(t)
	for _, path := range []string{"/api/endpoints?from=not-a-date", "/api/endpoints/ep-1/events?to=not-a-date"} {
		rec := doWithSession(t, h, http.MethodGet, path, sids[RoleAdmin], "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, path+": "+rec.Body.String())
	}
}

func TestHandler_TriggerScan_AdminCanRequestScan(t *testing.T) {
	h, _, enrollment, sid := newEnrollHandlerWithAdmin(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "HT-PC", APIKeyID: "key-1"}))

	rec := doWithSession(t, h, http.MethodPost, "/api/agents/agent-1/scan", sid, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	agent, ok := enrollment.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.True(t, agent.ScanRequested)
	assert.Equal(t, "pending", agent.ScanState)
}

func TestHandler_TriggerScan_UnknownAgentReturns404(t *testing.T) {
	h, _, _, sid := newEnrollHandlerWithAdmin(t)
	rec := doWithSession(t, h, http.MethodPost, "/api/agents/does-not-exist/scan", sid, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_TriggerScan_NoSessionReturns401(t *testing.T) {
	h, _, enrollment, _ := newEnrollHandlerWithAdmin(t)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	rec := doWithSession(t, h, http.MethodPost, "/api/agents/agent-1/scan", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_Heartbeat_ReturnsScanRequestedAndClearsFlag(t *testing.T) {
	h, groups, enrollment, sid := newEnrollHandlerWithAdmin(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "HT-PC", GroupID: group.ID}))
	plainKey, key, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)
	require.NoError(t, enrollment.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "1.2.3.4", "10.0.0.5", group.ID, key.ID))

	requestRec := doWithSession(t, h, http.MethodPost, "/api/agents/agent-1/scan", sid, "")
	require.Equal(t, http.StatusOK, requestRec.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"version":"0.18.10","os":"windows","arch":"amd64"}`))
	req.Header.Set("Authorization", plainKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["scan_requested"])

	// Second heartbeat must not re-dispatch (fire-once).
	req2 := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"version":"0.18.10","os":"windows","arch":"amd64"}`))
	req2.Header.Set("Authorization", plainKey)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp2))
	assert.Equal(t, false, resp2["scan_requested"])
}

func TestHandler_Policy_ReturnsCurrentPolicyForValidAPIKey(t *testing.T) {
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	enrollment, err := NewEnrollmentStore(dataDir)
	require.NoError(t, err)
	policy, err := NewPolicyStore(dataDir)
	require.NoError(t, err)
	require.NoError(t, policy.AddRule("block", PolicyRule{Ecosystem: "npm", Name: "handlebars", Version: "4.7.9"}))

	h := Handler(dataDir, HandlerDeps{Groups: groups, Enrollment: enrollment, Policy: policy})

	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	plainKey, _, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/policy", nil)
	req.Header.Set("Authorization", plainKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp struct {
		Policy Policy `json:"policy"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Policy.Blocklist, 1)
	assert.Equal(t, "handlebars", resp.Policy.Blocklist[0].Name)
}

func TestHandler_Policy_InvalidAPIKeyReturns401(t *testing.T) {
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	h := Handler(dataDir, HandlerDeps{Groups: groups})

	req := httptest.NewRequest(http.MethodGet, "/api/policy", nil)
	req.Header.Set("Authorization", "not-a-real-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_Policy_NoAPIKeyReturns401(t *testing.T) {
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	h := Handler(dataDir, HandlerDeps{Groups: groups})

	req := httptest.NewRequest(http.MethodGet, "/api/policy", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_ScanReport_StartedUpdatesState(t *testing.T) {
	h, groups, enrollment, _ := newEnrollHandlerWithAdmin(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "placeholder"}))
	plainKey, key, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)
	require.NoError(t, enrollment.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "", "", group.ID, key.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/scan-report", strings.NewReader(`{"status":"started"}`))
	req.Header.Set("Authorization", plainKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	agent, _ := enrollment.GetAgentByID("agent-1")
	assert.Equal(t, "running", agent.ScanState)
}

func TestHandler_ScanReport_CompletedStoresFindings(t *testing.T) {
	h, groups, enrollment, _ := newEnrollHandlerWithAdmin(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "placeholder"}))
	plainKey, key, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)
	require.NoError(t, enrollment.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "", "", group.ID, key.ID))

	body := `{"status":"completed","findings":[{"ecosystem":"npm","name":"evil-pkg","version":"6.6.6","verdict":"known malware","paths":["/a"],"remove_hint":"npm uninstall evil-pkg"}],"summary":{"total_paths_scanned":10,"unique_packages":5,"flagged_count":1}}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan-report", strings.NewReader(body))
	req.Header.Set("Authorization", plainKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	agent, _ := enrollment.GetAgentByID("agent-1")
	assert.Equal(t, "completed", agent.ScanState)
	require.Len(t, agent.Findings, 1)
	assert.Equal(t, "evil-pkg", agent.Findings[0].Name)
}

func TestHandler_ScanReport_NoAPIKeyReturns401(t *testing.T) {
	h, _, _, _ := newEnrollHandlerWithAdmin(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scan-report", strings.NewReader(`{"status":"started"}`)))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_EcosystemFindings_AdminOnly(t *testing.T) {
	h, groups, enrollment, sid := newEnrollHandlerWithAdmin(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", Hostname: "HT-PC", APIKeyID: "placeholder"}))
	_, key, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)
	require.NoError(t, enrollment.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "", "", group.ID, key.ID))
	require.NoError(t, enrollment.RecordScanCompleted(key.ID,
		[]EcosystemFinding{{Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6"}}, nil, EcosystemScanSummary{}))

	rec := doWithSession(t, h, http.MethodGet, "/api/ecosystem/findings", sid, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var views []EcosystemFindingView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &views))
	require.Len(t, views, 1)
	assert.Equal(t, "evil-pkg", views[0].Name)
	assert.Equal(t, "HT-PC", views[0].Hostname)

	unauthedRec := doWithSession(t, h, http.MethodGet, "/api/ecosystem/findings", "", "")
	assert.Equal(t, http.StatusUnauthorized, unauthedRec.Code)
}

func TestHandler_EcosystemSummary_ReturnsAggregateCounts(t *testing.T) {
	h, groups, enrollment, sid := newEnrollHandlerWithAdmin(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)
	require.NoError(t, enrollment.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "placeholder"}))
	_, key, err := groups.CreateAPIKey(group.ID, "agent-1 key")
	require.NoError(t, err)
	require.NoError(t, enrollment.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "", "", group.ID, key.ID))
	require.NoError(t, enrollment.RecordScanCompleted(key.ID,
		[]EcosystemFinding{{Name: "evil-pkg"}}, nil, EcosystemScanSummary{}))

	rec := doWithSession(t, h, http.MethodGet, "/api/ecosystem/summary", sid, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var summary EcosystemFleetSummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &summary))
	assert.Equal(t, 1, summary.AgentsScanned)
	assert.Equal(t, 1, summary.TotalFindings)
}

func newGroupsHandlerWithEditor(t *testing.T) (http.Handler, *GroupStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	groups, err := NewGroupStore(dataDir)
	require.NoError(t, err)
	users, err := NewUserStore(dataDir, "seed-admin", "seed-password-123")
	require.NoError(t, err)
	sessions := NewSessionStore()
	sid, err := sessions.Create(DashUser{ID: "u2", Username: "editor", Role: RoleEditor})
	require.NoError(t, err)
	h := Handler(dataDir, HandlerDeps{Groups: groups, Users: users, Sessions: sessions})
	return h, groups, sid
}

func TestHandler_Groups_EditorCanListGroups(t *testing.T) {
	h, groups, sid := newGroupsHandlerWithEditor(t)
	group, err := groups.CreateGroup("vega")
	require.NoError(t, err)

	rec := doWithSession(t, h, http.MethodGet, "/api/groups", sid, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var rows []struct {
		Group
		KeyCount int `json:"key_count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, group.ID, rows[0].ID)
}

func TestHandler_Groups_EditorCannotCreateGroup(t *testing.T) {
	h, _, sid := newGroupsHandlerWithEditor(t)

	rec := doWithSession(t, h, http.MethodPost, "/api/groups", sid, `{"name":"nova"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
