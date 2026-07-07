# Agents Tab Filters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a client-side filter bar (free-text search + Status/Group/OS dropdowns) to the Agents tab so operators can quickly narrow a large agent list.

**Architecture:** Single-file vanilla-JS change to `dashboard/static/index.html`. `renderAgents()` already fetches the full agent list via `GET /api/agents` with no pagination; add four filter fields to the global `S` state object, filter the in-memory array before building table rows, and render a `.filter-bar` UI matching the existing Packages-tab convention. No backend/API changes.

**Tech Stack:** Vanilla JS embedded in a static HTML file (no build step, no bundler, no JS test framework in this repo — see Global Constraints).

## Global Constraints

- No JS test framework exists in this repo (confirmed: no `package.json`, no `*.test.js`). Verification for this plan is (a) a `node --check` syntax gate on the extracted script block, and (b) manual/browser verification against a locally-run server with seeded data — there is no automated JS test to write.
- Match the existing Packages-tab filter convention exactly: `.filter-bar` CSS class, text input uses `oninput` (instant, no Clear-trip-to-server), dropdowns use `onchange`, a `btn btn-sm btn-ghost` "Clear" button resets all filter fields — see `dashboard/static/index.html:835-846` (`renderPackages()`).
- Filtering is client-side only. Do not add query params to `/api/agents` or change any Go backend file.
- The offline/"missing agents" alert banner (`dashboard/static/index.html:1169-1173`) must continue to reflect the **full, unfiltered** agent list — it's a fleet-health alert, not a view of the current table, and must not disappear just because an operator has an unrelated filter active (e.g. filtering to `OS=windows` must not hide a warning about offline Linux agents). Only the **table rows** are filtered. This is a deliberate refinement of the original design spec (`docs/superpowers/specs/2026-07-06-agents-tab-filters-design.md`), which is otherwise authoritative for scope.
- The "N total" header count and the "Scan All (N)" button must keep counting the **full, unfiltered** list — `triggerScanAll()` (`dashboard/static/index.html:1431-1446`) always re-fetches and scans every agent regardless of what's currently filtered/visible, so the displayed count must not imply otherwise.

---

### Task 1: Add filter bar and filtering logic to the Agents tab

**Files:**
- Modify: `dashboard/static/index.html:287` (global `S` state object)
- Modify: `dashboard/static/index.html:1163-1211` (`renderAgents()`)

**Interfaces:**
- Consumes: `epStatus(last_seen)` (existing, `dashboard/static/index.html:667-673`, returns `{label,dot,color}` with `label` one of `'Online'|'Away'|'Offline'`), `S.groups` (existing, array of `{id,name,key_count}`), `h()` (existing HTML-escape helper).
- Produces: four new fields on `S` — `agentSearch` (string), `agentStatus` (string, one of `''|'Online'|'Away'|'Offline'`), `agentGroup` (string, one of `''|'__unassigned__'|<group id>`), `agentOs` (string, one of `''|'windows'|'linux'|'macos'`). No other task depends on these.

- [ ] **Step 1: Add the four filter fields to the global `S` object**

In `dashboard/static/index.html`, line 287, change:

```js
var S={page:'dashboard',group:'',days:30,from:'',to:'',useRange:false,evOffset:0,evFilter:{type:'',action:'',malware:''},pkgTab:0,pkgSearch:'',pkgEcoFilter:'',malwareSearch:'',malwareEco:'',malwareOffset:0,dark:localStorage.dark==='1',groups:[],config:null,me:null,pendingUpdateVersion:null};
```

to:

```js
var S={page:'dashboard',group:'',days:30,from:'',to:'',useRange:false,evOffset:0,evFilter:{type:'',action:'',malware:''},pkgTab:0,pkgSearch:'',pkgEcoFilter:'',malwareSearch:'',malwareEco:'',malwareOffset:0,dark:localStorage.dark==='1',groups:[],config:null,me:null,pendingUpdateVersion:null,agentSearch:'',agentStatus:'',agentGroup:'',agentOs:''};
```

- [ ] **Step 2: Add filtering logic and the filter bar to `renderAgents()`**

In `dashboard/static/index.html`, replace this block (lines 1163-1211):

```js
async function renderAgents(){
  var agents=[];var tokens=[];
  try{agents=await api('/api/agents');}catch(e){}
  agents=(agents||[]).filter(function(a){return a&&!a.removed;});
  if(S.me&&S.me.role==='admin'){try{tokens=await api('/api/enrollment-tokens');}catch(e){}}
  var groups=S.groups||[];
  var missing=agents.filter(function(a){return epStatus(a.last_seen).label==='Offline';});
  var missingBanner=missing.length?
    '<div class="alert-bar danger" style="display:flex;border-radius:9px;margin-bottom:16px;border:1px solid #fca5a5">'+
    '&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 12 hours ago: '+
    missing.map(function(a){return'<code>'+h(a.hostname)+'</code>';}).join(', ')+'</div>':'';
  document.getElementById('content').innerHTML=
missingBanner+
(S.me&&S.me.role==='admin'?
'<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">'+
'<div class="sec-title" style="margin:0">Enrolled Agents <small>'+agents.length+' total</small></div>'+
'<div class="flex" style="gap:8px">'+
'<button class="btn btn-ghost" onclick="triggerScanAll()">Scan All ('+agents.length+')</button>'+
'<button class="btn btn-primary" onclick="openDeployWizard()">&#43; Deploy New Agent</button>'+
'</div></div>':
'<div class="sec-title">Enrolled Agents <small>'+agents.length+' total</small></div>')+
'<div class="tbl-wrap"><table><thead><tr><th>Status</th><th>Device</th><th>OS / Arch</th><th>IP Address</th><th>Group</th><th>Enrolled</th><th>Last Seen</th><th>Scan</th><th></th></tr></thead><tbody>'+
(agents.length?agents.map(function(a){
```

with:

```js
async function renderAgents(){
  var agents=[];var tokens=[];
  try{agents=await api('/api/agents');}catch(e){}
  agents=(agents||[]).filter(function(a){return a&&!a.removed;});
  if(S.me&&S.me.role==='admin'){try{tokens=await api('/api/enrollment-tokens');}catch(e){}}
  var groups=S.groups||[];
  var missing=agents.filter(function(a){return epStatus(a.last_seen).label==='Offline';});
  var missingBanner=missing.length?
    '<div class="alert-bar danger" style="display:flex;border-radius:9px;margin-bottom:16px;border:1px solid #fca5a5">'+
    '&#9888;&#65039; <strong>'+missing.length+' agent'+(missing.length>1?'s':'')+' offline</strong> — last heartbeat &gt; 12 hours ago: '+
    missing.map(function(a){return'<code>'+h(a.hostname)+'</code>';}).join(', ')+'</div>':'';
  var filteredAgents=agents;
  if(S.agentSearch){
    var q=S.agentSearch.toLowerCase();
    filteredAgents=filteredAgents.filter(function(a){
      return(a.hostname||'').toLowerCase().includes(q)
        ||(a.label||'').toLowerCase().includes(q)
        ||(a.local_ip||'').toLowerCase().includes(q)
        ||(a.remote_ip||'').toLowerCase().includes(q);
    });
  }
  if(S.agentStatus){filteredAgents=filteredAgents.filter(function(a){return epStatus(a.last_seen).label===S.agentStatus;});}
  if(S.agentGroup){
    filteredAgents=filteredAgents.filter(function(a){
      if(S.agentGroup==='__unassigned__')return!a.group_id||!groups.some(function(g){return g.id===a.group_id;});
      return a.group_id===S.agentGroup;
    });
  }
  if(S.agentOs){filteredAgents=filteredAgents.filter(function(a){return a.os===S.agentOs;});}
  var filterBar='<div class="filter-bar" style="margin-bottom:12px">'+
    '<input class="input" placeholder="Search hostname, label, IP..." value="'+h(S.agentSearch||'')+'" oninput="S.agentSearch=this.value;renderAgents()" style="flex:1;max-width:280px">'+
    '<select onchange="S.agentStatus=this.value;renderAgents()">'+
    '<option value="">All Statuses</option>'+
    '<option value="Online"'+(S.agentStatus==='Online'?' selected':'')+'>Online</option>'+
    '<option value="Away"'+(S.agentStatus==='Away'?' selected':'')+'>Away</option>'+
    '<option value="Offline"'+(S.agentStatus==='Offline'?' selected':'')+'>Offline</option>'+
    '</select>'+
    '<select onchange="S.agentGroup=this.value;renderAgents()">'+
    '<option value="">All Groups</option>'+
    '<option value="__unassigned__"'+(S.agentGroup==='__unassigned__'?' selected':'')+'>Unassigned</option>'+
    groups.map(function(g){return'<option value="'+h(g.id)+'"'+(S.agentGroup===g.id?' selected':'')+'>'+h(g.name)+'</option>';}).join('')+
    '</select>'+
    '<select onchange="S.agentOs=this.value;renderAgents()">'+
    '<option value="">All OS</option>'+
    '<option value="windows"'+(S.agentOs==='windows'?' selected':'')+'>Windows</option>'+
    '<option value="linux"'+(S.agentOs==='linux'?' selected':'')+'>Linux</option>'+
    '<option value="macos"'+(S.agentOs==='macos'?' selected':'')+'>macOS</option>'+
    '</select>'+
    '<button class="btn btn-sm btn-ghost" onclick="S.agentSearch=\'\';S.agentStatus=\'\';S.agentGroup=\'\';S.agentOs=\'\';renderAgents()">Clear</button>'+
    '</div>';
  document.getElementById('content').innerHTML=
missingBanner+
(S.me&&S.me.role==='admin'?
'<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">'+
'<div class="sec-title" style="margin:0">Enrolled Agents <small>'+agents.length+' total</small></div>'+
'<div class="flex" style="gap:8px">'+
'<button class="btn btn-ghost" onclick="triggerScanAll()">Scan All ('+agents.length+')</button>'+
'<button class="btn btn-primary" onclick="openDeployWizard()">&#43; Deploy New Agent</button>'+
'</div></div>':
'<div class="sec-title">Enrolled Agents <small>'+agents.length+' total</small></div>')+
filterBar+
'<div class="tbl-wrap"><table><thead><tr><th>Status</th><th>Device</th><th>OS / Arch</th><th>IP Address</th><th>Group</th><th>Enrolled</th><th>Last Seen</th><th>Scan</th><th></th></tr></thead><tbody>'+
(filteredAgents.length?filteredAgents.map(function(a){
```

Note this last line changes `agents.length?agents.map(...)` to `filteredAgents.length?filteredAgents.map(...)` — only the table body iterates the filtered array; every other reference to `agents` (header count, Scan All count, offline banner) is untouched on purpose (see Global Constraints).

- [ ] **Step 3: Update the empty-state row to distinguish "no agents at all" from "no agents match the filter"**

Immediately below the code from Step 2, still inside `renderAgents()`, find this line (originally line 1210, now shifted down by the Step 2 insertion):

```js
}).join(''):'<tr><td colspan="9" class="empty">No agents enrolled yet.</td></tr>')+
```

Replace it with:

```js
}).join(''):'<tr><td colspan="9" class="empty">'+(agents.length?'No agents match the current filters.':'No agents enrolled yet.')+'</td></tr>')+
```

- [ ] **Step 4: Syntax-check the modified script block**

Run:

```bash
sed -n '/<script>/,/<\/script>/p' dashboard/static/index.html | sed '1d;$d' > /tmp/agents-filter-check.js
node --check /tmp/agents-filter-check.js && echo SYNTAX_OK
```

Expected output: `SYNTAX_OK` with no errors printed above it. If `node --check` reports a `SyntaxError`, re-read the diff you just made — the most likely cause is a mismatched quote inside one of the new HTML-string-building lines — and fix it before continuing.

- [ ] **Step 5: Build the Go module to confirm the embedded static file still compiles**

```bash
go build ./...
```

Expected: no output (success). This doesn't validate the JS logic (that's Step 4 and Task 2) but confirms `//go:embed static` still packages correctly and nothing else in the module broke.

- [ ] **Step 6: Commit**

```bash
git add dashboard/static/index.html
git commit -m "$(cat <<'EOF'
feat: add search and filter dropdowns to Agents tab

Adds a client-side filter bar (search, status, group, OS) to the
Agents table so operators can quickly narrow a large fleet, matching
the existing Packages-tab filter convention. The offline-agent alert
banner and total/Scan-All counts stay based on the full fleet so
filtering never hides a fleet-health warning.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Verify the filters end-to-end against a running server

**Files:** none modified — this task validates Task 1's deliverable.

**Interfaces:**
- Consumes: the running dashboard binary built in Task 1 Step 5, the `filterBar`/`filteredAgents` logic added in Task 1 Step 2.
- Produces: nothing consumed by later tasks — this is the final acceptance check for the plan.

- [ ] **Step 1: Start a fresh local server**

```bash
DATA_DIR=$(mktemp -d)
go run . -addr="" -http-addr=":18080" -data-dir="$DATA_DIR" \
  -dash-user="admin" -dash-pass="ChangeMe123!" -malware-refresh-interval=0 &
sleep 2
curl -s http://localhost:18080/healthz
```

Expected: a JSON response (the `ok` field may be `false` due to the offline malware-feed check — that's fine, it only confirms the HTTP server itself answered).

- [ ] **Step 2: Log in and capture the session cookie**

```bash
curl -s -c /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"ChangeMe123!"}'
```

Expected: JSON containing `"role":"admin"`.

- [ ] **Step 3: Seed two groups**

```bash
G1=$(curl -s -b /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/api/groups -H 'Content-Type: application/json' -d '{"name":"win-fleet"}')
G1_ID=$(echo "$G1" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
G2=$(curl -s -b /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/api/groups -H 'Content-Type: application/json' -d '{"name":"linux-fleet"}')
G2_ID=$(echo "$G2" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "G1_ID=$G1_ID G2_ID=$G2_ID"
```

Expected: both IDs printed, non-empty.

- [ ] **Step 4: Create enrollment tokens and enroll four fake agents**

```bash
T1=$(curl -s -b /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/api/enrollment-tokens -H 'Content-Type: application/json' -d "{\"label\":\"win-token\",\"group_id\":\"$G1_ID\",\"max_uses\":0,\"ttl_hours\":0}")
TOK1=$(echo "$T1" | grep -o '"secret":"[^"]*"' | cut -d'"' -f4)
T2=$(curl -s -b /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/api/enrollment-tokens -H 'Content-Type: application/json' -d "{\"label\":\"linux-token\",\"group_id\":\"$G2_ID\",\"max_uses\":0,\"ttl_hours\":0}")
TOK2=$(echo "$T2" | grep -o '"secret":"[^"]*"' | cut -d'"' -f4)
T3=$(curl -s -b /tmp/agents-filter-cookies.txt -X POST http://localhost:18080/api/enrollment-tokens -H 'Content-Type: application/json' -d '{"label":"no-group-token","group_id":"","max_uses":0,"ttl_hours":0}')
TOK3=$(echo "$T3" | grep -o '"secret":"[^"]*"' | cut -d'"' -f4)

E1=$(curl -s -X POST http://localhost:18080/api/enroll -H 'Content-Type: application/json' -d "{\"token\":\"$TOK1\",\"hostname\":\"WIN-DESKTOP-01\",\"os\":\"windows\",\"arch\":\"amd64\",\"pmg_version\":\"0.18.10\",\"local_ip\":\"10.0.0.11\"}")
KEY1=$(echo "$E1" | grep -o '"api_key":"[^"]*"' | cut -d'"' -f4)
curl -s -X POST http://localhost:18080/api/enroll -H 'Content-Type: application/json' -d "{\"token\":\"$TOK2\",\"hostname\":\"LNX-SVR-01\",\"os\":\"linux\",\"arch\":\"amd64\",\"pmg_version\":\"0.18.10\",\"local_ip\":\"10.0.0.21\"}"
curl -s -X POST http://localhost:18080/api/enroll -H 'Content-Type: application/json' -d "{\"token\":\"$TOK2\",\"hostname\":\"LNX-SVR-02\",\"os\":\"linux\",\"arch\":\"arm64\",\"pmg_version\":\"0.18.10\",\"local_ip\":\"10.0.0.22\"}"
curl -s -X POST http://localhost:18080/api/enroll -H 'Content-Type: application/json' -d "{\"token\":\"$TOK3\",\"hostname\":\"MAC-LAPTOP-01\",\"os\":\"macos\",\"arch\":\"arm64\",\"pmg_version\":\"0.18.10\",\"local_ip\":\"10.0.0.31\"}"

curl -s -X POST http://localhost:18080/api/heartbeat -H "Authorization: $KEY1" -H 'Content-Type: application/json' -d '{"version":"0.18.10","os":"windows","arch":"amd64","local_ip":"10.0.0.11"}'
```

Expected: 4 successful enroll responses (each containing `agent_id`) and one successful heartbeat response.

- [ ] **Step 5: Confirm the seed via API**

```bash
curl -s -b /tmp/agents-filter-cookies.txt http://localhost:18080/api/agents | python3 -m json.tool
```

Expected: 4 agents — `WIN-DESKTOP-01` (os windows, group win-fleet, recent `last_seen`), `LNX-SVR-01` and `LNX-SVR-02` (os linux, group linux-fleet, `last_seen` null), `MAC-LAPTOP-01` (os macos, auto-created "Enrolled Agents" group, `last_seen` null).

- [ ] **Step 6: Drive the browser and verify each filter**

Using the Playwright browser tools:
1. `browser_navigate` to `http://localhost:18080/`.
2. Log in through the UI form with `admin` / `ChangeMe123!`.
3. Navigate to the Agents tab.
4. `browser_snapshot` — confirm 4 rows are visible, header reads "4 total", and the offline banner lists 3 offline hosts (LNX-SVR-01, LNX-SVR-02, MAC-LAPTOP-01).
5. Type `win` into the search input (`browser_type`) — `browser_snapshot` and confirm only `WIN-DESKTOP-01` remains, header still reads "4 total" (unfiltered count unchanged), offline banner still lists all 3 offline hosts.
6. Clear the search box, then select `Offline` in the Status dropdown — confirm 3 rows remain (`LNX-SVR-01`, `LNX-SVR-02`, `MAC-LAPTOP-01`), `WIN-DESKTOP-01` is hidden.
7. Reset Status to "All Statuses", select `linux-fleet` in the Group dropdown — confirm exactly `LNX-SVR-01` and `LNX-SVR-02` remain.
8. Enrolling with an empty `group_id` auto-assigns the agent to a server-created "Enrolled Agents" group rather than leaving it truly unassigned (confirmed in Step 5's JSON: `MAC-LAPTOP-01`'s `group_id` is non-empty). So: reset Group to "All Groups", select `Unassigned` — confirm **zero** rows show (no agent in this seed is truly unassigned). Then select `Enrolled Agents` in the same dropdown — confirm exactly `MAC-LAPTOP-01` remains.
9. Reset Group to "All Groups", select `macOS` in the OS dropdown — confirm exactly `MAC-LAPTOP-01` remains.
10. Click "Clear" — confirm all 4 rows reappear and all four filter controls reset to their default ("All ..." / empty search).
11. Combine two filters at once (e.g. search `LNX` + Status `Offline`) — confirm both `LNX-SVR-01` and `LNX-SVR-02` remain (AND semantics, not OR).

Expected: every sub-step's row set matches what's described. If any step shows the wrong rows, go back to Task 1 Step 2's filtering logic and re-check the predicate for that specific filter before re-running this step.

- [ ] **Step 7: Tear down**

```bash
kill %1 2>/dev/null
rm -rf "$DATA_DIR" /tmp/agents-filter-cookies.txt /tmp/agents-filter-check.js
```

No commit for this task — it only validates Task 1, which is already committed.
