# Ecosystem Scan — pmg-cloud (Server + Dashboard) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a dashboard admin trigger a machine-wide malware scan on an enrolled agent, receive
the scan's progress/results over a new plain HTTP+JSON endpoint, and view findings across the fleet
in a new "Ecosystem" dashboard tab.

**Architecture:** Scan state (`ScanRequested`/`ScanState`/`LastScanAt`/`Findings`) is added directly
to the existing `Agent` record in `EnrollmentStore` (same JSON-backed store already used for agent
enrollment). An admin's `POST /api/agents/{id}/scan` sets the flag; the agent's existing
`POST /api/heartbeat` poll picks it up and clears it (fire-once); a new `POST /api/scan-report`
(agent-authenticated, same style as heartbeat) receives progress/results. Two new admin-only GET
endpoints expose fleet-wide findings for a new dashboard tab.

**Tech Stack:** Go 1.25, existing `dashboard` package conventions (JSON file stores, plain
`net/http` mux with manual auth checks, vanilla JS dashboard in `static/index.html`), testify.

**Full design context:** `docs/superpowers/specs/2026-07-02-ecosystem-scan-design.md` (this spec
lives in the sibling `pmg` repo at the path noted in that repo's plan; read it there before
starting — this plan implements only the pmg-cloud side. The agent-side plan
(`2026-07-02-ecosystem-scan-agent.md` in the `pmg` repo) is a separate, independently-shippable
piece that calls the endpoints this plan builds).

## Global Constraints

- Report-only in v1 — pmg-cloud never tells an agent to remove anything; findings are
  display/audit only.
- The admin-facing endpoint (`POST /api/agents/{id}/scan`) is session-authenticated (admin role
  only); the agent-facing endpoint (`POST /api/scan-report`) is API-key authenticated, exactly like
  `POST /api/heartbeat` — the agent does not know its own dashboard-internal `{id}`, only its API
  key, so the server resolves the target agent from the key.
- **Deploy this plan (pmg-cloud) before the agent-side plan ships** — an older agent talking to a
  newer server is unaffected (it just never receives `scan_requested: true`); a newer agent talking
  to an older server would get 404s from `/api/scan-report` and the scan report would be lost. See
  the design spec's Rollout Order section.
- Follow existing project conventions exactly: testify `assert`/`require`, the existing
  `EnrollmentStore` JSON-file-store pattern (`dashboard/enrollment.go`), the existing manual
  session/API-key auth checks already used throughout `dashboard/handler.go` (no framework
  middleware beyond what's already there).
- The "Ecosystem" tab and its data are admin-only, since findings expose real filesystem paths
  (potentially containing usernames/internal structure) from employee machines.

---

### Task 1: Agent scan-state fields and EnrollmentStore methods

**Files:**
- Modify: `dashboard/enrollment.go` (extend the `Agent` struct at lines 39-53)
- Create: `dashboard/ecosystem.go`
- Test: `dashboard/ecosystem_test.go`

**Interfaces:**
- Produces: `EcosystemFinding`, `EcosystemScanSummary`, `EcosystemFindingView`,
  `EcosystemFleetSummary` types; `Agent.ScanRequested/ScanState/ScanDispatchedAt/LastScanAt/LastScanSummary/Findings` fields;
  `(es *EnrollmentStore) RequestScan(agentID string) error`,
  `ConsumeScanRequest(keyID string) bool`,
  `RecordScanStarted(keyID string) error`,
  `RecordScanCompleted(keyID string, findings []EcosystemFinding, summary EcosystemScanSummary) error`,
  `ListEcosystemFindings() []EcosystemFindingView`,
  `EcosystemFleetSummaryStats() EcosystemFleetSummary`.
- Consumed by: Task 2's HTTP handlers.

- [ ] **Step 1: Write the failing tests**

```go
// dashboard/ecosystem_test.go
package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestScan_SetsFlagAndPendingState(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RequestScan("agent-1"))

	agent, ok := es.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.True(t, agent.ScanRequested)
	assert.Equal(t, "pending", agent.ScanState)
}

func TestRequestScan_UnknownAgentReturnsError(t *testing.T) {
	es := newTestEnrollmentStore(t)
	err := es.RequestScan("does-not-exist")
	assert.Error(t, err)
}

func TestConsumeScanRequest_ClearsFlagAndDispatches(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RequestScan("agent-1"))

	dispatched := es.ConsumeScanRequest("key-1")
	assert.True(t, dispatched)

	agent, _ := es.GetAgentByID("agent-1")
	assert.False(t, agent.ScanRequested)
	assert.Equal(t, "dispatched", agent.ScanState)
	assert.NotNil(t, agent.ScanDispatchedAt)
}

func TestConsumeScanRequest_FireOnce(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RequestScan("agent-1"))

	require.True(t, es.ConsumeScanRequest("key-1"))
	assert.False(t, es.ConsumeScanRequest("key-1"), "a second poll must not re-dispatch")
}

func TestConsumeScanRequest_NoPendingRequestReturnsFalse(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	assert.False(t, es.ConsumeScanRequest("key-1"))
}

func TestRecordScanStarted_SetsRunningState(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RecordScanStarted("key-1"))

	agent, _ := es.GetAgentByID("agent-1")
	assert.Equal(t, "running", agent.ScanState)
}

func TestRecordScanCompleted_StoresFindingsAndSummary(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1", Hostname: "HT-PC", OS: "windows"}))

	findings := []EcosystemFinding{{
		Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6",
		Verdict: "known malware", Paths: []string{"/a/node_modules/evil-pkg"},
		RemoveHint: "npm uninstall evil-pkg",
	}}
	summary := EcosystemScanSummary{TotalPathsScanned: 10, UniquePackages: 5, FlaggedCount: 1}

	require.NoError(t, es.RecordScanCompleted("key-1", findings, summary))

	agent, _ := es.GetAgentByID("agent-1")
	assert.Equal(t, "completed", agent.ScanState)
	require.NotNil(t, agent.LastScanAt)
	require.NotNil(t, agent.LastScanSummary)
	assert.Equal(t, 1, agent.LastScanSummary.FlaggedCount)
	require.Len(t, agent.Findings, 1)
	assert.Equal(t, "evil-pkg", agent.Findings[0].Name)
}

func TestRecordScanCompleted_ReplacesPreviousFindings(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "old-finding"}}, EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "new-finding"}}, EcosystemScanSummary{}))

	agent, _ := es.GetAgentByID("agent-1")
	require.Len(t, agent.Findings, 1)
	assert.Equal(t, "new-finding", agent.Findings[0].Name)
}

func TestListEcosystemFindings_EnrichesWithAgentIdentityAndSkipsRemoved(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1", Hostname: "HT-PC", OS: "windows"}))
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-2", APIKeyID: "key-2", Hostname: "removed-box", Removed: true}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Ecosystem: "npm", Name: "evil-pkg", Version: "6.6.6"}},
		EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-2",
		[]EcosystemFinding{{Ecosystem: "npm", Name: "should-not-appear", Version: "1.0.0"}},
		EcosystemScanSummary{}))

	views := es.ListEcosystemFindings()
	require.Len(t, views, 1)
	assert.Equal(t, "HT-PC", views[0].Hostname)
	assert.Equal(t, "windows", views[0].OS)
	assert.Equal(t, "evil-pkg", views[0].Name)
}

func TestEcosystemFleetSummaryStats_AggregatesAcrossAgents(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-1", APIKeyID: "key-1"}))
	require.NoError(t, es.RegisterAgent(Agent{ID: "agent-2", APIKeyID: "key-2"}))

	require.NoError(t, es.RecordScanCompleted("key-1",
		[]EcosystemFinding{{Name: "a"}, {Name: "b"}}, EcosystemScanSummary{}))
	require.NoError(t, es.RecordScanCompleted("key-2",
		[]EcosystemFinding{{Name: "c"}}, EcosystemScanSummary{}))

	summary := es.EcosystemFleetSummaryStats()
	assert.Equal(t, 2, summary.AgentsScanned)
	assert.Equal(t, 3, summary.TotalFindings)
	require.NotNil(t, summary.LastScanAt)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./dashboard/... -run "TestRequestScan|TestConsumeScanRequest|TestRecordScan|TestListEcosystemFindings|TestEcosystemFleetSummaryStats" -v`
Expected: FAIL — `undefined: EcosystemFinding` (and related)

- [ ] **Step 3: Extend the `Agent` struct in `dashboard/enrollment.go`**

Replace the existing struct (lines 39-53):

```go
// Agent is a registered machine that enrolled via an EnrollmentToken.
type Agent struct {
	ID         string     `json:"id"`
	Hostname   string     `json:"hostname"`
	Label      string     `json:"label,omitempty"` // admin-assigned friendly name (e.g. owner/dev)
	OS         string     `json:"os"`
	Arch       string     `json:"arch"`
	PMGVersion string     `json:"pmg_version,omitempty"`
	RemoteIP   string     `json:"remote_ip,omitempty"`
	LocalIP    string     `json:"local_ip,omitempty"`
	GroupID    string     `json:"group_id,omitempty"`
	APIKeyID   string     `json:"api_key_id"`
	EnrolledAt time.Time  `json:"enrolled_at"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	Removed    bool       `json:"removed,omitempty"` // true = agent removed but events kept for audit

	// Ecosystem scan state: set by an admin's POST /api/agents/{id}/scan,
	// consumed (fire-once) by the agent's next heartbeat poll, and updated as
	// the agent reports progress via POST /api/scan-report.
	ScanRequested    bool                  `json:"scan_requested,omitempty"`
	ScanState        string                `json:"scan_state,omitempty"` // idle|pending|dispatched|running|completed
	ScanDispatchedAt *time.Time            `json:"scan_dispatched_at,omitempty"`
	LastScanAt       *time.Time            `json:"last_scan_at,omitempty"`
	LastScanSummary  *EcosystemScanSummary `json:"last_scan_summary,omitempty"`
	Findings         []EcosystemFinding    `json:"findings,omitempty"`
}
```

- [ ] **Step 4: Write `dashboard/ecosystem.go`**

```go
// dashboard/ecosystem.go
package dashboard

import (
	"fmt"
	"time"
)

// EcosystemFinding is a single malicious package found on an agent's disk
// during a machine-wide ecosystem scan, as reported by POST /api/scan-report.
type EcosystemFinding struct {
	Ecosystem    string   `json:"ecosystem"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Verdict      string   `json:"verdict"`
	ReferenceURL string   `json:"reference_url,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	RemoveHint   string   `json:"remove_hint,omitempty"`
}

// EcosystemScanSummary carries aggregate counters for one completed ecosystem scan.
type EcosystemScanSummary struct {
	TotalPathsScanned  int     `json:"total_paths_scanned"`
	UniquePackages     int     `json:"unique_packages"`
	FlaggedCount       int     `json:"flagged_count"`
	SkippedDirs        int     `json:"skipped_dirs"`
	SkippedCloudChecks int     `json:"skipped_cloud_checks"`
	DurationSeconds    float64 `json:"duration_seconds"`
}

// EcosystemFindingView is a single finding enriched with the identity of the
// agent it was found on, for the dashboard's fleet-wide findings table.
type EcosystemFindingView struct {
	AgentID      string     `json:"agent_id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Ecosystem    string     `json:"ecosystem"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Verdict      string     `json:"verdict"`
	ReferenceURL string     `json:"reference_url,omitempty"`
	Paths        []string   `json:"paths,omitempty"`
	RemoveHint   string     `json:"remove_hint,omitempty"`
	DetectedAt   *time.Time `json:"detected_at,omitempty"`
}

// EcosystemFleetSummary is the aggregate counts shown as dashboard summary cards.
type EcosystemFleetSummary struct {
	AgentsScanned int        `json:"agents_scanned"`
	TotalFindings int        `json:"total_findings"`
	LastScanAt    *time.Time `json:"last_scan_at,omitempty"`
}

// RequestScan flags the agent identified by agentID for a scan on its next
// heartbeat poll.
func (es *EnrollmentStore) RequestScan(agentID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == agentID {
			es.data.Agents[i].ScanRequested = true
			es.data.Agents[i].ScanState = "pending"
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}

// ConsumeScanRequest checks whether the agent identified by keyID has a
// pending scan request. If so, it clears the flag (fire-once — the caller is
// responsible for delivering this to the agent in the same response) and
// marks the agent "dispatched". Returns whether a scan was dispatched.
func (es *EnrollmentStore) ConsumeScanRequest(keyID string) bool {
	if keyID == "" {
		return false
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID && a.ScanRequested {
			es.data.Agents[i].ScanRequested = false
			es.data.Agents[i].ScanState = "dispatched"
			now := time.Now().UTC()
			es.data.Agents[i].ScanDispatchedAt = &now
			_ = es.save()
			return true
		}
	}
	return false
}

// RecordScanStarted marks the agent identified by keyID as actively running a
// scan. A miss (no matching agent) is non-fatal, mirroring TouchAgentByAPIKeyID.
func (es *EnrollmentStore) RecordScanStarted(keyID string) error {
	if keyID == "" {
		return nil
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID {
			es.data.Agents[i].ScanState = "running"
			return es.save()
		}
	}
	return nil
}

// RecordScanCompleted stores findings and the scan summary for the agent
// identified by keyID, replacing any findings from a previous scan.
func (es *EnrollmentStore) RecordScanCompleted(keyID string, findings []EcosystemFinding, summary EcosystemScanSummary) error {
	if keyID == "" {
		return nil
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	now := time.Now().UTC()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID {
			es.data.Agents[i].ScanState = "completed"
			es.data.Agents[i].LastScanAt = &now
			es.data.Agents[i].LastScanSummary = &summary
			es.data.Agents[i].Findings = findings
			return es.save()
		}
	}
	return nil
}

// ListEcosystemFindings returns every finding across all non-removed agents,
// enriched with agent identity, for the dashboard's fleet-wide findings table.
func (es *EnrollmentStore) ListEcosystemFindings() []EcosystemFindingView {
	es.mu.RLock()
	defer es.mu.RUnlock()
	var out []EcosystemFindingView
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		for _, f := range a.Findings {
			out = append(out, EcosystemFindingView{
				AgentID:      a.ID,
				Hostname:     a.Hostname,
				OS:           a.OS,
				Ecosystem:    f.Ecosystem,
				Name:         f.Name,
				Version:      f.Version,
				Verdict:      f.Verdict,
				ReferenceURL: f.ReferenceURL,
				Paths:        f.Paths,
				RemoveHint:   f.RemoveHint,
				DetectedAt:   a.LastScanAt,
			})
		}
	}
	return out
}

// EcosystemFleetSummaryStats returns fleet-wide aggregate counts for the
// dashboard's Ecosystem tab summary cards.
func (es *EnrollmentStore) EcosystemFleetSummaryStats() EcosystemFleetSummary {
	es.mu.RLock()
	defer es.mu.RUnlock()
	var summary EcosystemFleetSummary
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		if a.LastScanAt != nil {
			summary.AgentsScanned++
			if summary.LastScanAt == nil || a.LastScanAt.After(*summary.LastScanAt) {
				summary.LastScanAt = a.LastScanAt
			}
		}
		summary.TotalFindings += len(a.Findings)
	}
	return summary
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./dashboard/... -run "TestRequestScan|TestConsumeScanRequest|TestRecordScan|TestListEcosystemFindings|TestEcosystemFleetSummaryStats" -v`
Expected: PASS

- [ ] **Step 6: Run the full dashboard package test suite**

Run: `go test ./dashboard/... -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add dashboard/enrollment.go dashboard/ecosystem.go dashboard/ecosystem_test.go
git commit -m "feat: add ecosystem scan state fields and EnrollmentStore methods"
```

---

### Task 2: HTTP endpoints

**Files:**
- Modify: `dashboard/handler.go`
  - Heartbeat handler (lines 1624-1671)
  - `/api/agents/` handler (lines 1691-1796)
  - `sessionMiddleware`'s `unauthAPI` map (lines 2109-2114)
  - Insert two new route registrations inside the `if deps.Enrollment != nil { ... }` block
- Test: `dashboard/handler_test.go` (new test functions appended)

**Interfaces:**
- Consumes: `EnrollmentStore.RequestScan/ConsumeScanRequest/RecordScanStarted/RecordScanCompleted/ListEcosystemFindings/EcosystemFleetSummaryStats` (Task 1), `EcosystemFinding`/`EcosystemScanSummary` (Task 1), existing `deps.Groups.ResolveKeyWithID`, `sessionFromContext`, `RoleAdmin`, `writeJSON`.
- Produces: `POST /api/agents/{id}/scan`, `POST /api/scan-report`, `GET /api/ecosystem/findings`, `GET /api/ecosystem/summary`; `scan_requested` field in the `/api/heartbeat` JSON response.

- [ ] **Step 1: Write the failing tests**

Append to `dashboard/handler_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./dashboard/... -run "TestHandler_TriggerScan|TestHandler_Heartbeat_ReturnsScanRequested|TestHandler_ScanReport|TestHandler_Ecosystem" -v`
Expected: FAIL — `undefined: newTestSessionsWithAdmin` and 404/401s on routes that don't exist yet

- [ ] **Step 3: Add `/api/scan-report` to `sessionMiddleware`'s `unauthAPI` map**

This is the most important step to get right — miss it and every real agent request to
`/api/scan-report` gets rejected with 401 by the session middleware before it ever reaches the
handler (agents authenticate via API key header, not a session cookie).

Replace (`dashboard/handler.go:2109-2114`):

```go
	unauthAPI := map[string]bool{
		"/api/me":        true,
		"/api/enroll":    true,
		"/api/heartbeat": true, // agent API key auth, not session
		"/api/sync":      true, // agent sync, not session
	}
```

with:

```go
	unauthAPI := map[string]bool{
		"/api/me":          true,
		"/api/enroll":      true,
		"/api/heartbeat":   true, // agent API key auth, not session
		"/api/sync":        true, // agent sync, not session
		"/api/scan-report": true, // agent API key auth, not session
	}
```

- [ ] **Step 4: Extend the heartbeat handler to consume and return `scan_requested`**

Replace (`dashboard/handler.go:1624-1671`):

```go
		// POST /api/heartbeat — agent-authenticated; updates LastSeen and returns update info
		mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if deps.Groups == nil {
				writeJSON(w, AgentUpdateInfo{})
				return
			}
			apiKey := r.Header.Get("Authorization")
			if apiKey == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_, keyID, ok := deps.Groups.ResolveKeyWithID(apiKey)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			var req struct {
				Version string `json:"version"`
				OS      string `json:"os"`
				Arch    string `json:"arch"`
				LocalIP string `json:"local_ip"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if deps.Enrollment != nil {
				if err := enrollment.TouchAgentByAPIKeyID(keyID, req.Version, req.LocalIP, realIP(r)); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
			var info AgentUpdateInfo
			if deps.Updates != nil && req.OS != "" && req.Arch != "" {
				info = deps.Updates.UpdateInfoForAgent(req.OS, req.Arch, req.Version)
			}
			resp := map[string]any{
				"update_available": info.UpdateAvailable,
				"version":          info.Version,
				"download_url":     info.DownloadURL,
				"sha256":           info.SHA256,
			}
			if deps.Policy != nil {
				resp["policy"] = deps.Policy.Get()
			}
			writeJSON(w, resp)
		})
```

with:

```go
		// POST /api/heartbeat — agent-authenticated; updates LastSeen, returns
		// update info, and (fire-once) whether a scan was requested for this agent.
		mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if deps.Groups == nil {
				writeJSON(w, AgentUpdateInfo{})
				return
			}
			apiKey := r.Header.Get("Authorization")
			if apiKey == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_, keyID, ok := deps.Groups.ResolveKeyWithID(apiKey)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			var req struct {
				Version string `json:"version"`
				OS      string `json:"os"`
				Arch    string `json:"arch"`
				LocalIP string `json:"local_ip"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			scanRequested := false
			if deps.Enrollment != nil {
				if err := enrollment.TouchAgentByAPIKeyID(keyID, req.Version, req.LocalIP, realIP(r)); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				scanRequested = enrollment.ConsumeScanRequest(keyID)
			}
			var info AgentUpdateInfo
			if deps.Updates != nil && req.OS != "" && req.Arch != "" {
				info = deps.Updates.UpdateInfoForAgent(req.OS, req.Arch, req.Version)
			}
			resp := map[string]any{
				"update_available": info.UpdateAvailable,
				"version":          info.Version,
				"download_url":     info.DownloadURL,
				"sha256":           info.SHA256,
				"scan_requested":   scanRequested,
			}
			if deps.Policy != nil {
				resp["policy"] = deps.Policy.Get()
			}
			writeJSON(w, resp)
		})

		// POST /api/scan-report — agent-authenticated; records ecosystem scan
		// progress/results reported by the agent's heartbeat-triggered scan.
		mux.HandleFunc("/api/scan-report", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if deps.Groups == nil || deps.Enrollment == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			apiKey := r.Header.Get("Authorization")
			if apiKey == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			_, keyID, ok := deps.Groups.ResolveKeyWithID(apiKey)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			var req struct {
				Status   string                 `json:"status"`
				Findings []EcosystemFinding     `json:"findings"`
				Summary  *EcosystemScanSummary  `json:"summary"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}

			switch req.Status {
			case "started":
				if err := enrollment.RecordScanStarted(keyID); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			case "completed":
				summary := EcosystemScanSummary{}
				if req.Summary != nil {
					summary = *req.Summary
				}
				if err := enrollment.RecordScanCompleted(keyID, req.Findings, summary); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("agent_scan_completed", keyID, fmt.Sprintf("flagged=%d", summary.FlaggedCount))
				}
			default:
				http.Error(w, "invalid status", http.StatusBadRequest)
				return
			}

			writeJSON(w, map[string]bool{"ok": true})
		})
```

- [ ] **Step 5: Extend the `/api/agents/` handler with the `/scan` admin action**

Replace the start of the handler (`dashboard/handler.go:1703-1709`):

```go
			agentID := strings.TrimPrefix(r.URL.Path, "/api/agents/")
			if agentID == "" {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodPut:
```

with:

```go
			rest := strings.TrimPrefix(r.URL.Path, "/api/agents/")
			if rest == "" {
				http.NotFound(w, r)
				return
			}

			if scanAgentID, isScan := strings.CutSuffix(rest, "/scan"); isScan {
				if r.Method != http.MethodPost {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				if s.Role != RoleAdmin {
					http.Error(w, `{"error":"only admins can trigger a scan"}`, http.StatusForbidden)
					return
				}
				if _, exists := enrollment.GetAgentByID(scanAgentID); !exists {
					http.NotFound(w, r)
					return
				}
				if err := enrollment.RequestScan(scanAgentID); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("agent_scan_requested", scanAgentID, "")
				}
				writeJSON(w, map[string]bool{"ok": true})
				return
			}

			agentID := rest
			switch r.Method {
			case http.MethodPut:
```

The rest of the existing `switch r.Method` block (PUT/DELETE cases) is unchanged — it still refers
to `agentID`, which is now assigned from `rest` instead of being computed directly.

- [ ] **Step 6: Add the two `GET /api/ecosystem/*` read endpoints**

Insert immediately after the closing `})` of the `/api/agents/` handler (i.e. right before the
`// GET /api/enrollment-tokens` comment at `dashboard/handler.go:1798`):

```go
		// GET /api/ecosystem/findings — admin only; fleet-wide malware findings from ecosystem scans
		mux.HandleFunc("/api/ecosystem/findings", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, enrollment.ListEcosystemFindings())
		})

		// GET /api/ecosystem/summary — admin only; fleet-wide scan summary counts
		mux.HandleFunc("/api/ecosystem/summary", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, enrollment.EcosystemFleetSummaryStats())
		})

```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./dashboard/... -run "TestHandler_TriggerScan|TestHandler_Heartbeat_ReturnsScanRequested|TestHandler_ScanReport|TestHandler_Ecosystem" -v`
Expected: PASS

- [ ] **Step 8: Run the full dashboard package test suite and the full build**

Run: `go build ./... && go test ./dashboard/... -count=1`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add dashboard/handler.go dashboard/handler_test.go
git commit -m "feat: add scan trigger, scan-report, and ecosystem findings/summary endpoints"
```

---

### Task 3: Dashboard "Ecosystem" tab UI

**Files:**
- Modify: `dashboard/static/index.html`
  - Nav bar (insert after line 197)
  - `PAGE_TITLES` map (line 484)
  - `loadPage()` dispatcher (lines 506-522)
  - `renderAgents()` (lines 1161-1222) — add a "Scan" column and a "Scan" action button
  - Append a new `renderEcosystemScan()` function and its two helpers (`triggerScan`,
    `scanStateBadge`) near the end of the Agents section (after `deleteAgentEvents`, i.e. after
    line 1401)

**Interfaces:**
- Consumes: `GET /api/ecosystem/findings`, `GET /api/ecosystem/summary`,
  `POST /api/agents/{id}/scan` (Task 2); existing JS helpers `api()`, `h()`, `bdg()`, `ecoBdg()`,
  `fmtDate()`, `showToast()`, `S.me.role`.

This task has no Go test — it's static HTML/JS served via `embed.FS` (`dashboard/static/index.html`
is already covered by the project's existing lack of a JS test harness; verification here is
build + manual browser check, matching how every other tab in this file was built).

- [ ] **Step 1: Add the nav item**

Insert after the existing Agents nav item (`dashboard/static/index.html:197`):

```html
    <div class="nav-item" onclick="nav('agents')" data-page="agents">&#128290; Agents</div>
    <div class="nav-item admin-only" onclick="nav('ecosystem')" data-page="ecosystem">&#129514; Ecosystem</div>
```

- [ ] **Step 2: Register the page title and dispatch**

Replace (`dashboard/static/index.html:484`):

```js
var PAGE_TITLES={dashboard:'Overview',events:'Events',endpoints:'Endpoints',packages:'Packages',cicd:'CI / CD',malware:'Malware Feed',agents:'Agents',groups:'Groups',audit:'Audit Log',settings:'Settings'};
```

with:

```js
var PAGE_TITLES={dashboard:'Overview',events:'Events',endpoints:'Endpoints',packages:'Packages',cicd:'CI / CD',malware:'Malware Feed',agents:'Agents',ecosystem:'Ecosystem',groups:'Groups',audit:'Audit Log',settings:'Settings'};
```

Replace the `loadPage()` dispatcher (`dashboard/static/index.html:506-522`):

```js
async function loadPage(){
  arN=30;
  var el=document.getElementById('content');
  el.innerHTML='<div class="empty"><span class="spin">&#8635;</span> Loading&#8230;</div>';
  try{
    if(S.page==='dashboard')await renderDashboard();
    else if(S.page==='events')await renderEvents();
    else if(S.page==='endpoints')await renderEndpoints();
    else if(S.page==='packages')await renderPackages();
    else if(S.page==='cicd')await renderCICD();
    else if(S.page==='malware')await renderMalware();
    else if(S.page==='agents')await renderAgents();
    else if(S.page==='groups')await renderGroups();
    else if(S.page==='audit')await renderAudit();
    else if(S.page==='settings')await renderSettings();
  }catch(e){document.getElementById('content').innerHTML='<div class="empty">&#9888;&#65039; '+h(e.message)+'</div>';}
}
```

with:

```js
async function loadPage(){
  arN=30;
  var el=document.getElementById('content');
  el.innerHTML='<div class="empty"><span class="spin">&#8635;</span> Loading&#8230;</div>';
  try{
    if(S.page==='dashboard')await renderDashboard();
    else if(S.page==='events')await renderEvents();
    else if(S.page==='endpoints')await renderEndpoints();
    else if(S.page==='packages')await renderPackages();
    else if(S.page==='cicd')await renderCICD();
    else if(S.page==='malware')await renderMalware();
    else if(S.page==='agents')await renderAgents();
    else if(S.page==='ecosystem')await renderEcosystemScan();
    else if(S.page==='groups')await renderGroups();
    else if(S.page==='audit')await renderAudit();
    else if(S.page==='settings')await renderSettings();
  }catch(e){document.getElementById('content').innerHTML='<div class="empty">&#9888;&#65039; '+h(e.message)+'</div>';}
}
```

- [ ] **Step 3: Add a "Scan" column and action button to `renderAgents()`**

Replace the table header line (`dashboard/static/index.html:1179`):

```js
'<div class="tbl-wrap"><table><thead><tr><th>Status</th><th>Device</th><th>OS / Arch</th><th>IP Address</th><th>Group</th><th>Enrolled</th><th>Last Seen</th><th></th></tr></thead><tbody>'+
```

with:

```js
'<div class="tbl-wrap"><table><thead><tr><th>Status</th><th>Device</th><th>OS / Arch</th><th>IP Address</th><th>Group</th><th>Enrolled</th><th>Last Seen</th><th>Scan</th><th></th></tr></thead><tbody>'+
```

Replace the row-building `return` statement (`dashboard/static/index.html:1189-1202`):

```js
  return'<tr>'+statusCell+
  '<td>'+deviceCell+'</td>'+
  '<td style="font-size:12px;color:var(--tx3)">'+h(a.os||'')+(a.arch?' / '+h(a.arch):'')+(a.pmg_version?' &nbsp;<code>v'+h(a.pmg_version)+'</code>':'')+'</td>'+
  '<td class="mono" style="font-size:11px">'+(a.local_ip?'<div>'+h(a.local_ip)+'</div>':'')+(a.remote_ip?'<div style="color:var(--tx4)">'+h(a.remote_ip)+'</div>':'—')+'</td>'+
  '<td>'+(grp?bdg(grp.name,'teal'):'<span style="color:var(--tx4)">unassigned</span>')+'</td>'+
  '<td style="font-size:12px;color:var(--tx3)">'+fmtDate(a.enrolled_at)+'</td>'+
  '<td style="font-size:12px;color:'+(st.label==='Offline'?'var(--dn)':'var(--tx3)')+'">'+fmtDate(a.last_seen)+'</td>'+
  '<td class="flex" style="gap:4px">'+
  (S.me&&(S.me.role==='admin'||S.me.role==='editor')?
    '<button class="btn btn-sm btn-ghost" onclick="renameAgent(\''+h(a.id)+'\',\''+h(a.label||'')+'\')">Rename</button>':'')+
  (S.me&&S.me.role==='admin'?
    '<button class="btn btn-sm btn-ghost" onclick="openAssignGroup(\''+h(a.id)+'\',\''+h(a.group_id||'')+'\')">Group</button>'+
    '<button class="btn btn-sm btn-danger" onclick="removeAgent(\''+h(a.id)+'\',\''+h(a.hostname)+'\')">Remove</button>':'')+
  '</td></tr>';
```

with:

```js
  return'<tr>'+statusCell+
  '<td>'+deviceCell+'</td>'+
  '<td style="font-size:12px;color:var(--tx3)">'+h(a.os||'')+(a.arch?' / '+h(a.arch):'')+(a.pmg_version?' &nbsp;<code>v'+h(a.pmg_version)+'</code>':'')+'</td>'+
  '<td class="mono" style="font-size:11px">'+(a.local_ip?'<div>'+h(a.local_ip)+'</div>':'')+(a.remote_ip?'<div style="color:var(--tx4)">'+h(a.remote_ip)+'</div>':'—')+'</td>'+
  '<td>'+(grp?bdg(grp.name,'teal'):'<span style="color:var(--tx4)">unassigned</span>')+'</td>'+
  '<td style="font-size:12px;color:var(--tx3)">'+fmtDate(a.enrolled_at)+'</td>'+
  '<td style="font-size:12px;color:'+(st.label==='Offline'?'var(--dn)':'var(--tx3)')+'">'+fmtDate(a.last_seen)+'</td>'+
  '<td>'+scanStateBadge(a)+'</td>'+
  '<td class="flex" style="gap:4px">'+
  (S.me&&(S.me.role==='admin'||S.me.role==='editor')?
    '<button class="btn btn-sm btn-ghost" onclick="renameAgent(\''+h(a.id)+'\',\''+h(a.label||'')+'\')">Rename</button>':'')+
  (S.me&&S.me.role==='admin'?
    '<button class="btn btn-sm btn-ghost" onclick="openAssignGroup(\''+h(a.id)+'\',\''+h(a.group_id||'')+'\')">Group</button>'+
    '<button class="btn btn-sm btn-ghost" onclick="triggerScan(\''+h(a.id)+'\',\''+h(a.hostname)+'\')">Scan</button>'+
    '<button class="btn btn-sm btn-danger" onclick="removeAgent(\''+h(a.id)+'\',\''+h(a.hostname)+'\')">Remove</button>':'')+
  '</td></tr>';
```

Replace the empty-state row's `colspan` (`dashboard/static/index.html:1203`):

```js
}).join(''):'<tr><td colspan="8" class="empty">No agents enrolled yet.</td></tr>')+
```

with:

```js
}).join(''):'<tr><td colspan="9" class="empty">No agents enrolled yet.</td></tr>')+
```

- [ ] **Step 4: Add `scanStateBadge()`, `triggerScan()`, and `renderEcosystemScan()`**

Insert after `deleteAgentEvents` (`dashboard/static/index.html:1401`, right before the
`/* ---- GROUPS ---- */` comment):

```js
function scanStateBadge(a){
  var state=a.scan_state||'idle';
  if(state==='dispatched'&&a.scan_dispatched_at){
    var age=Date.now()-new Date(a.scan_dispatched_at).getTime();
    if(age>20*60*1000)state='idle'; // stale dispatch — admin can just retrigger
  }
  var m={idle:['—','gray'],pending:['Pending','yellow'],dispatched:['Dispatched','yellow'],running:['Running','blue'],completed:['Completed','green']};
  var v=m[state]||['—','gray'];
  return bdg(v[0],v[1]);
}

async function triggerScan(id,hostname){
  if(!confirm('Scan all installed packages on "'+hostname+'" for malware? This runs on the agent\'s next check-in (within ~15 min).'))return;
  try{
    var r=await fetch('/api/agents/'+id+'/scan',{method:'POST'});
    if(!r.ok){var d=await r.json().catch(function(){return{};});showToast(d.error||'Error','error');return;}
    showToast('Scan requested','success');
    await renderAgents();
  }catch(e){showToast('Error: '+e.message,'error');}
}

async function renderEcosystemScan(){
  var summary={},findings=[];
  try{summary=await api('/api/ecosystem/summary');}catch(e){}
  try{findings=await api('/api/ecosystem/findings');}catch(e){}
  findings=findings||[];
  document.getElementById('content').innerHTML=
  '<div class="sec-title">Ecosystem Scan <small>malware found in already-installed packages</small></div>'+
  '<div class="kpi-grid" style="margin-bottom:16px">'+
  '<div class="kpi"><div class="kpi-lbl">Agents Scanned</div><div class="kpi-val">'+fmt(summary.agents_scanned||0)+'</div></div>'+
  '<div class="kpi'+((summary.total_findings||0)>0?' danger':'')+'"><div class="kpi-lbl">Total Findings</div><div class="kpi-val">'+fmt(summary.total_findings||0)+'</div></div>'+
  '<div class="kpi"><div class="kpi-lbl">Last Scan</div><div class="kpi-val" style="font-size:16px">'+(summary.last_scan_at?fmtDate(summary.last_scan_at):'—')+'</div></div>'+
  '</div>'+
  '<div class="tbl-wrap"><table><thead><tr><th>Agent</th><th>OS</th><th>Ecosystem</th><th>Package</th><th>Version</th><th>Verdict</th><th>Paths</th><th>Detected</th><th>Remove</th></tr></thead><tbody>'+
  (findings.length?findings.map(function(f){
    var paths=f.paths||[];
    return'<tr>'+
    '<td><strong>'+h(f.hostname)+'</strong></td>'+
    '<td style="font-size:12px;color:var(--tx3)">'+h(f.os||'')+'</td>'+
    '<td>'+ecoBdg(f.ecosystem)+'</td>'+
    '<td class="mono">'+h(f.name)+'</td>'+
    '<td class="mono">'+h(f.version)+'</td>'+
    '<td style="font-size:12px">'+h(f.verdict||'')+(f.reference_url?' <a href="'+h(f.reference_url)+'" target="_blank" rel="noopener">(ref)</a>':'')+'</td>'+
    '<td style="font-size:11px;color:var(--tx4)">'+paths.length+' path'+(paths.length===1?'':'s')+
      (paths.length?' <button class="btn btn-sm btn-ghost" onclick="alert('+JSON.stringify(paths.join('\n'))+')">view</button>':'')+'</td>'+
    '<td style="font-size:12px;color:var(--tx3)">'+fmtDate(f.detected_at)+'</td>'+
    '<td class="mono" style="font-size:11px">'+h(f.remove_hint||'')+
      (f.remove_hint?' <button class="btn btn-sm btn-ghost" onclick="navigator.clipboard.writeText('+JSON.stringify(f.remove_hint)+')">copy</button>':'')+'</td>'+
    '</tr>';
  }).join(''):'<tr><td colspan="9" class="empty">No malware findings yet. Trigger a scan from the Agents tab.</td></tr>')+
  '</tbody></table></div>';
}

```

- [ ] **Step 5: Build and smoke-test locally**

Run: `go build ./...`
Expected: exits 0 (verifies `//go:embed` picked up the modified `static/index.html` without syntax
errors breaking the Go build — note this does NOT validate the JavaScript itself, only that the
file still embeds).

Then run the server locally (see this repo's own run/dev instructions — e.g. `go run . serve` or
equivalent) and manually verify in a browser, logged in as an admin user:
1. An "Ecosystem" nav item appears (admin-only — log in as a non-admin user and confirm it's
   hidden).
2. Clicking it loads the Ecosystem tab with three summary cards and an empty findings table
   ("No malware findings yet...").
3. On the Agents tab, an enrolled agent shows a "Scan" column with a "—" badge and a "Scan" button
   (admin only).
4. Clicking "Scan" shows a confirm dialog, then a "Scan requested" toast, and the badge changes to
   "Pending".
5. Manually flip an agent's `scan_state`/insert a fake finding via a temporary local script or by
   POSTing to `/api/scan-report` with `curl` (using a real API key from an enrolled test agent) to
   confirm the findings table renders a row correctly, including the "view" paths button and
   "copy" remove-hint button.

- [ ] **Step 6: Commit**

```bash
git add dashboard/static/index.html
git commit -m "feat: add Ecosystem dashboard tab with per-agent scan trigger"
```

---

## Self-Review Notes

**Spec coverage:**
- `Agent.ScanRequested`/`ScanState`/`LastScanAt`/`Findings` fields → Task 1.
- Admin-only `POST /api/agents/{id}/scan` → Task 2.
- Agent-authenticated `POST /api/scan-report` (started/completed) → Task 2.
- Heartbeat `scan_requested` field, fire-once semantics → Task 1 (`ConsumeScanRequest`) + Task 2
  (wiring).
- `GET /api/ecosystem/findings` and `GET /api/ecosystem/summary`, admin-only → Task 2.
- "Ecosystem" dashboard tab, per-agent "Scan Now" button and status → Task 3.
- Admin-only visibility (dashboard tab findings expose real paths) → Task 3's `admin-only` CSS
  class on the nav item, plus the Go handlers' `RoleAdmin` checks in Task 2.
- Dispatched-timeout-treated-as-idle (>20 min) → Task 3's `scanStateBadge()` client-side logic,
  matching the existing project convention of computing derived status (`epStatus()`) client-side
  rather than server-side.

**Critical fix caught during research, not assumption:** `sessionMiddleware`'s `unauthAPI` map
(`dashboard/handler.go:2109-2114`) would silently 401 every real agent call to `/api/scan-report`
in any deployment that has `Users`/`Sessions` configured (i.e. every production deployment with
login enabled) if not added to that map — this is easy to miss since it's a separate middleware
layer wrapping the whole mux, not something visible from the handler's own code. Task 2 Step 3
addresses this explicitly, first, before anything else in that task.

**Not covered by this plan (belongs to Plan 1 — pmg repo):** the `internal/ecoscan` package, the
`cmd/cloud` heartbeat trigger and `postScanReport` HTTP client, and all disk-walking/malware
analysis logic. This plan only implements the pmg-cloud side that Plan 1's agent code talks to.

**Type consistency check:** `EcosystemFinding`, `EcosystemScanSummary` (Task 1) are the exact JSON
shapes Task 2's `/api/scan-report` handler decodes requests into and Task 3's dashboard renders —
field names (`ecosystem`, `name`, `version`, `verdict`, `reference_url`, `paths`, `remove_hint` /
`total_paths_scanned`, `unique_packages`, `flagged_count`, `skipped_dirs`, `skipped_cloud_checks`,
`duration_seconds`) match verbatim what Plan 1's `cmd/cloud/ecoscan_report.go` (`scanFindingPayload`/
`scanSummaryPayload`) sends — verified against the sibling plan's Task 11.
