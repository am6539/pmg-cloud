package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		[]EcosystemFinding{{Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6"}}, EcosystemScanSummary{}))

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
		[]EcosystemFinding{{Name: "evil-pkg"}}, EcosystemScanSummary{}))

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
