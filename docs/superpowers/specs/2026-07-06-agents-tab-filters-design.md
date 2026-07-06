# Agents tab filters — design

## Problem

The Agents tab (`renderAgents()` in `dashboard/static/index.html`) renders every
enrolled agent in one table with no way to narrow the list. As fleets grow this
makes it slow to find a specific machine or check the status of a subset of
agents. Other tabs (Packages, Malware Feed) already have a filter bar; Agents
does not.

## Approach

Follow the existing **Packages tab** convention: a `.filter-bar` with a search
input and dropdowns, filtering a client-side array with instant (`oninput`)
feedback, no backend changes. This fits because:

- The agent list is already fetched in full via `GET /api/agents` (no
  pagination), so client-side filtering is cheap and avoids new API surface.
- It reuses existing CSS classes (`.filter-bar`, `.input`) and the existing
  `epStatus()` helper for status, and `S.groups` for group names — no new
  dependencies.

Alternative considered: server-side filtering via query params (like the
Malware Feed tab). Rejected because agent lists are small (tens to low
hundreds per org) and already loaded in full; adding query params and
re-fetching on every keystroke/dropdown change adds complexity with no real
benefit at this scale.

## UI

A `.filter-bar` row inserted between the offline banner and the "Enrolled
Agents" table, visible to all roles that can see the Agents tab:

- **Search input** (`placeholder="Search hostname, label, IP..."`) — matches
  (case-insensitive substring) against `hostname`, `label`, `local_ip`,
  `remote_ip`.
- **Status dropdown** — options: All, Online, Away, Offline. Matches
  `epStatus(a.last_seen).label`.
- **Group dropdown** — options: All, Unassigned, then one entry per
  `S.groups` (by name). "Unassigned" matches agents with no `group_id` or a
  `group_id` not found in `S.groups`.
- **OS dropdown** — options: All, Linux, macOS, Windows. Matches `a.os`.
- **Clear button** — resets all four filter fields and re-renders.

Layout and styling match the Packages tab's filter bar exactly (same CSS
classes, same `btn btn-sm btn-ghost` Clear button).

## State

New fields on the global `S` object (next to `pkgSearch`/`pkgEcoFilter`):

```js
agentSearch:'', agentStatus:'', agentGroup:'', agentOs:''
```

All default to `''` (meaning "All" / no filter). These are session-only (not
persisted to localStorage), matching how `pkgSearch`/`malwareSearch` behave
today.

## Filtering logic

Inside `renderAgents()`, after the existing `agents=(agents||[]).filter(a =>
a && !a.removed)` line, apply the four filters in sequence (all must match —
AND semantics, same as Packages' search+ecosystem combo):

```js
if (S.agentSearch) {
  var q = S.agentSearch.toLowerCase();
  agents = agents.filter(function(a){
    return (a.hostname||'').toLowerCase().includes(q)
        || (a.label||'').toLowerCase().includes(q)
        || (a.local_ip||'').toLowerCase().includes(q)
        || (a.remote_ip||'').toLowerCase().includes(q);
  });
}
if (S.agentStatus) {
  agents = agents.filter(function(a){ return epStatus(a.last_seen).label === S.agentStatus; });
}
if (S.agentGroup) {
  agents = agents.filter(function(a){
    if (S.agentGroup === '__unassigned__') return !a.group_id || !groups.some(function(g){return g.id===a.group_id;});
    return a.group_id === S.agentGroup;
  });
}
if (S.agentOs) {
  agents = agents.filter(function(a){ return a.os === S.agentOs; });
}
```

The offline banner and the "N total" count in the section header are computed
from the **filtered** list, so both reflect what's currently visible — same
behavior as Packages tab counts.

Filtering happens after the `groups` lookup variable is available (it's
already defined before the table is built), so the "Unassigned" case can use
it.

## Non-goals

- No backend/API changes.
- No persistence of filter state across full page reloads — filters live only
  on the in-memory `S` object, same as `pkgSearch`/`malwareSearch`. They do
  persist across re-renders and tab switches within the same page session
  (switching to another tab and back to Agents keeps the filters applied),
  matching existing Packages tab behavior.
- No changes to the Enrollment Tokens table below the agents table.
- No filtering on Scan status or Enrolled/Last Seen dates (not requested).

## Testing

This is a frontend-only, vanilla-JS change with no existing JS test harness
in this repo. Verification is manual: run the dashboard locally, enroll/seed
a few agents with different OS/group/status combinations, and confirm each
filter (and their combination) narrows the table correctly, the Clear button
resets everything, and the "N total" / offline banner update accordingly.
