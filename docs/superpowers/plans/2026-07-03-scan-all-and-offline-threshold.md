# Scan All + Offline Threshold Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Scan All" button to the Agents tab that triggers an ecosystem scan on every listed
agent in one click, and raise the dashboard's offline-status threshold from 2 hours to 12 hours.

**Architecture:** Both changes are confined to `dashboard/static/index.html` — no Go code, no new
endpoints. "Scan All" is a client-side loop that calls the existing `POST /api/agents/{id}/scan`
endpoint once per agent via `Promise.allSettled`. The offline threshold is a single constant change
plus a matching hardcoded banner-text update.

**Tech Stack:** Plain JavaScript (no framework), served via Go's `embed.FS` — same house style as
the rest of `dashboard/static/index.html`.

## Global Constraints

- No new Go code, no new backend endpoints — reuse the existing `POST /api/agents/{id}/scan`
  (admin-only, already shipped).
- "Scan All" applies to every agent currently returned by `GET /api/agents` (already filtered to
  non-removed agents by the existing endpoint), including ones shown as Offline — the
  `ScanRequested` flag persists until that agent's next heartbeat poll, whenever that happens.
- No group-scoped filtering — "Scan All" scans exactly the list the Agents tab shows today
  (unfiltered by group).
- The `H2` identifier name stays as-is even though its value stops meaning "2 hours" — renaming it
  everywhere it's used is out of scope for this change (see spec's Non-Goals).
- This file has no automated test harness. Verification is `go build ./...` (confirms the file
  still embeds without a syntax error) plus a manual browser-check checklist per task.

---

### Task 1: "Scan All" button

**Files:**
- Modify: `dashboard/static/index.html:1179` (insert new button in `renderAgents()`'s admin-only header)
- Modify: `dashboard/static/index.html:1418` (insert new `triggerScanAll()` function immediately after the existing `triggerScan()`)

**Interfaces:**
- Consumes: existing `api()` helper (`dashboard/static/index.html:469`, GET-only, throws on non-OK),
  existing `showToast(msg, type)`, existing `renderAgents()`.
- Produces: `triggerScanAll()` — a global function invoked from the new button's `onclick`. No other
  task depends on this function.

- [ ] **Step 1: Add the "Scan All" button next to "Deploy New Agent"**

Replace (`dashboard/static/index.html:1176-1180`):

```js
(S.me&&S.me.role==='admin'?
'<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">'+
'<div class="sec-title" style="margin:0">Enrolled Agents <small>'+agents.length+' total</small></div>'+
'<button class="btn btn-primary" onclick="openDeployWizard()">&#43; Deploy New Agent</button></div>':
'<div class="sec-title">Enrolled Agents <small>'+agents.length+' total</small></div>')+
```

with:

```js
(S.me&&S.me.role==='admin'?
'<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">'+
'<div class="sec-title" style="margin:0">Enrolled Agents <small>'+agents.length+' total</small></div>'+
'<div class="flex" style="gap:8px">'+
'<button class="btn btn-ghost" onclick="triggerScanAll()">Scan All ('+agents.length+')</button>'+
'<button class="btn btn-primary" onclick="openDeployWizard()">&#43; Deploy New Agent</button>'+
'</div></div>':
'<div class="sec-title">Enrolled Agents <small>'+agents.length+' total</small></div>')+
```

- [ ] **Step 2: Add the `triggerScanAll()` function**

Insert immediately after the existing `triggerScan()` function (`dashboard/static/index.html:1418-1426`):

```js
async function triggerScan(id,hostname){
  if(!confirm('Scan all installed packages on "'+hostname+'" for malware? This runs on the agent\'s next check-in (within ~15 min).'))return;
  try{
    var r=await fetch('/api/agents/'+id+'/scan',{method:'POST'});
    if(!r.ok){var d=await r.json().catch(function(){return{};});showToast(d.error||'Error','error');return;}
    showToast('Scan requested','success');
    await renderAgents();
  }catch(e){showToast('Error: '+e.message,'error');}
}

async function triggerScanAll(){
  var agents=[];
  try{agents=(await api('/api/agents')).filter(function(a){return a&&!a.removed;});}
  catch(e){showToast('Error: '+e.message,'error');return;}

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

Note: `triggerScanAll()` re-fetches `/api/agents` itself rather than reusing `renderAgents()`'s
`agents` variable, so the list is current at click-time, not stale from the last render. The
`try/catch` around the initial fetch handles a transport failure or expired session (the `api()`
helper's existing 401 handling still applies since `triggerScanAll` calls `api()`, not raw `fetch`,
for that first read).

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: exits 0 with no output (confirms `//go:embed` still picks up the file without a JS/HTML
syntax problem breaking the Go build — this does not validate the JavaScript itself).

- [ ] **Step 4: Manual browser verification**

Run the server locally (this repo's own run/dev instructions) and, logged in as an admin:
1. Go to the Agents tab — a "Scan All (N)" button appears next to "Deploy New Agent", where N
   matches the "Enrolled Agents" total shown just above the table.
2. Click it — a confirm dialog appears reading "Scan all N agents for malware?..." with correct
   pluralization for N=1 vs N>1.
3. Confirm — a toast appears reading "Requested scan for N/N agents" (no failures in the normal
   case), and every agent row's Scan-status badge changes to "Pending" after the table re-renders.
4. Log out and log in as a non-admin (editor or viewer) — the "Scan All" button is absent, matching
   the existing admin-only "Deploy New Agent" and per-agent "Scan" button.
5. If there are zero enrolled agents (e.g. a fresh local dev database), click "Scan All" — a "No
   agents to scan" toast appears immediately, with no confirm dialog.

- [ ] **Step 5: Commit**

```bash
git add dashboard/static/index.html
git commit -m "feat: add Scan All button to trigger ecosystem scan on every agent"
```

---

### Task 2: Raise offline threshold from 2 hours to 12 hours

**Files:**
- Modify: `dashboard/static/index.html:666` (the `H2` constant)
- Modify: `dashboard/static/index.html:1172` (the hardcoded offline-banner text)

**Interfaces:**
- Consumes: none (pure constant/text change).
- Produces: nothing new — `H2` keeps its existing name and is still read by `epStatus()`,
  `epOnline()`, `renderEndpoints()`, and `renderAgents()`'s `missing` filter, all unchanged by this
  task (only the value changes, so no call site needs editing).

- [ ] **Step 1: Raise the `H2` constant**

Replace (`dashboard/static/index.html:666`):

```js
var M30=30*60*1000,H2=2*60*60*1000;
```

with:

```js
var M30=30*60*1000,H2=12*60*60*1000;
```

- [ ] **Step 2: Update the hardcoded offline-banner text**

Replace (`dashboard/static/index.html:1172`):

```js
    '&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 2 hours ago: '+
```

with:

```js
    '&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 12 hours ago: '+
```

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: exits 0 with no output.

- [ ] **Step 4: Manual browser verification**

1. Find or create a test agent record with `last_seen` set to roughly 3 hours ago (i.e. within the
   old 2-hour cutoff but well under the new 12-hour one) — confirm it now shows status "Away" (the
   30-minute-to-12-hour band), not "Offline", and is absent from the offline warning banner.
2. Find or create a test agent with `last_seen` set to more than 12 hours ago (or no `last_seen` at
   all) — confirm it still shows "Offline" and appears in the banner with the updated "> 12 hours
   ago" wording.
3. Confirm the banner's pluralization ("agent" vs "agents", "was"/wording elsewhere) still reads
   correctly with the new number.

- [ ] **Step 5: Commit**

```bash
git add dashboard/static/index.html
git commit -m "fix: raise agent offline threshold from 2 hours to 12 hours"
```

---

## Self-Review Notes

**Spec coverage:**
- "Scan All" button placement (Agents tab, next to Deploy New Agent) → Task 1 Step 1.
- Client-side loop over existing per-agent endpoint, no new backend → Task 1 Step 2.
- Applies to all listed agents including Offline ones → Task 1 Step 2 (`agents` is unfiltered by
  status, only filtered by `!a.removed` same as `renderAgents()` itself).
- Confirm-once + summary toast → Task 1 Step 2.
- Zero-agents edge case (no confirm dialog) → Task 1 Step 2 (`if(!agents.length)` short-circuits
  before the `confirm()` call) and Step 4.5.
- `H2` value change and banner text update → Task 2.
- `H2` identifier intentionally not renamed (spec Non-Goal) → explicitly not touched anywhere in
  Task 2.

**Placeholder scan:** no TBD/TODO; every step has literal code to write and an exact verification
command.

**Type consistency check:** `triggerScanAll()` is defined once (Task 1 Step 2) and referenced once,
from the button's `onclick` added in Task 1 Step 1 — same name in both places. No other task
references it.
