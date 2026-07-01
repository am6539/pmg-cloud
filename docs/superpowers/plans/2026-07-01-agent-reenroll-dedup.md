# Agent Re-enroll Dedup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a PMG agent re-enrolls with the same hostname and local IP as an existing active agent, update that agent's record in place (new API key, old one revoked) instead of creating a duplicate `Agent` row.

**Architecture:** Add a hostname+local-IP lookup and an in-place update method to `EnrollmentStore` (`dashboard/enrollment.go`). Wire them into the `/api/enroll` handler (`dashboard/handler.go`) so it branches between "update existing" and the current "create new" path. No client, migration, or data-model changes.

**Tech Stack:** Go, `testify` (`assert`/`require`) for tests, existing `EnrollmentStore`/`GroupStore`/`AuditLog` file-backed stores.

## Global Constraints

- Match criterion is **hostname (case-insensitive) AND local IP**, both exact match — never hostname alone (spec: two distinct physical machines were observed sharing hostname `HT-PC`).
- If the incoming `local_ip` is empty, never match — always fall back to create-new.
- Never match against agents where `Removed == true`.
- On match: keep the existing agent's `ID` and `EnrolledAt`; do **not** touch `Label`.
- On match: revoke the old API key before issuing the new one.
- No migration of already-duplicated production data, and no new merge UI/API — out of scope (spec: Non-goals).

---

## Task 1: Enrollment store — hostname+IP lookup and in-place update

**Files:**
- Modify: `dashboard/enrollment.go`
- Create: `dashboard/enrollment_test.go`

**Interfaces:**
- Produces: `func (es *EnrollmentStore) FindActiveAgentByHostnameAndIP(hostname, localIP string) (Agent, bool)`
- Produces: `func (es *EnrollmentStore) ReenrollAgent(id, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string) error`
- Consumes: existing `Agent` struct (`dashboard/enrollment.go:38-52`) and `EnrollmentStore.data.Agents` (unexported field, same package).

- [ ] **Step 1: Write the failing tests**

Create `dashboard/enrollment_test.go`:

```go
package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEnrollmentStore(t *testing.T) *EnrollmentStore {
	t.Helper()
	es, err := NewEnrollmentStore(t.TempDir())
	require.NoError(t, err)
	return es
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_MatchesCaseInsensitiveHostname(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	found, ok := es.FindActiveAgentByHostnameAndIP("ht-pc", "169.254.27.36")
	require.True(t, ok)
	assert.Equal(t, "agent-1", found.ID)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenIPDiffers(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "192.168.99.99")
	assert.False(t, ok)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenRemoved(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
		Removed:  true,
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "169.254.27.36")
	assert.False(t, ok)
}

func TestEnrollmentStore_FindActiveAgentByHostnameAndIP_NoMatchWhenLocalIPEmpty(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:       "agent-1",
		Hostname: "HT-PC",
		LocalIP:  "169.254.27.36",
	}))

	_, ok := es.FindActiveAgentByHostnameAndIP("HT-PC", "")
	assert.False(t, ok)
}

func TestEnrollmentStore_ReenrollAgent_UpdatesFieldsKeepsIDAndLabel(t *testing.T) {
	es := newTestEnrollmentStore(t)
	require.NoError(t, es.RegisterAgent(Agent{
		ID:         "agent-1",
		Hostname:   "HT-PC",
		LocalIP:    "169.254.27.36",
		OS:         "windows",
		Arch:       "amd64",
		PMGVersion: "0.18.9",
		GroupID:    "group-old",
		APIKeyID:   "key-old",
	}))
	require.NoError(t, es.SetAgentLabel("agent-1", "HC-Hieu"))

	err := es.ReenrollAgent("agent-1", "windows", "amd64", "0.18.10", "113.190.252.218", "169.254.27.36", "group-new", "key-new")
	require.NoError(t, err)

	updated, ok := es.GetAgentByID("agent-1")
	require.True(t, ok)
	assert.Equal(t, "agent-1", updated.ID)
	assert.Equal(t, "HC-Hieu", updated.Label, "label must survive re-enrollment")
	assert.Equal(t, "0.18.10", updated.PMGVersion)
	assert.Equal(t, "113.190.252.218", updated.RemoteIP)
	assert.Equal(t, "169.254.27.36", updated.LocalIP)
	assert.Equal(t, "group-new", updated.GroupID)
	assert.Equal(t, "key-new", updated.APIKeyID)
}

func TestEnrollmentStore_ReenrollAgent_ErrorsWhenNotFound(t *testing.T) {
	es := newTestEnrollmentStore(t)
	err := es.ReenrollAgent("does-not-exist", "windows", "amd64", "0.18.10", "1.2.3.4", "10.0.0.1", "group-new", "key-new")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./dashboard/... -run TestEnrollmentStore -v`
Expected: compile error — `FindActiveAgentByHostnameAndIP` and `ReenrollAgent` are undefined on `*EnrollmentStore`.

- [ ] **Step 3: Implement the two methods**

In `dashboard/enrollment.go`, add `"strings"` to the import block (currently `crypto/rand`, `crypto/sha256`, `encoding/hex`, `encoding/json`, `fmt`, `os`, `path/filepath`, `sync`, `time`):

```go
import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)
```

Add the two methods after `GetAgentByID` (`dashboard/enrollment.go:210-220`):

```go
// FindActiveAgentByHostnameAndIP returns the first non-removed agent whose
// hostname (case-insensitive) and local IP both match. Used to detect a
// re-enrolling machine so /api/enroll can update it instead of creating a
// duplicate. Hostname alone is not a reliable identifier — distinct
// physical machines can share a hostname — so an empty localIP always
// misses rather than risk merging unrelated agents.
func (es *EnrollmentStore) FindActiveAgentByHostnameAndIP(hostname, localIP string) (Agent, bool) {
	if localIP == "" {
		return Agent{}, false
	}
	es.mu.RLock()
	defer es.mu.RUnlock()
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		if strings.EqualFold(a.Hostname, hostname) && a.LocalIP == localIP {
			return a, true
		}
	}
	return Agent{}, false
}

// ReenrollAgent updates an existing agent's connection/enrollment metadata
// in place. ID, EnrolledAt, and Label are left untouched so re-enrollment
// history and any admin-assigned name survive.
func (es *EnrollmentStore) ReenrollAgent(id, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == id {
			es.data.Agents[i].OS = os
			es.data.Agents[i].Arch = arch
			es.data.Agents[i].PMGVersion = pmgVersion
			es.data.Agents[i].RemoteIP = remoteIP
			es.data.Agents[i].LocalIP = localIP
			es.data.Agents[i].GroupID = groupID
			es.data.Agents[i].APIKeyID = apiKeyID
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./dashboard/... -run TestEnrollmentStore -v`
Expected: all 6 tests `PASS`, `ok github.com/yourorg/pmg-cloud/dashboard`.

- [ ] **Step 5: Commit**

```bash
git add dashboard/enrollment.go dashboard/enrollment_test.go
git commit -m "feat: add hostname+IP agent lookup and in-place re-enroll update"
```

---

## Task 2: Wire dedup into `/api/enroll` handler

**Files:**
- Modify: `dashboard/handler.go:1544-1565` (agent creation block inside the `/api/enroll` handler)
- Modify: `dashboard/handler_test.go`

**Interfaces:**
- Consumes: `enrollment.FindActiveAgentByHostnameAndIP(hostname, localIP string) (Agent, bool)` and `enrollment.ReenrollAgent(id, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string) error` from Task 1.
- Consumes: `deps.Groups.RevokeAPIKey(groupID, keyID string) error` (`dashboard/groups.go:185`), `deps.Groups.CreateAPIKey(groupID, name string) (plaintext string, key APIKey, err error)` (`dashboard/groups.go:137`), `deps.Groups.ResolveKeyWithID(plaintext string) (groupID, keyID string, ok bool)` (`dashboard/groups.go:213`).
- Produces: no new exported symbols — behavioral change only. Response shape of `/api/enroll` (`api_key`, `endpoint`, `insecure`, `group_id`, `agent_id`) is unchanged.

- [ ] **Step 1: Write the failing tests**

The current import block in `dashboard/handler_test.go` is:

```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Add `"strings"` to it (used by `doEnroll` below):

```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Then add to `dashboard/handler_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./dashboard/... -run TestHandler_Enroll -v`
Expected: `TestHandler_Enroll_ReenrollSameHostnameAndIP_UpdatesExistingAgent` and `TestHandler_Enroll_DifferentLocalIP_CreatesSeparateAgents` FAIL — re-enroll currently creates a second agent row, so the "exactly 1 active agent" / "same agent_id" assertions fail. `TestHandler_Enroll_ReenrollPreservesAdminAssignedLabel` fails too (`updated.Label` is empty because a *new* agent got created, and the label was set on the old one).

- [ ] **Step 3: Implement the dedup branch in the handler**

In `dashboard/handler.go`, replace the block from the `groupID` resolution through `RegisterAgent` (currently lines ~1531-1565):

```go
			var plainKey string
			var apiKeyID string
			if deps.Groups != nil && groupID != "" {
				keyName := req.Hostname + " (enrolled)"
				pk, key, kErr := deps.Groups.CreateAPIKey(groupID, keyName)
				if kErr != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				plainKey = pk
				apiKeyID = key.ID
			}

			agentID := genID()
			agent := Agent{
				ID:         agentID,
				Hostname:   req.Hostname,
				OS:         req.OS,
				Arch:       req.Arch,
				PMGVersion: req.PMGVersion,
				RemoteIP:   ip,
				LocalIP:    req.LocalIP,
				GroupID:    groupID,
				APIKeyID:   apiKeyID,
				EnrolledAt: time.Now().UTC(),
			}
			if err := enrollment.RegisterAgent(agent); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			if deps.Audit != nil {
				deps.Audit.Log("agent_enrolled", req.Hostname,
					fmt.Sprintf("ip=%s os=%s arch=%s", ip, req.OS, req.Arch))
			}
```

with:

```go
			existing, isReenroll := enrollment.FindActiveAgentByHostnameAndIP(req.Hostname, req.LocalIP)

			var plainKey string
			var apiKeyID string
			if deps.Groups != nil && groupID != "" {
				if isReenroll && existing.APIKeyID != "" && existing.GroupID != "" {
					_ = deps.Groups.RevokeAPIKey(existing.GroupID, existing.APIKeyID) // best-effort; old key may already be gone
				}
				keyName := req.Hostname + " (enrolled)"
				pk, key, kErr := deps.Groups.CreateAPIKey(groupID, keyName)
				if kErr != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				plainKey = pk
				apiKeyID = key.ID
			}

			var agentID string
			if isReenroll {
				agentID = existing.ID
				if err := enrollment.ReenrollAgent(agentID, req.OS, req.Arch, req.PMGVersion, ip, req.LocalIP, groupID, apiKeyID); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			} else {
				agentID = genID()
				agent := Agent{
					ID:         agentID,
					Hostname:   req.Hostname,
					OS:         req.OS,
					Arch:       req.Arch,
					PMGVersion: req.PMGVersion,
					RemoteIP:   ip,
					LocalIP:    req.LocalIP,
					GroupID:    groupID,
					APIKeyID:   apiKeyID,
					EnrolledAt: time.Now().UTC(),
				}
				if err := enrollment.RegisterAgent(agent); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}

			if deps.Audit != nil {
				action := "agent_enrolled"
				if isReenroll {
					action = "agent_reenrolled"
				}
				deps.Audit.Log(action, req.Hostname,
					fmt.Sprintf("ip=%s os=%s arch=%s", ip, req.OS, req.Arch))
			}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./dashboard/... -run TestHandler_Enroll -v`
Expected: all 3 tests `PASS`.

- [ ] **Step 5: Run the full package test suite to check for regressions**

Run: `go test ./dashboard/... ./server/...`
Expected: `ok` for both packages, no failures (in particular, the pre-existing `agent_enrolled` audit/create-new path used by every other enrollment test must be unaffected).

- [ ] **Step 6: Commit**

```bash
git add dashboard/handler.go dashboard/handler_test.go
git commit -m "feat: update existing agent on re-enroll instead of creating a duplicate"
```

---

## Self-Review Notes

- **Spec coverage:** match criteria (hostname+IP, case-insensitive, empty-IP miss, Removed exclusion) → Task 1. Update-in-place preserving ID/EnrolledAt/Label → Task 1 (`ReenrollAgent`) + Task 2 test `TestHandler_Enroll_ReenrollPreservesAdminAssignedLabel`. API key revoke-then-reissue → Task 2. Audit event `agent_reenrolled` → Task 2. No migration/merge UI → not built, confirmed absent from both tasks.
- **Type consistency:** `ReenrollAgent` signature is identical between its Task 1 definition and its Task 2 call site (`id, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string`). `FindActiveAgentByHostnameAndIP(hostname, localIP string) (Agent, bool)` likewise matches at both definition and call sites.
- **No placeholders:** every step has complete, runnable code; no "TBD" or "add error handling" left unresolved.
