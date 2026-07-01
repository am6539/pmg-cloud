# Agent Re-enroll Dedup — Design

## Problem

`POST /api/enroll` (`dashboard/handler.go:1475`) always creates a brand-new
`Agent` record with a new ID and a new API key, regardless of whether an
agent with the same hostname is already enrolled. Any time a physical
machine re-enrolls (PMG reinstalled, `~/.pmg/config.yml` deleted/reset, new
enrollment token used deliberately), the dashboard ends up with two
independent `Agent` rows for the same physical machine — one orphaned, one
active — with two separate API keys.

Observed in production (`vgpmg.ovp.vn`): the same Windows machine (hostname
`HT-PC`, local IP `169.254.27.36`) enrolled twice on 2026-07-01, four hours
apart, producing agent `ht-pcc99.108` (auto-label) and agent `HC-Hieu`
(manually relabeled after the second enroll). Both point at the same
physical PC.

## Goal

When an agent enrolls and an **active** (non-removed) agent already exists
with the same hostname *and* the same local IP, update that existing
record instead of creating a new one. When no such match exists, keep the
current create-new behavior unchanged.

## Non-goals

- No migration or bulk-merge of agents that are *already* duplicated in
  existing data (e.g. `ht-pcc99.108` / `HC-Hieu`). Admins clean those up
  manually via the existing "Remove" button.
- No admin-facing UI/API to manually merge two arbitrary agent rows.
- No change to hostname-based event merging in `dashboard/reader.go`
  (`MergeAgentEndpoints`) — that logic is for the Endpoints/Overview event
  aggregation view and is out of scope here.

## Match criteria

Match on **hostname (case-insensitive) AND local IP**, both exact string
equality, against agents where `Removed == false`.

Rationale: hostname alone is not a reliable identifier — two distinct
physical machines were observed sharing the hostname `HT-PC` (one on LAN IP
`192.168.99.99`, one on APIPA IP `169.254.27.36`). Requiring both fields to
match avoids merging unrelated machines that happen to share a hostname.

If the incoming `local_ip` is empty, skip matching entirely and fall back
to create-new — matching on hostname alone was explicitly rejected as
unsafe.

Removed agents are never matched, even if hostname+IP line up: an admin
who explicitly removed an agent should get a fresh record on re-enroll,
not a revived old one.

## Behavior on match

When `FindActiveAgentByHostnameAndIP` finds an existing agent:

1. Keep the existing `ID` and `EnrolledAt` (enrollment history is
   preserved; this is the same logical agent).
2. Update `OS`, `Arch`, `PMGVersion`, `RemoteIP`, `LocalIP`, `GroupID` from
   the new enrollment request/token (group can legitimately change if a
   different token/group was used to re-enroll).
3. Leave `Label` untouched — an admin-assigned friendly name (e.g.
   `HC-Hieu`) must survive re-enrollment.
4. Revoke the agent's previous API key
   (`groups.RevokeAPIKey(oldAgent.GroupID, oldAgent.APIKeyID)`) so the old
   key can no longer authenticate, then issue a new API key exactly as the
   create-new path does today, and store its ID as the agent's
   `APIKeyID`.
5. Audit log event `agent_reenrolled` (new event type, mirrors the
   existing `agent_enrolled` entry) with the same detail format
   (`ip=... os=... arch=...`).

When no match is found, behavior is unchanged: create a new `Agent` with a
new ID and new API key, audit-logged as `agent_enrolled`.

## Implementation sketch

### `dashboard/enrollment.go`

Add a lookup method alongside `GetAgentByID`:

```go
// FindActiveAgentByHostnameAndIP returns the first non-removed agent whose
// hostname (case-insensitive) and local IP both match, for re-enroll dedup.
// Returns false if localIP is empty, since hostname alone is not a
// sufficiently reliable identifier.
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
```

Add an update method alongside `AssignAgentGroup` / `SetAgentLabel`:

```go
// ReenrollAgent updates an existing agent's connection/enrollment metadata
// in place (used when re-enrollment matches an existing active agent).
// Label is intentionally left untouched.
func (es *EnrollmentStore) ReenrollAgent(id string, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string) error {
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

(`strings` needs to be added to the import block.)

### `dashboard/handler.go` (`/api/enroll`)

After token validation and group resolution (existing code up to and
including determining `groupID`), before creating the API key:

```go
existing, matched := enrollment.FindActiveAgentByHostnameAndIP(req.Hostname, req.LocalIP)
```

Branch:

- **`matched == true`:**
  - If `existing.APIKeyID != "" && existing.GroupID != ""`, revoke it:
    `deps.Groups.RevokeAPIKey(existing.GroupID, existing.APIKeyID)` (ignore
    "not found" errors — best-effort, key may already be gone).
  - Create the new API key exactly as today (`deps.Groups.CreateAPIKey`).
  - Call `enrollment.ReenrollAgent(existing.ID, req.OS, req.Arch,
    req.PMGVersion, ip, req.LocalIP, groupID, apiKeyID)`.
  - `agentID := existing.ID` (so the rest of the handler — the response
    payload, gRPC endpoint resolution, etc. — is unaffected downstream).
  - Audit log `agent_reenrolled` instead of `agent_enrolled`.
- **`matched == false`:** existing code path, unchanged.

The response returned to the enrolling client (endpoint address, TLS
mode, API key) is identical in shape either way — the client has no way
to tell whether it hit the create or update path, which is correct: it
just gets working credentials.

## Testing

- **`dashboard/enrollment_test.go`** (new or existing file):
  - `FindActiveAgentByHostnameAndIP` matches when hostname (any case) and
    local IP both match an active agent.
  - Returns false when local IP differs (same hostname).
  - Returns false when the only matching agent has `Removed == true`.
  - Returns false when queried `localIP` is empty.
- **Handler-level test for `/api/enroll`:**
  - Enroll once → one active agent, one API key.
  - Enroll again with the same hostname + local IP → still exactly one
    active agent (same ID), old API key no longer resolves
    (`ResolveKeyWithID` fails), new API key resolves, `Label` (if set
    beforehand) is unchanged.
  - Enroll with same hostname but different local IP → two separate
    active agents.

## Rollout

Pure logic change in `pmg-cloud`, no data migration, no client
(`pmg`/PMG agent) changes required. Existing duplicate agents in
production (`ht-pcc99.108`, orphaned `HT-PC` rows) are left as-is; the
fix only prevents new duplicates going forward.
