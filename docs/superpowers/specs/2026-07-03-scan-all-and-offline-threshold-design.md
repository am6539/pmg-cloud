# Scan All + Offline Threshold — Design Spec

Date: 2026-07-03

## Overview

The Ecosystem Scan feature (shipped 2026-07-02) lets an admin trigger a machine-wide malware scan
on one agent at a time via a "Scan" button in the Agents tab, backed by `POST
/api/agents/{id}/scan`. For a fleet of any real size, triggering scans one agent at a time is
tedious. This adds a "Scan All" action that triggers a scan on every agent currently listed, in one
click.

Separately, the Agents tab currently marks an agent "Offline" (and includes it in the "agents
offline" warning banner) after only 2 hours without a heartbeat. Given the heartbeat interval is
15 minutes, 2 hours is a low bar that produces noisy false-positive warnings for machines that are
simply powered off overnight or over a weekend. This raises that threshold to 12 hours.

## Goals

- Let an admin trigger an ecosystem scan on every agent shown in the Agents tab with one click.
- Reuse the existing per-agent `POST /api/agents/{id}/scan` endpoint and `RequestScan`/
  `ConsumeScanRequest` machinery — no new backend endpoint, no new Go code.
- Reduce false-positive "offline" warnings by raising the offline threshold from 2 hours to 12
  hours.

## Non-Goals

- No new bulk backend endpoint. "Scan All" is a client-side loop over the existing per-agent
  endpoint.
- No group-scoped "scan all agents in group X" in this iteration — the Agents tab currently shows
  all agents unfiltered by group, and "Scan All" scans exactly that list.
- No change to the "Away" threshold (30 minutes, `M30`) or to the Online/Away/Offline status model
  itself — only the numeric offline cutoff changes.
- No change to `ScanState`, `ConsumeScanRequest` fire-once semantics, or any other part of the
  already-shipped Ecosystem Scan backend.

## Architecture

```
[Admin clicks "Scan All (N)"]
      │ confirm() — "Scan all N agents?"
      ▼
[Browser] Promise.allSettled(agents.map(a => fetch(POST /api/agents/{a.id}/scan)))
      │  (existing endpoint, existing admin-only + RequestScan logic — unchanged)
      ▼
[Browser] one summary toast ("Requested scan for X/N agents (Y failed)") + re-render Agents table
```

No new client/server contract is introduced. "Scan All" is a thin loop around the existing
`triggerScan(id, hostname)` request shape, sent to every agent currently in the rendered table
(including ones shown as Offline — the `ScanRequested` flag is durable and is consumed on that
agent's next heartbeat poll, whenever that happens).

## Components

### `dashboard/static/index.html` (frontend only — no Go changes)

**New button**, next to the existing "Deploy New Agent" button in `renderAgents()`'s admin-only
header (currently `dashboard/static/index.html:1179`):

```js
'<button class="btn btn-ghost" onclick="triggerScanAll()">Scan All ('+agents.length+')</button>'
```

**New function `triggerScanAll()`**, added near the existing `triggerScan()`:

```js
async function triggerScanAll(){
  var agents=(await api('/api/agents')).filter(function(a){return a&&!a.removed;});
  if(!agents.length){showToast('No agents to scan','error');return;}
  if(!confirm('Scan all '+agents.length+' agent'+(agents.length>1?'s':'')+' for malware? Each runs on its next check-in (within ~15 min).'))return;

  var results=await Promise.allSettled(agents.map(function(a){
    return fetch('/api/agents/'+a.id+'/scan',{method:'POST'});
  }));
  var ok=results.filter(function(r){return r.status==='fulfilled'&&r.value.ok;}).length;
  var fail=results.length-ok;
  showToast('Requested scan for '+ok+'/'+results.length+' agent'+(results.length>1?'s':'')+(fail?' ('+fail+' failed)':''), fail?'error':'success');
  await renderAgents();
}
```

`triggerScanAll()` re-fetches `/api/agents` itself (rather than reusing the `agents` array already
in scope inside `renderAgents()`'s closure) so the request list is always current at the moment the
button is clicked, not stale from whenever the page last rendered.

**Offline threshold**, `dashboard/static/index.html:666`:

```js
var M30=30*60*1000,H2=2*60*60*1000;
```
→
```js
var M30=30*60*1000,H2=12*60*60*1000;
```

The variable name `H2` ("2 hours") becomes misleading once the value is 12 hours. Renaming it
everywhere it's referenced (`epStatus`, `epOnline`, `renderEndpoints`, `renderEpTab`, `renderAgents`)
is a larger, purely-cosmetic diff across files unrelated to this feature's actual behavior change —
out of scope here per the "don't propose unrelated refactoring" principle. Leave the identifier
named `H2`; only its value changes. (Worth a follow-up rename if this file gets touched again for a
real reason.)

**Offline banner text**, `dashboard/static/index.html:1172` (inside `renderAgents()`):

```js
'&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 2 hours ago: '+
```
→
```js
'&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 12 hours ago: '+
```

This text is hardcoded rather than derived from `H2`; it must be updated by hand alongside the
constant so the banner's wording stays truthful.

## Data Flow (step by step)

1. Admin clicks "Scan All (N)" in the Agents tab.
2. `triggerScanAll()` re-fetches `/api/agents`, filters out removed agents, confirms with the admin.
3. Fires `N` parallel `POST /api/agents/{id}/scan` requests via `Promise.allSettled` (never rejects
   the whole batch on one failure).
4. Each request hits the existing, unmodified handler: admin-role check, `GetAgentByID` existence
   check, `RequestScan(agentID)` → sets `ScanRequested=true`, `ScanState=pending`.
5. Once all requests settle, `triggerScanAll()` counts successes/failures and shows one toast.
6. `renderAgents()` re-runs, so every agent's "Scan" status badge reflects the new `pending` state
   on the next render.

## Error Handling

- A single agent's request failing (e.g. that specific agent was deleted between page load and
  click, giving a 404) does not block or roll back the others — `Promise.allSettled` guarantees
  every request completes independently, and the failure is only reflected in the summary toast's
  failure count.
- If `/api/agents` itself fails (network error, session expired), `api()`'s existing behavior
  applies unchanged: a 401 triggers the existing session-expired handling; other failures throw and
  are caught by `triggerScanAll`'s `await` — this should be wrapped in a try/catch so a transport
  failure shows a toast instead of an uncaught promise rejection (see plan for exact code).
- Zero agents (e.g. a brand-new deployment with nothing enrolled yet) short-circuits with a toast
  before showing the confirm dialog, avoiding a "Scan all 0 agents?" prompt.

## Testing

- This is a pure frontend change to `dashboard/static/index.html` (no Go code touched), matching
  the existing project convention that this file has no automated test harness — verification is
  `go build ./...` (confirms the modified file still embeds without breaking the build) plus manual
  browser verification:
  1. Log in as admin, go to Agents tab — "Scan All (N)" button appears next to "Deploy New Agent",
     N matches the visible agent count.
  2. Click it — confirm dialog appears with the correct count and wording.
  3. Confirm — a toast reports "Requested scan for N/N agents", and every agent's Scan-status badge
     changes to "Pending".
  4. Log in as a non-admin (editor/viewer) — "Scan All" button is absent (same as "Deploy New
     Agent" and the per-agent "Scan" button, all admin-only).
  5. With zero agents enrolled, click "Scan All" — a "No agents to scan" toast appears with no
     confirm dialog.
  6. Confirm the Agents tab's offline banner and status badges show `Offline` only after 12 hours,
     not 2, by checking an agent whose `last_seen` is set (via test data or waiting) between the
     old and new thresholds.
