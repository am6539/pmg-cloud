# Unified Endpoints Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the separate Agents and Endpoints tabs with one role-aware Endpoints inventory containing managed and observed devices, activity, scan state, filters, detail actions, and responsive behavior.

**Architecture:** Extend the existing `EndpointInfo` merge model so it carries enrollment identity and scan data while activity remains derived from the selected event period. Make `/api/endpoints` the read API for every dashboard role, filter agent and event inputs consistently, and keep enrollment mutations on `/api/agents/{agent_id}` while telemetry deletion uses `/api/endpoints/{endpoint_id}/events`. Replace the two vanilla-JS render paths with one inventory and its existing drawer; no framework or new dependency is introduced.

**Tech Stack:** Go 1.25, `net/http`, JSONL event storage, embedded vanilla HTML/CSS/JavaScript, `testify`, Node syntax checking, Playwright smoke testing.

**Spec:** `docs/superpowers/specs/2026-09-28-unified-endpoints-design.md`

## Global Constraints

- Keep `agent_id` and `endpoint_id` separate in every backend and frontend operation.
- Managed means `agent_id` is non-empty; Observed means `agent_id` is empty.
- Status thresholds stay Online `<30m`, Away `<12h`, Offline otherwise.
- Activity range changes sessions/packages/blocked and drawer events, not managed endpoint inventory membership or heartbeat status.
- Group filtering applies to both enrolled agents and event activity.
- Viewer is read-only; Editor may rename managed endpoints only; Admin may perform all applicable actions.
- Do not add dependencies, a frontend build system, server-side pagination, or a new `/api/devices` namespace.
- Keep legacy `#agents` bookmarks working by normalizing them to `#endpoints`.
- Preserve current behavior that removing a managed agent also deletes telemetry; observed telemetry deletion is a separate endpoint route.
- Do not commit or push unless the user separately requests it.

---

### Task 1: Expand the merged endpoint model

**Files:**
- Modify: `dashboard/reader.go:305-448`
- Modify: `dashboard/reader_test.go`

**Interfaces:**
- Consumes: `Agent`, `Event`, `EcosystemScanSummary`.
- Produces: `MergeAgentEndpoints(agents []Agent, events []Event) []EndpointInfo`; expanded `EndpointInfo` JSON fields `managed`, `machine_id`, `scan_state`, `scan_dispatched_at`, `last_scan_at`, `last_scan_summary`.

- [ ] **Step 1: Add failing merge tests**

Append tests that prove hostname fallback preserves different IDs and that enrollment scan state is copied:

```go
func TestMergeAgentEndpoints_HostnameMatchPreservesSeparateIDs(t *testing.T) {
	now := time.Now().UTC()
	agents := []Agent{{ID: "agent-1", Hostname: "build-host", ScanState: "completed", LastScanAt: &now}}
	events := []Event{{EndpointID: "build-host", MachineID: "machine-1", InvocationID: "run-1", ReceivedAt: now}}

	got := MergeAgentEndpoints(agents, events)
	require.Len(t, got, 1)
	assert.Equal(t, "agent-1", got[0].AgentID)
	assert.Equal(t, "build-host", got[0].EndpointID)
	assert.Equal(t, "machine-1", got[0].MachineID)
	assert.True(t, got[0].Managed)
	assert.Equal(t, "completed", got[0].ScanState)
	assert.Equal(t, &now, got[0].LastScanAt)
}

func TestMergeAgentEndpoints_ObservedEndpointIsNotManaged(t *testing.T) {
	now := time.Now().UTC()
	got := MergeAgentEndpoints(nil, []Event{{EndpointID: "ci-runner", MachineID: "machine-ci", ReceivedAt: now}})
	require.Len(t, got, 1)
	assert.Empty(t, got[0].AgentID)
	assert.Equal(t, "ci-runner", got[0].EndpointID)
	assert.False(t, got[0].Managed)
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run:

```bash
go test ./dashboard -run 'TestMergeAgentEndpoints_(HostnameMatchPreservesSeparateIDs|ObservedEndpointIsNotManaged)' -count=1
```

Expected: compile failure because the new fields do not exist.

- [ ] **Step 3: Expand `EndpointInfo` and merge aggregation**

Add these fields:

```go
Managed            bool                  `json:"managed"`
MachineID          string                `json:"machine_id,omitempty"`
ScanState          string                `json:"scan_state,omitempty"`
ScanDispatchedAt   *time.Time            `json:"scan_dispatched_at,omitempty"`
LastScanAt         *time.Time            `json:"last_scan_at,omitempty"`
LastScanSummary    *EcosystemScanSummary `json:"last_scan_summary,omitempty"`
```

Track the latest non-empty event `MachineID` in the per-endpoint accumulator. When an agent matches, set `Managed: true` and copy scan fields. For zero-event agents, set `Managed: true`. Observed event rows retain `Managed: false`.

- [ ] **Step 4: Run merge tests**

```bash
go test ./dashboard -run 'TestMergeAgentEndpoints' -count=1
```

Expected: PASS.

---

### Task 2: Make endpoint reads role-aware and query-correct

**Files:**
- Modify: `dashboard/handler.go:216-315,439-524`
- Modify: `dashboard/handler_test.go`

**Interfaces:**
- Consumes: `MergeAgentEndpoints`, `parseDays`, `parseDateRange`, `filterByGroup`, `EnrollmentStore.ListAllAgents()`.
- Produces: `loadEventsForQuery(r *http.Request, reader *Reader) ([]Event, error)` and `filterAgentsByGroup(agents []Agent, groupID string) []Agent`; unified GET semantics for `/api/endpoints` and `/api/endpoints/{endpoint_id}/events`.

- [ ] **Step 1: Add failing query and RBAC tests**

Add a role-aware handler helper that creates admin/editor/viewer sessions and tests:

```go
func TestHandler_Endpoints_AllDashboardRolesCanRead(t *testing.T) {
	// Register one managed agent, then issue GET /api/endpoints with
	// admin, editor, and viewer session cookies. Each must return 200
	// and JSON containing agent_id and endpoint_id.
}

func TestHandler_Endpoints_GroupFilterExcludesOtherZeroEventAgents(t *testing.T) {
	// Register zero-event agents in g1 and g2.
	// GET /api/endpoints?group_id=g1 must return only the g1 agent.
}

func TestHandler_Endpoints_PeriodLimitsActivityButKeepsManagedInventory(t *testing.T) {
	// Seed an active enrolled agent and an event file older than 7 days.
	// GET /api/endpoints?days=7 must keep the managed row with zero
	// period activity and preserve LastSeen from the enrollment heartbeat.
}

func TestHandler_EndpointEvents_UsesRequestedRange(t *testing.T) {
	// Seed recent and old events for one endpoint.
	// GET /api/endpoints/ep-1/events?days=7 returns only the recent event.
}
```

Use the existing `doWithSession`, `writeEventsFile`, `NewUserStore`, and `NewSessionStore` helpers. Decode response bodies into `[]EndpointInfo` or `[]Event` and assert exact row/event IDs.

- [ ] **Step 2: Run focused tests and verify failures**

```bash
go test ./dashboard -run 'TestHandler_Endpoints_|TestHandler_EndpointEvents_UsesRequestedRange' -count=1
```

Expected: editor endpoint read returns 403; group/period assertions fail because endpoints load all time and only events are group-filtered.

- [ ] **Step 3: Extract the shared event-query loader**

Implement:

```go
func loadEventsForQuery(r *http.Request, reader *Reader) ([]Event, error) {
	q := r.URL.Query()
	if q.Get("from") != "" || q.Get("to") != "" {
		from, to, err := parseDateRange(q.Get("from"), q.Get("to"))
		if err != nil {
			return nil, err
		}
		return reader.LoadEventsRange(from, to)
	}
	return reader.LoadEvents(parseDays(r, 30))
}

func filterAgentsByGroup(agents []Agent, groupID string) []Agent {
	if groupID == "" {
		return agents
	}
	out := make([]Agent, 0, len(agents))
	for _, agent := range agents {
		if agent.GroupID == groupID {
			out = append(out, agent)
		}
	}
	return out
}
```

Return HTTP 400 for an invalid custom range. Reuse the loader in endpoint inventory and endpoint detail event reads.

- [ ] **Step 4: Update endpoint list authorization and filtering**

Require GET, and when session auth is configured require any valid dashboard role rather than rejecting Editor. Load range-scoped events, filter events by `group_id`, independently filter agents by the same `group_id`, merge them, and preserve CSV behavior:

```go
agents := []Agent(nil)
if deps.Enrollment != nil {
	agents = filterAgentsByGroup(deps.Enrollment.ListAllAgents(), q.Get("group_id"))
}
list := MergeAgentEndpoints(agents, filterByGroup(events, q.Get("group_id")))
```

- [ ] **Step 5: Update per-endpoint event reads**

Use `loadEventsForQuery` and then filter by the path `endpoint_id`. Retain newest-first sorting and the 200-event cap.

- [ ] **Step 6: Run focused and package tests**

```bash
go test ./dashboard -run 'TestHandler_Endpoints_|TestHandler_EndpointEvents_UsesRequestedRange' -count=1
go test ./dashboard -count=1
```

Expected: PASS.

---

### Task 3: Separate managed-agent removal from observed telemetry deletion

**Files:**
- Modify: `dashboard/handler.go:477-503,1889-1922`
- Modify: `dashboard/handler_test.go`

**Interfaces:**
- Consumes: `EnrollmentStore.GetAgentByID`, `EnrollmentStore.RemoveAgent`, `Reader.DeleteEventsByEndpointID`.
- Produces: strict admin-only `DELETE /api/endpoints/{endpoint_id}/events` for observed telemetry and strict `DELETE /api/agents/{agent_id}` for enrolled agents.

- [ ] **Step 1: Add failing destructive-operation tests**

```go
func TestHandler_DeleteObservedEndpointTelemetry_AdminSucceeds(t *testing.T) {
	// Seed an event for endpoint "ci-runner" without enrollment.
	// DELETE /api/endpoints/ci-runner/events as admin => 200.
	// A subsequent GET returns no event for ci-runner.
}

func TestHandler_DeleteEndpointTelemetry_ActiveManagedRejected(t *testing.T) {
	// Register active agent-1.
	// DELETE /api/endpoints/agent-1/events as admin => 400.
}

func TestHandler_DeleteObservedEndpointTelemetry_EditorForbidden(t *testing.T) {
	// DELETE observed endpoint telemetry as editor => 403.
}

func TestHandler_DeleteUnknownAgentDoesNotDeleteObservedTelemetry(t *testing.T) {
	// Seed observed endpoint ci-runner.
	// DELETE /api/agents/ci-runner as admin => 404.
	// Its event must remain.
}
```

- [ ] **Step 2: Run tests and verify current failures**

```bash
go test ./dashboard -run 'TestHandler_Delete(Observed|Endpoint|Unknown)' -count=1
```

Expected: observed endpoint route returns 404 and `/api/agents/ci-runner` currently deletes telemetry.

- [ ] **Step 3: Correct telemetry deletion semantics**

For `DELETE /api/endpoints/{endpoint_id}/events`:

1. Require admin.
2. If `endpoint_id` directly identifies an active agent, return 400.
3. Also reject when an active agent hostname equals `endpoint_id`, because merge may have associated the observed ID with that managed agent.
4. Confirm at least one matching event exists; otherwise return 404.
5. Delete by exact `endpoint_id`, audit `endpoint_telemetry_deleted`, and return `{"status":"ok"}`.

- [ ] **Step 4: Restrict `/api/agents/{id}` delete to real agents**

If `GetAgentByID(agentID)` fails, return 404. If present, preserve the current remove-enrollment-plus-delete-events behavior and audit event. Remove the orphan fallback from this route.

- [ ] **Step 5: Run focused and dashboard tests**

```bash
go test ./dashboard -run 'TestHandler_Delete(Observed|Endpoint|Unknown)' -count=1
go test ./dashboard -count=1
```

Expected: PASS.

---

### Task 4: Replace Agents navigation with the unified Endpoints inventory

**Files:**
- Modify: `dashboard/static/index.html:19-179,183-201,286-307,368-387,501-578,720-865,1218-1500`
- Create: `dashboard/static_test.go`

**Interfaces:**
- Consumes: `GET /api/endpoints`, `S.groups`, `S.me`, `epStatus`, `scanStateBadge`, existing modal/drawer helpers.
- Produces: `renderEndpoints()`, `filteredEndpoints()`, `endpointType(ep)`, `endpointActions(ep)`, endpoint filter fields on `S`, legacy hash normalization.

- [ ] **Step 1: Add a failing embedded-static contract test**

Create `dashboard/static_test.go`:

```go
func TestUnifiedEndpointsStaticContract(t *testing.T) {
	data, err := staticFiles.ReadFile("static/index.html")
	require.NoError(t, err)
	html := string(data)
	assert.NotContains(t, html, `nav('agents')`)
	assert.Contains(t, html, `if(hashPage==='agents')hashPage='endpoints'`)
	assert.Contains(t, html, `function renderEndpoints()`)
	assert.Contains(t, html, `Managed`)
	assert.Contains(t, html, `Observed`)
	assert.Contains(t, html, `/api/endpoints/`)
}
```

- [ ] **Step 2: Run the static contract test and verify failure**

```bash
go test ./dashboard -run TestUnifiedEndpointsStaticContract -count=1
```

Expected: FAIL because Agents navigation and render path still exist.

- [ ] **Step 3: Consolidate navigation and state**

- Move the one Endpoints nav item under Management and remove Agents.
- Remove `agents` from `PAGE_TITLES` and the `renderAgents()` dispatch.
- Normalize the initial hash before lookup:

```js
var hashPage=location.hash.replace('#','');
if(hashPage==='agents')hashPage='endpoints';
```

- Change the Editor redirect to `S.page='endpoints'` and keep Endpoints visible to all roles.
- Add endpoint state:

```js
epSearch:'',epStatusFilter:'',epTypeFilter:'',epGroupFilter:'',epOsFilter:'',epVersionFilter:'',epKpiFilter:''
```

- [ ] **Step 4: Implement inventory filtering helpers**

Implement `endpointType(ep)` as `ep.managed || ep.agent_id ? 'Managed' : 'Observed'`. Implement AND-filtering over search, status, type, group, OS, version, and KPI selection. Search includes label, hostname, endpoint ID, agent ID, local IP, and remote IP.

Determine the latest known version from the endpoint list. Unknown versions are not outdated. Compute KPI counts from the full API result, not the filtered list.

- [ ] **Step 5: Rebuild `renderEndpoints()`**

Render:

1. Description and role-aware actions: CSV for all readers; Enrollment Tokens, Scan All, Deploy Agent for admin.
2. Clickable KPI cards: Total, Online, Away/Offline, Outdated, Blocked.
3. Search and Status/Type/Group/OS/Version controls plus Clear.
4. Eight grouped columns: Status, Device, Type, Platform, Group, Activity, Last Seen, Scan/Actions.
5. A filtered empty state distinct from an empty inventory.

Rows call `openEpDrawer(index)` using an index into the unfiltered `epList`, not an interpolated identifier. Contextual action buttons call `event.stopPropagation()` and use `ep.agent_id` for rename/group/scan/remove, never `ep.endpoint_id`.

- [ ] **Step 6: Remove the old Agents renderer but keep reusable workflows**

Delete `renderAgents()` and its inline enrollment-token table. Keep deploy, token create/revoke, rename/group/scan helpers, updating their completion refresh from `renderAgents()` to `renderEndpoints()`.

- [ ] **Step 7: Run static and syntax checks**

```bash
go test ./dashboard -run TestUnifiedEndpointsStaticContract -count=1
python3 - <<'PY'
from pathlib import Path
s=Path('dashboard/static/index.html').read_text()
js=s.split('<script>',1)[1].split('</script>',1)[0]
Path('/tmp/unified-endpoints.js').write_text(js)
PY
node --check /tmp/unified-endpoints.js
```

Expected: PASS and no Node output.

---

### Task 5: Complete the drawer, tokens modal, actions, and responsive layout

**Files:**
- Modify: `dashboard/static/index.html:45-179,783-868,1287-1500`
- Modify: `dashboard/static_test.go`

**Interfaces:**
- Consumes: `_drawerEp`, `_drawerEvents`, `buildQ`, agent mutation routes, enrollment-token routes.
- Produces: `endpointOverviewActions(ep)`, `openEnrollmentTokens()`, `deleteEndpointEvents(endpointID)`, responsive endpoint table/card styles.

- [ ] **Step 1: Extend the static contract test**

Add assertions for:

```go
assert.Contains(t, html, `function openEnrollmentTokens()`)
assert.Contains(t, html, `function deleteEndpointEvents(endpointId)`)
assert.Contains(t, html, `'/api/agents/'+encodeURIComponent(ep.agent_id)`)
assert.Contains(t, html, `'/api/endpoints/'+encodeURIComponent(endpointId)+'/events'`)
assert.Contains(t, html, `overflow-x:auto`)
```

Run and confirm failure.

- [ ] **Step 2: Make drawer event queries period-aware and failure-isolated**

Fetch:

```js
api('/api/endpoints/'+encodeURIComponent(ep.endpoint_id)+'/events?'+buildQ())
```

Keep Overview rendered immediately. Store a drawer-specific event error and show it only for Events/Packages instead of replacing the whole drawer body.

- [ ] **Step 3: Expand Overview and role/type actions**

Overview shows:

- Managed/Observed badge and group name;
- sessions/packages/blocked;
- hostname, label, local/public IP;
- OS/arch, PMG version;
- enrolled, first seen, last seen;
- endpoint ID, agent ID, machine ID;
- scan badge, last scan timestamp, flagged count when managed.

Actions:

```js
// editor/admin + managed
renameAgent(ep.agent_id, ep.label||'')
// admin + managed
openAssignGroup(ep.agent_id, ep.group_id||'')
triggerScan(ep.agent_id, ep.hostname||ep.agent_id)
removeAgent(ep.agent_id, ep.hostname||ep.agent_id)
// admin + observed
deleteEndpointEvents(ep.endpoint_id)
```

After successful mutations, close or refresh the drawer and call `renderEndpoints()`.

- [ ] **Step 4: Implement the Enrollment Tokens modal**

`openEnrollmentTokens()` fetches `/api/enrollment-tokens`, renders active tokens in the existing modal, and retains New Token/Revoke. After create/revoke/deploy completion, return to or refresh the unified endpoint page rather than invoking removed Agents code.

- [ ] **Step 5: Correct Scan All**

Use the already-loaded managed rows:

```js
var agents=epList.filter(function(ep){return ep.agent_id;});
```

POST scans by encoded `agent_id`. Never scan observed rows.

- [ ] **Step 6: Add responsive styles**

- Change `.tbl-wrap` to `overflow-x:auto`.
- Add stable endpoint-table classes rather than styling by fragile global column indexes.
- At tablet width, collapse Group and secondary activity detail.
- At mobile width, stack endpoint rows/cards, allow filters/header actions to wrap, reduce content padding, and set drawer width to nearly `100vw`.
- Preserve 16px minimum mobile horizontal gutter and no page-level horizontal scrolling.

- [ ] **Step 7: Run static, syntax, and Go verification**

```bash
go test ./dashboard -run TestUnifiedEndpointsStaticContract -count=1
python3 - <<'PY'
from pathlib import Path
s=Path('dashboard/static/index.html').read_text()
Path('/tmp/unified-endpoints.js').write_text(s.split('<script>',1)[1].split('</script>',1)[0])
PY
node --check /tmp/unified-endpoints.js
gofmt -w dashboard/reader.go dashboard/reader_test.go dashboard/handler.go dashboard/handler_test.go dashboard/static_test.go
go test ./dashboard -count=1
go test ./... -count=1
go build ./...
```

Expected: all commands PASS.

---

### Task 6: Browser acceptance testing

**Files:**
- No source changes expected; fix source and repeat checks if acceptance reveals a defect.

**Interfaces:**
- Consumes: the completed unified page and API.
- Produces: acceptance evidence for Admin, Editor, Viewer, and responsive layouts.

- [ ] **Step 1: Start the dashboard with isolated data**

Run the project using the repository's supported CLI flags on an unused local port and a temporary data directory. Create Admin, Editor, and Viewer sessions/users using existing APIs or test setup conventions.

- [ ] **Step 2: Seed representative inventory**

Create:

- one online managed endpoint with scan results;
- one offline zero-event managed endpoint in another group;
- one observed endpoint with recent package events and blocked activity;
- at least two known PMG versions plus one unknown version.

- [ ] **Step 3: Verify Admin behavior with Playwright**

Confirm:

1. Sidebar has Endpoints and no Agents.
2. `#agents` normalizes to Endpoints.
3. KPIs and every filter produce the expected rows.
4. Managed and Observed badges are correct.
5. Managed drawer has Rename/Group/Scan/Remove and no Delete Telemetry.
6. Observed drawer has Delete Telemetry and no enrollment actions.
7. Events/Packages respect the selected period.
8. Enrollment Tokens opens a modal.
9. CSV downloads from the current group/time query.

- [ ] **Step 4: Verify Editor and Viewer RBAC**

- Editor can load Endpoints and rename a managed endpoint; no admin actions appear.
- Viewer can load Endpoints and drawer details; no mutation actions appear.
- Direct unauthorized mutation requests remain rejected by the backend.

- [ ] **Step 5: Verify responsive layouts**

Test at approximately 1440px, 900px, and 390px viewport widths. Confirm no clipped actions, no page-level horizontal overflow, readable endpoint rows, wrapping filters, and an accessible near-full-width mobile drawer.

- [ ] **Step 6: Final regression check**

```bash
git diff --check
go test ./... -count=1
go build ./...
```

Expected: no whitespace errors and all tests/build pass. Report any unrelated pre-existing failure separately rather than claiming completion.
