# Unified Endpoints Dashboard — Design

## Problem

The dashboard presents enrolled agents and endpoint activity as two peer tabs even though they describe the same fleet inventory from different data sources. Both views repeat status, device identity, platform, network, PMG version, and last-seen information. The split forces operators to switch tabs to answer basic questions such as whether a managed device is connected, what activity it produced, and whether it needs a malware scan.

The backend already models endpoints as a merged view of enrollment records and event-derived activity. The UI should reflect that model.

## Domain model

The unified page is named **Endpoints**. An endpoint has one of two types:

- **Managed**: backed by an active enrollment record. It can be renamed, assigned to a group, scanned, and removed according to the current role policy.
- **Observed**: known only from event telemetry. It has activity and identity data but no enrollment-management actions.

`agent_id` and `endpoint_id` remain separate identifiers:

- `agent_id` addresses enrollment operations: rename, group assignment, scan, and agent removal.
- `endpoint_id` addresses telemetry operations: event lookup and telemetry deletion.

The backend may match an event source to an enrolled agent by agent ID or by hostname, so the two identifiers must never be assumed equal.

## Navigation and compatibility

The sidebar contains one **Endpoints** item under Management. The separate **Agents** item is removed.

Both legacy hashes remain compatible:

- `#endpoints` opens the unified page.
- `#agents` is normalized to `#endpoints` so existing bookmarks do not break.

Editors are no longer forced onto an Agents-only route; they enter the unified Endpoints page.

## Unified endpoint API

`GET /api/endpoints` becomes the inventory API for admin, editor, and viewer roles. It returns the same endpoint identity and activity shape for every role, including both `agent_id` and `endpoint_id`; the UI determines visible actions from the authenticated role and whether `agent_id` is present. Existing mutation endpoints remain authoritative for server-side authorization.

Each endpoint response includes:

- merged identity: `agent_id`, `endpoint_id`, hostname, label, type;
- platform and network: OS, architecture, PMG version, local IP, public IP;
- organization metadata: group ID and enrollment timestamp;
- lifecycle: first seen and last seen;
- activity for the selected period: sessions, package count, blocked package count;
- managed-agent scan state: state, dispatch/last-scan timestamps, findings summary;
- role-independent classification: managed when `agent_id` is non-empty, otherwise observed.

The current CSV response remains available from `/api/endpoints?format=csv` and reflects the unified inventory.

## Filtering semantics

The page supports client-side search and dimension filters over the returned inventory:

- search matches label, hostname, endpoint ID, agent ID, and IP addresses;
- status: Online, Away, Offline;
- type: Managed, Observed;
- group: all groups or a specific group;
- OS;
- PMG version.

Global group and time controls have different semantics:

- `group_id` filters both enrolled agent records and event activity before the merge. A zero-event managed agent outside the selected group must not reappear in the result.
- `days` or `from`/`to` limits only activity metrics and the event detail query. It does not determine whether a managed endpoint exists.
- status uses the most recent heartbeat or observed activity and is independent of the activity period.

The existing thresholds remain unchanged:

- Online: last seen less than 30 minutes ago.
- Away: last seen from 30 minutes through less than 12 hours ago.
- Offline: no last-seen value or at least 12 hours old.

## Page layout

### Header actions

The page header reads **Endpoints** with the description “Manage enrolled agents and observed event sources.”

Actions:

- CSV export for roles that can view the endpoint inventory;
- Scan All for admins, operating only on managed endpoints;
- Deploy Agent for admins;
- Enrollment Tokens for admins, opening a modal rather than appending a second inventory table.

### KPI row

The first row contains clickable summary cards:

- Total;
- Online;
- Away / Offline;
- Outdated;
- Blocked.

A status or compliance KPI applies the corresponding table filter. “Outdated” compares known PMG versions to the latest detected version. Endpoints with unknown versions are not marked outdated; their version remains filterable as Unknown. “Blocked” means at least one blocked package in the selected activity period.

The current full Version Compliance table is removed from the default page because the KPI and version filter cover its primary inventory use case.

### Inventory table

The desktop table contains eight information groups:

| Column | Content |
|---|---|
| Status | Online, Away, or Offline indicator |
| Device | label, hostname, shortened endpoint ID |
| Type | Managed or Observed badge |
| Platform | OS/architecture and PMG version |
| Group | resolved group name or unassigned marker |
| Activity | sessions, packages, and blocked count |
| Last Seen | formatted timestamp |
| Scan / Actions | scan state for managed endpoints and contextual action menu |

IP addresses, full identifiers, enrollment time, and first-seen time remain in the detail drawer rather than expanding the table.

### Detail drawer

The existing drawer remains the primary detail surface with three tabs:

1. **Overview**: endpoint type, identity, group, network, platform, version, enrollment/first-seen/last-seen values, activity totals, scan state, and the separate endpoint/agent identifiers.
2. **Events**: recent events loaded by `endpoint_id` and constrained by the selected activity period.
3. **Packages**: package aggregation from those endpoint events.

Overview also exposes contextual management actions:

- Managed + editor/admin: Rename.
- Managed + admin: Assign Group, Run Scan, Remove Agent.
- Observed + admin: Delete Telemetry.
- Other combinations: read-only.

Actions remain absent rather than disabled when the endpoint type or role does not support them.

## Destructive operations

Removing a managed agent and deleting telemetry are distinct operations.

- `DELETE /api/agents/{agent_id}` removes/revokes the managed enrollment. Its confirmation explicitly states whether telemetry is also deleted. This design preserves the current backend behavior: removing an enrolled agent deletes its events as one operation.
- `DELETE /api/endpoints/{endpoint_id}/events` deletes telemetry for an observed endpoint. It must use `endpoint_id`, not `agent_id`, and is admin-only.

The endpoint telemetry route must accept an observed endpoint. It must not require a removed enrollment record, because observed endpoints do not have one. It rejects attempts to use the route for an active managed endpoint.

## Enrollment tokens

Enrollment tokens are an administrative workflow, not a second fleet inventory. The page provides an **Enrollment Tokens** button that opens a modal containing the existing active-token list and New Token/Revoke actions. The Deploy Agent wizard remains unchanged except that completion refreshes the unified Endpoints page.

## RBAC

- **Viewer**: view unified endpoint inventory and details; export CSV if currently permitted by the endpoint API; no mutations.
- **Editor**: viewer capabilities plus rename managed endpoints. Editors cannot change groups, scan, remove, deploy, or manage tokens.
- **Admin**: all viewer/editor capabilities plus group assignment, scan, Scan All, remove managed agents, delete observed telemetry, deploy, and manage enrollment tokens.

Backend checks remain mandatory even when the UI hides an action.

## Responsive behavior

- Desktop: eight-column unified table.
- Tablet: hide or visually collapse lower-priority Group and Activity detail while keeping Device, Type, Status, Platform, Last Seen, and Actions accessible.
- Mobile: endpoint rows become compact stacked cards or a two-column row layout; the detail drawer uses nearly the full viewport width.
- Table wrappers use horizontal scrolling as a fallback rather than clipping content.
- Header actions and filters wrap without introducing horizontal page overflow.

## Error and empty states

- Failure to load the endpoint API renders the existing page-level error state; it must not silently present an empty fleet.
- No inventory returns “No endpoints found.”
- An active filter with zero matches returns “No endpoints match the current filters.”
- A drawer event request failure leaves Overview usable and shows the error only in Events/Packages content.
- Mutation failures use the existing toast mechanism and do not optimistically remove or alter rows.

## Testing

Automated Go tests cover:

- merge behavior and preservation of separate agent/endpoint IDs;
- group filtering for both agent and event inputs;
- period-limited activity with inventory retention;
- endpoint inventory access for all three roles;
- editor mutation restrictions;
- observed telemetry deletion through the endpoint route;
- rejection of telemetry deletion for active managed endpoints;
- CSV compatibility.

Frontend verification covers:

- legacy `#agents` normalization;
- role-based actions for Managed and Observed rows;
- KPI and filter behavior;
- drawer identity/activity/scan rendering;
- correct identifier use for management versus telemetry operations;
- enrollment-token modal and deployment refresh;
- desktop, tablet, and mobile layouts;
- JavaScript syntax and Go embed/build checks.

## Non-goals

- Server-side pagination or search; current lists are fetched as one inventory.
- A new device-management API namespace; the existing endpoint and agent routes remain.
- Persisting page filters across a full browser reload.
- Changing the 30-minute/12-hour status thresholds.
- Retaining a separate Agents page after migration.
