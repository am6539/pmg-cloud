package dashboard

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ctxKey string

const ctxSession ctxKey = "session"

func sessionFromContext(r *http.Request) (DashSession, bool) {
	s, ok := r.Context().Value(ctxSession).(DashSession)
	return s, ok
}

//go:embed static
var staticFiles embed.FS

// HandlerDeps bundles optional dependencies for the dashboard handler.
type HandlerDeps struct {
	Mirror       *MalwareMirror
	Groups       *GroupStore      // may be nil
	Config       *ConfigStore     // may be nil
	Audit        *AuditLog        // may be nil
	Webhook      *WebhookDelivery // may be nil
	Users        *UserStore       // may be nil; enables session-based auth
	Sessions     *SessionStore    // required when Users is set
	Enrollment   *EnrollmentStore // may be nil; enables agent enrollment
	Updates      *UpdateStore     // may be nil; enables PMG agent update management
	Policy       *PolicyStore     // may be nil; enables org-wide package policy
	GRPCAddr     string           // gRPC endpoint reported to enrolling agents
	GRPCInsecure bool             // when true, enrolling agents skip TLS verification for gRPC
}

const installScriptTemplatePS1 = `# PMG Windows Install Script
$ErrorActionPreference = 'Stop'
$PMG_SERVER = '{{SERVER_URL}}'
$PMG_TOKEN  = if ($env:PMG_TOKEN) { $env:PMG_TOKEN } else { '' }

foreach ($arg in $args) {
  if ($arg -match '^--token=(.+)$') { $PMG_TOKEN = $Matches[1] }
}

if (-not $PMG_TOKEN) {
  Write-Error '--token=TOKEN is required'; exit 1
}

# Detect architecture
$archRaw = if ([Environment]::Is64BitOperatingSystem) {
  if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
} else { 'i386' }

# Download PMG binary from pmg-cloud server (no internet required)
$dir = "$env:LOCALAPPDATA\pmg"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$bin = "$dir\pmg.exe"

$binaryURL = "${PMG_SERVER}/bin/windows/${archRaw}/pmg"
Write-Host "Downloading PMG from ${binaryURL}..."
try {
  Invoke-WebRequest -Uri $binaryURL -OutFile $bin -UseBasicParsing
} catch {
  Write-Error "Failed to download PMG binary from server: $_"; exit 1
}
if (-not (Test-Path $bin)) { Write-Error "pmg.exe not found after download"; exit 1 }

# Add to PATH (user scope)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  $env:Path += ";$dir"
}

# Enroll with pmg-cloud
Write-Host 'Enrolling with PMG Cloud...'
& $bin cloud enroll --endpoint="$PMG_SERVER" --token="$PMG_TOKEN"

# Wire PMG into shell
Write-Host 'Setting up PMG...'
& $bin setup install

# Refresh PATH in the current session from registry so shims work immediately
# without needing to restart the terminal.
$machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
$userPath    = [Environment]::GetEnvironmentVariable('PATH', 'User')
$env:PATH    = ($machinePath + ';' + $userPath) -replace ';;+', ';'

Write-Host ''
Write-Host 'Done! PMG is installed, enrolled, and active.'
`

const installScriptTemplate = `#!/bin/sh
set -eu
PMG_SERVER="{{SERVER_URL}}"
PMG_TOKEN="${PMG_TOKEN:-}"

# Parse --token flag
for arg in "$@"; do
  case "$arg" in
    --token=*) PMG_TOKEN="${arg#--token=}" ;;
  esac
done

if [ -z "$PMG_TOKEN" ]; then
  echo "Error: --token=TOKEN is required" >&2
  exit 1
fi

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)          ARCH="amd64" ;;
  aarch64|arm64)   ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Install directory
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

# Download PMG binary from pmg-cloud server (no internet required)
BINARY_URL="${PMG_SERVER}/bin/${OS}/${ARCH}/pmg"
echo "Downloading PMG from ${BINARY_URL}..."
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "${BINARY_URL}" -o "${INSTALL_DIR}/pmg"
elif command -v wget >/dev/null 2>&1; then
  wget -q -O "${INSTALL_DIR}/pmg" "${BINARY_URL}"
else
  echo "Error: curl or wget is required" >&2; exit 1
fi
chmod +x "${INSTALL_DIR}/pmg"

# Add to PATH if not already present
case ":${PATH}:" in
  *:"${INSTALL_DIR}":*) ;;
  *)
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.bashrc"
    echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "${HOME}/.zshrc" 2>/dev/null || true
    export PATH="${INSTALL_DIR}:${PATH}"
    ;;
esac

echo "PMG installed to ${INSTALL_DIR}/pmg"

# Enroll with server
echo "Enrolling with PMG Cloud..."
"${INSTALL_DIR}/pmg" cloud enroll --endpoint="$PMG_SERVER" --token="$PMG_TOKEN"

# Wire PMG into shell (aliases + shims)
echo "Wiring PMG into your shell..."
"${INSTALL_DIR}/pmg" setup install

echo ""
echo "Done! PMG is installed, enrolled, and active."
echo "Restart your terminal (or run: source ~/.bashrc) for shell integration to take effect."
`

// Handler returns an http.Handler for the dashboard.
// dataDir is the path to the JSONL event directory.
func Handler(dataDir string, deps HandlerDeps) http.Handler {
	reader := NewReader(dataDir)
	mux := http.NewServeMux()
	mirror := deps.Mirror

	// serve static files at /
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// API: dashboard — combined stats + recent events in one call
	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = filterByGroup(events, r.URL.Query().Get("group_id"))
		writeJSON(w, Aggregate(events))
	})

	// API: stats (kept for backwards compat)
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, Aggregate(events))
	})

	// API: recent events list with optional filter and date-range support
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		var events []Event
		var err error

		fromStr := q.Get("from")
		toStr := q.Get("to")
		if fromStr != "" || toStr != "" {
			from, to, parseErr := parseDateRange(fromStr, toStr)
			if parseErr != nil {
				http.Error(w, parseErr.Error(), http.StatusBadRequest)
				return
			}
			events, err = reader.LoadEventsRange(from, to)
		} else {
			days := parseDays(r, 30)
			events, err = reader.LoadEvents(days)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		events = filterByGroup(events, q.Get("group_id"))
		// optional filter by event_type
		if et := q.Get("event_type"); et != "" {
			filtered := make([]Event, 0, len(events))
			for _, ev := range events {
				if ev.EventType == et {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}
		// optional filter by action (comma-separated)
		if a := q.Get("action"); a != "" {
			actions := splitComma(a)
			filtered := make([]Event, 0, len(events))
			for _, ev := range events {
				if actions[ev.Action] {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}
		// optional filter malware=true|false
		if m := q.Get("malware"); m != "" {
			want := m == "true"
			filtered := make([]Event, 0, len(events))
			for _, ev := range events {
				if ev.IsMalware != nil && *ev.IsMalware == want {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}
		// sort newest first
		sort.Slice(events, func(i, j int) bool {
			return events[i].ReceivedAt.After(events[j].ReceivedAt)
		})
		// limit (hard cap at 10 000 to prevent memory exhaustion)
		limit := 100
		if l := q.Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 10000 {
			limit = 10000
		}
		// offset (pagination)
		offset := 0
		if o := q.Get("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil && n >= 0 {
				offset = n
			}
		}
		if offset > len(events) {
			offset = len(events)
		}
		events = events[offset:]
		if len(events) > limit {
			events = events[:limit]
		}

		// CSV download
		if q.Get("format") == "csv" {
			writeEventsCSV(w, events)
			return
		}

		writeJSON(w, events)
	})

	// API: package stats — top packages, ecosystems, endpoints by install count
	mux.HandleFunc("/api/package-stats", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Build comprehensive package stats
		type pkgKey struct {
			eco, name string
		}
		pkgMap := make(map[pkgKey]*map[string]interface{})
		ecoMap := make(map[string]int)

		for _, ev := range events {
			if ev.EventType != "PACKAGE_DECISION" || ev.PackageName == "" {
				continue
			}
			k := pkgKey{ev.Ecosystem, ev.PackageName}
			p, ok := pkgMap[k]
			if !ok {
				p = &map[string]interface{}{
					"name":                   ev.PackageName,
					"ecosystem":              ev.Ecosystem,
					"count":                  0,
					"blocked_count":          0,
					"malware_count":          0,
					"cooldown_blocked_count": 0,
					"versions":               []string{},
					"last_seen":              ev.ReceivedAt,
				}
				pkgMap[k] = p
			}
			pkg := *p
			pkg["count"] = pkg["count"].(int) + 1
			if ev.Action == "BLOCKED" {
				pkg["blocked_count"] = pkg["blocked_count"].(int) + 1
			}
			if ev.IsMalware != nil && *ev.IsMalware {
				pkg["malware_count"] = pkg["malware_count"].(int) + 1
			}
			if ev.ReceivedAt.After(pkg["last_seen"].(time.Time)) {
				pkg["last_seen"] = ev.ReceivedAt
			}
			// Track versions
			if ev.PackageVersion != "" {
				versions := pkg["versions"].([]string)
				found := false
				for _, v := range versions {
					if v == ev.PackageVersion {
						found = true
						break
					}
				}
				if !found {
					pkg["versions"] = append(versions, ev.PackageVersion)
				}
			}
			ecoMap[ev.Ecosystem]++
		}

		// Convert to arrays
		topPackages := make([]map[string]interface{}, 0, len(pkgMap))
		for _, p := range pkgMap {
			topPackages = append(topPackages, *p)
		}
		sort.Slice(topPackages, func(i, j int) bool {
			return topPackages[i]["count"].(int) > topPackages[j]["count"].(int)
		})

		topEcosystems := make([]map[string]interface{}, 0, len(ecoMap))
		for eco, count := range ecoMap {
			topEcosystems = append(topEcosystems, map[string]interface{}{
				"name":  eco,
				"count": count,
			})
		}
		sort.Slice(topEcosystems, func(i, j int) bool {
			return topEcosystems[i]["count"].(int) > topEcosystems[j]["count"].(int)
		})

		writeJSON(w, map[string]interface{}{
			"top_packages":    topPackages,
			"top_ecosystems": topEcosystems,
		})
	})

	// API: endpoints — exact match (list)
	mux.HandleFunc("/api/endpoints", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		events, err := reader.LoadEvents(0) // all time
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = filterByGroup(events, r.URL.Query().Get("group_id"))
		var list []EndpointInfo
		if deps.Enrollment != nil {
			list = MergeAgentEndpoints(deps.Enrollment.ListAllAgents(), events)
		}
		if r.URL.Query().Get("format") == "csv" {
			writeEndpointsCSV(w, list)
			return
		}
		writeJSON(w, list)
	})

	// API: per-endpoint events: /api/endpoints/{id}/events
	mux.HandleFunc("/api/endpoints/", func(w http.ResponseWriter, r *http.Request) {
		tail := strings.TrimPrefix(r.URL.Path, "/api/endpoints/")
		parts := strings.SplitN(tail, "/", 2)
		if len(parts) != 2 || parts[1] != "events" {
			http.NotFound(w, r)
			return
		}
		endpointID := parts[0]
		if endpointID == "" {
			http.NotFound(w, r)
			return
		}

		// DELETE /api/endpoints/{id}/events - admin only, delete events for removed agent
		if r.Method == http.MethodDelete {
			s, ok := sessionFromContext(r)
			if !ok || s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			// Check if agent exists and is removed
			if deps.Enrollment != nil {
				agent, found := deps.Enrollment.GetAgentByID(endpointID)
				if !found {
					http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
					return
				}
				if !agent.Removed {
					http.Error(w, `{"error":"cannot delete events for active agent"}`, http.StatusBadRequest)
					return
				}
			}
			// Delete events for this endpoint
			if err := reader.DeleteEventsByEndpointID(dataDir, endpointID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]string{"status": "ok"})
			return
		}

		// GET /api/endpoints/{id}/events
		events, err := reader.LoadEvents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		filtered := make([]Event, 0)
		for _, ev := range events {
			if ev.EndpointID == endpointID {
				filtered = append(filtered, ev)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].ReceivedAt.After(filtered[j].ReceivedAt)
		})
		if len(filtered) > 200 {
			filtered = filtered[:200]
		}
		writeJSON(w, filtered)
	})

	// API: CI stats
	mux.HandleFunc("/api/ci-stats", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ComputeCIStats(events))
	})

	// Aikido malware feed mirror
	mux.HandleFunc("/malware_predictions.json", func(w http.ResponseWriter, r *http.Request) {
		data, _ := mirror.NPMFeed(r.Context())
		if data == nil {
			http.Error(w, "feed unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data) //nolint:errcheck
	})

	mux.HandleFunc("/malware_pypi.json", func(w http.ResponseWriter, r *http.Request) {
		data, _ := mirror.PyPIFeed(r.Context())
		if data == nil {
			http.Error(w, "feed unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data) //nolint:errcheck
	})

	mux.HandleFunc("/api/malware/status", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		writeJSON(w, mirror.Status())
	})

	mux.HandleFunc("/api/malware/refresh", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := mirror.Refresh(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if deps.Audit != nil {
			deps.Audit.Log("feed_refreshed", "malware", "")
		}
		writeJSON(w, mirror.Status())
	})

	// API: malware entries with search/filter
	mux.HandleFunc("/api/malware/entries", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		ecosystem := r.URL.Query().Get("ecosystem") // npm or pypi
		search := strings.ToLower(r.URL.Query().Get("search"))
		limit := 50
		offset := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
				limit = v
			}
		}
		if o := r.URL.Query().Get("offset"); o != "" {
			if v, err := strconv.Atoi(o); err == nil && v >= 0 {
				offset = v
			}
		}

		var entries []map[string]interface{}
		if ecosystem == "npm" || ecosystem == "" {
			data, _ := mirror.NPMFeed(r.Context())
			if data != nil {
				var npmEntries []map[string]interface{}
				if err := json.Unmarshal(data, &npmEntries); err == nil {
					for _, e := range npmEntries {
						e["ecosystem"] = "npm"
						entries = append(entries, e)
					}
				}
			}
		}
		if ecosystem == "pypi" || ecosystem == "" {
			data, _ := mirror.PyPIFeed(r.Context())
			if data != nil {
				var pypiEntries []map[string]interface{}
				if err := json.Unmarshal(data, &pypiEntries); err == nil {
					for _, e := range pypiEntries {
						e["ecosystem"] = "pypi"
						entries = append(entries, e)
					}
				}
			}
		}

		// Filter by search
		if search != "" {
			filtered := make([]map[string]interface{}, 0)
			for _, e := range entries {
				// Try both 'name' and 'package' field names (Aikido uses 'name')
				var pkg string
				if n, ok := e["name"].(string); ok {
					pkg = n
				} else if p, ok := e["package"].(string); ok {
					pkg = p
				}
				if strings.Contains(strings.ToLower(pkg), search) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		// Paginate
		total := len(entries)
		if offset >= total {
			offset = 0
		}
		end := offset + limit
		if end > total {
			end = total
		}
		paginated := entries[offset:end]

		writeJSON(w, map[string]interface{}{
			"entries": paginated,
			"total":   total,
			"offset":  offset,
			"limit":   limit,
		})
	})

	// API: malware statistics
	mux.HandleFunc("/api/malware/stats", func(w http.ResponseWriter, r *http.Request) {
		s, ok := sessionFromContext(r)
		if ok && s.Role == RoleEditor {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		_, npmStatus := mirror.NPMFeed(r.Context())
		_, pypiStatus := mirror.PyPIFeed(r.Context())

		// Count entries per ecosystem
		npmCount := npmStatus.EntryCount
		pypiCount := pypiStatus.EntryCount

		// Get recent detections
		events, _ := reader.LoadEvents(7) // last 7 days
		malwareByPackage := make(map[string]int)
		for _, ev := range events {
			if ev.IsMalware != nil && *ev.IsMalware && ev.PackageName != "" {
				malwareByPackage[ev.PackageName]++
			}
		}

		// Top 10 detected malware
		type detection struct {
			Package string `json:"package"`
			Count   int    `json:"count"`
		}
		detections := make([]detection, 0)
		for pkg, count := range malwareByPackage {
			detections = append(detections, detection{Package: pkg, Count: count})
		}
		sort.Slice(detections, func(i, j int) bool { return detections[i].Count > detections[j].Count })
		if len(detections) > 10 {
			detections = detections[:10]
		}

		writeJSON(w, map[string]interface{}{
			"npm": map[string]interface{}{
				"total":        npmCount,
				"last_updated": npmStatus.LastUpdated,
			},
			"pypi": map[string]interface{}{
				"total":        pypiCount,
				"last_updated": pypiStatus.LastUpdated,
			},
			"detections": detections,
		})
	})

	// Config management APIs — only when Config is wired.
	if deps.Config != nil {
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if ok && s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, deps.Config.Get())
			case http.MethodPost:
				var cfg ServerConfig
				if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				if err := deps.Config.Update(cfg); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("config_updated", "server", "")
				}
				writeJSON(w, deps.Config.Get())
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		mux.HandleFunc("/api/config/webhooks", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, deps.Config.Get().Webhooks)
			case http.MethodPost:
				var wh WebhookEntry
				if err := json.NewDecoder(r.Body).Decode(&wh); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				created, err := deps.Config.AddWebhook(wh)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("webhook_added", created.ID, created.Name)
				}
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, created)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		mux.HandleFunc("/api/config/webhooks/", func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimPrefix(r.URL.Path, "/api/config/webhooks/")
			if id == "" {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodPut:
				var wh WebhookEntry
				if err := json.NewDecoder(r.Body).Decode(&wh); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				wh.ID = id
				if err := deps.Config.UpdateWebhook(wh); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("webhook_updated", id, wh.Name)
				}
				writeJSON(w, wh)
			case http.MethodDelete:
				if err := deps.Config.DeleteWebhook(id); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("webhook_deleted", id, "")
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// Alert channel management (admin only — channels hold secret tokens).
		mux.HandleFunc("/api/config/alert-channels", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, deps.Config.Get().AlertChannels)
			case http.MethodPost:
				var ch AlertChannel
				if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				created, err := deps.Config.AddAlertChannel(ch)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("alert_channel_added", created.ID, created.Kind+" "+created.Name)
				}
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, created)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// DELETE /api/config/alert-channels/{id}  |  POST .../{id}/test
		mux.HandleFunc("/api/config/alert-channels/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			tail := strings.TrimPrefix(r.URL.Path, "/api/config/alert-channels/")
			if tail == "" {
				http.NotFound(w, r)
				return
			}
			// POST /api/config/alert-channels/{id}/test — send a test alert
			if strings.HasSuffix(tail, "/test") && r.Method == http.MethodPost {
				id := strings.TrimSuffix(tail, "/test")
				ch, found := deps.Config.GetAlertChannel(id)
				if !found {
					http.Error(w, `{"error":"channel not found"}`, http.StatusNotFound)
					return
				}
				if err := sendAlert(ch, "✅ PMG test alert — this channel is configured correctly"); err != nil {
					http.Error(w, "test failed: "+err.Error(), http.StatusBadGateway)
					return
				}
				writeJSON(w, map[string]bool{"ok": true})
				return
			}
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := deps.Config.DeleteAlertChannel(tail); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("alert_channel_deleted", tail, "")
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	// Audit log API — only when Audit is wired.
	if deps.Audit != nil {
		mux.HandleFunc("/api/audit", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if ok && s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			limit := 200
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 {
					limit = n
				}
			}
			if limit > 1000 {
				limit = 1000
			}
			entries, err := deps.Audit.Read(limit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, entries)
		})
	}

	// Group management APIs — only when Groups is wired.
	if deps.Groups != nil {
		groups := deps.Groups

		// GET  /api/groups        — list all groups with key counts
		// POST /api/groups        — create group {name}
		mux.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if ok && s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				list := groups.ListGroups()
				counts := groups.KeyCount()
				type groupRow struct {
					Group
					KeyCount int `json:"key_count"`
				}
				rows := make([]groupRow, len(list))
				for i, g := range list {
					rows[i] = groupRow{Group: g, KeyCount: counts[g.ID]}
				}
				writeJSON(w, rows)

			case http.MethodPost:
				var body struct {
					Name string `json:"name"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				g, err := groups.CreateGroup(body.Name)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("group_created", g.ID, g.Name)
				}
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, g)

			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// GET /api/groups/export — export group list + key metadata (no hashes)
		mux.HandleFunc("/api/groups/export", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if ok && s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			type keyMeta struct {
				ID        string    `json:"id"`
				GroupID   string    `json:"group_id"`
				Name      string    `json:"name"`
				KeyPrefix string    `json:"key_prefix"`
				CreatedAt time.Time `json:"created_at"`
			}
			type exportRow struct {
				Group
				Keys []keyMeta `json:"keys"`
			}
			list := groups.ListGroups()
			rows := make([]exportRow, 0, len(list))
			for _, g := range list {
				rawKeys := groups.ListAPIKeys(g.ID)
				metas := make([]keyMeta, 0, len(rawKeys))
				for _, k := range rawKeys {
					metas = append(metas, keyMeta{
						ID:        k.ID,
						GroupID:   k.GroupID,
						Name:      k.Name,
						KeyPrefix: k.KeyPrefix,
						CreatedAt: k.CreatedAt,
					})
				}
				rows = append(rows, exportRow{Group: g, Keys: metas})
			}
			w.Header().Set("Content-Disposition", `attachment; filename="groups-export.json"`)
			writeJSON(w, rows)
		})

		// POST /api/groups/import — NOT implemented (security risk)
		mux.HandleFunc("/api/groups/import", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not implemented", http.StatusNotImplemented)
		})

		// /api/groups/{id}  /api/groups/{id}/keys  /api/groups/{id}/keys/{kid}
		mux.HandleFunc("/api/groups/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if ok && s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			tail := strings.TrimPrefix(r.URL.Path, "/api/groups/")
			parts := strings.SplitN(tail, "/", 3)

			groupID := parts[0]
			if groupID == "" {
				http.NotFound(w, r)
				return
			}

			// /api/groups/{id}
			if len(parts) == 1 {
				if r.Method != http.MethodDelete {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				if err := groups.DeleteGroup(groupID); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("group_deleted", groupID, "")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// /api/groups/{id}/keys[/{kid}]
			if parts[1] != "keys" {
				http.NotFound(w, r)
				return
			}

			if len(parts) == 2 {
				switch r.Method {
				case http.MethodGet:
					writeJSON(w, groups.ListAPIKeys(groupID))

				case http.MethodPost:
					var body struct {
						Name string `json:"name"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, "invalid JSON", http.StatusBadRequest)
						return
					}
					plaintext, key, err := groups.CreateAPIKey(groupID, body.Name)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					if deps.Audit != nil {
						deps.Audit.Log("key_created", key.ID, fmt.Sprintf("group=%s name=%s", groupID, key.Name))
					}
					w.WriteHeader(http.StatusCreated)
					writeJSON(w, map[string]any{
						"key":    key,
						"secret": plaintext, // shown exactly once
					})

				default:
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				}
				return
			}

			// DELETE /api/groups/{id}/keys/{kid}
			keyID := parts[2]
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := groups.RevokeAPIKey(groupID, keyID); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("key_revoked", keyID, fmt.Sprintf("group=%s", groupID))
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	// PMG Update APIs — admin only; active when Updates is wired.
	if deps.Updates != nil {
		updates := deps.Updates

		mux.HandleFunc("/api/config/pmg-update", func(w http.ResponseWriter, r *http.Request) {
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
			writeJSON(w, updates.GetConfig())
		})

		mux.HandleFunc("/api/config/pmg-update/upload", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			goos := r.URL.Query().Get("os")
			arch := r.URL.Query().Get("arch")
			validOS := map[string]bool{"linux": true, "darwin": true, "windows": true}
			validArch := map[string]bool{"amd64": true, "arm64": true}
			if !validOS[goos] || !validArch[arch] {
				http.Error(w, "invalid os or arch", http.StatusBadRequest)
				return
			}
			if err := r.ParseMultipartForm(64 << 20); err != nil {
				http.Error(w, "file too large or invalid form", http.StatusBadRequest)
				return
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing file field", http.StatusBadRequest)
				return
			}
			defer f.Close()
			if mkErr := os.MkdirAll(updates.BinariesDir(), 0o755); mkErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			dst := updates.BinaryPath(goos, arch)
			tmp := dst + ".tmp"
			outf, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h := sha256.New()
			n, err := io.Copy(io.MultiWriter(outf, h), f)
			outf.Close()
			if err != nil {
				os.Remove(tmp)
				http.Error(w, "write failed", http.StatusInternalServerError)
				return
			}
			if err := os.Rename(tmp, dst); err != nil {
				os.Remove(tmp)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			sum := fmt.Sprintf("%x", h.Sum(nil))
			if err := updates.StoreBinaryMeta(goos, arch, BinaryMeta{SHA256: sum, Size: n}); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("pmg_binary_uploaded", goos+"/"+arch, fmt.Sprintf("sha256=%s size=%d", sum, n))
			}
			writeJSON(w, map[string]any{"sha256": sum, "size": n})
		})

		mux.HandleFunc("/api/config/pmg-update/publish", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var body struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version == "" {
				http.Error(w, "version required", http.StatusBadRequest)
				return
			}
			if len(body.Version) > 64 {
				http.Error(w, "version too long", http.StatusBadRequest)
				return
			}
			if err := updates.SetTargetVersion(body.Version); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("pmg_update_published", body.Version, "")
			}
			writeJSON(w, map[string]bool{"ok": true})
		})

		// POST /api/config/pmg-update/scan — scan binaries dir, register found files (admin)
		mux.HandleFunc("/api/config/pmg-update/scan", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			results, err := updates.ScanBinaries()
			if err != nil {
				http.Error(w, "scan failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("pmg_binaries_scanned", fmt.Sprintf("%d found", len(results)), "")
			}
			writeJSON(w, map[string]any{"scanned": len(results), "results": results})
		})

		// POST /api/config/pmg-update/fetch — fetch latest release from GitHub (admin)
		mux.HandleFunc("/api/config/pmg-update/fetch", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var body struct {
				Repo string `json:"repo"`
			}
			body.Repo = "am6539/pmg"
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Repo == "" {
				body.Repo = "am6539/pmg"
			}
			fetchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
			defer cancel()
			result, err := updates.FetchFromGitHub(fetchCtx, body.Repo)
			if err != nil {
				http.Error(w, "fetch failed: "+err.Error(), http.StatusBadGateway)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("pmg_binaries_fetched", result.Version, fmt.Sprintf("%d platforms", len(result.Results)))
			}
			writeJSON(w, result)
		})

		mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if deps.Groups != nil {
				apiKey := r.Header.Get("Authorization")
				if apiKey == "" {
					http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
					return
				}
				if _, _, ok := deps.Groups.ResolveKeyWithID(apiKey); !ok {
					http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
					return
				}
			}
			// filename: pmg-{os}-{arch} or pmg-{os}-{arch}.exe
			filename := strings.TrimPrefix(r.URL.Path, "/download/")
			filename = strings.TrimSuffix(filename, ".exe")
			parts := strings.SplitN(strings.TrimPrefix(filename, "pmg-"), "-", 2)
			if len(parts) != 2 {
				http.NotFound(w, r)
				return
			}
			binPath := updates.BinaryPath(parts[0], parts[1])
			binFile, err := os.Open(binPath)
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			defer binFile.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="pmg"`)
			http.ServeContent(w, r, "", time.Time{}, binFile)
		})
	}

	// PMG org policy APIs — admin only.
	if deps.Policy != nil {
		policy := deps.Policy

		mux.HandleFunc("/api/config/policy", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, policy.Get())
			case http.MethodPost:
				var body struct {
					List string     `json:"list"`
					Rule PolicyRule `json:"rule"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				if err := policy.AddRule(body.List, body.Rule); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("policy_rule_added", body.Rule.Name, fmt.Sprintf("list=%s eco=%s ver=%s", body.List, body.Rule.Ecosystem, body.Rule.Version))
				}
				writeJSON(w, policy.Get())
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// DELETE /api/config/policy/{list}/{id}
		mux.HandleFunc("/api/config/policy/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/config/policy/"), "/", 2)
			if len(parts) != 2 || parts[1] == "" {
				http.NotFound(w, r)
				return
			}
			if err := policy.RemoveRule(parts[0], parts[1]); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("policy_rule_removed", parts[1], "list="+parts[0])
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	// Enrollment APIs — always active when Enrollment is wired.
	if deps.Enrollment != nil {
		enrollment := deps.Enrollment
		enrollRL := newIPRateLimiter() // tighter: 5 req/min per IP

		// GET /bin/{os}/{arch}/pmg — public endpoint, serve PMG binaries for offline installation
		mux.HandleFunc("/bin/", func(w http.ResponseWriter, r *http.Request) {
			// Parse: /bin/linux/amd64/pmg → os=linux, arch=amd64
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bin/"), "/")
			if len(parts) < 3 {
				http.Error(w, "invalid path format, expected /bin/{os}/{arch}/pmg", http.StatusBadRequest)
				return
			}

			goos, goarch := parts[0], parts[1]

			// Validate platform
			validPlatforms := map[string]bool{
				"linux/amd64":   true,
				"linux/arm64":   true,
				"darwin/amd64":  true,
				"darwin/arm64":  true,
				"windows/amd64": true,
			}
			platform := goos + "/" + goarch
			if !validPlatforms[platform] {
				http.Error(w, fmt.Sprintf("unsupported platform: %s", platform), http.StatusNotFound)
				return
			}

			// Check if UpdateStore is available
			if deps.Updates == nil {
				http.Error(w, "binary distribution not enabled on this server", http.StatusServiceUnavailable)
				return
			}

			// Get binary path
			binPath := deps.Updates.BinaryPath(goos, goarch)

			// Check if binary exists
			if _, err := os.Stat(binPath); os.IsNotExist(err) {
				http.Error(w, fmt.Sprintf("binary not available for %s/%s - admin must fetch binaries first", goos, goarch), http.StatusNotFound)
				return
			}

			// Serve binary
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", "attachment; filename=pmg")
			http.ServeFile(w, r, binPath)
		})

		// GET /install.sh — unauthenticated, serves dynamic install script (Linux/macOS)
		mux.HandleFunc("/install.sh", func(w http.ResponseWriter, r *http.Request) {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			serverURL := scheme + "://" + r.Host
			script := strings.ReplaceAll(installScriptTemplate, "{{SERVER_URL}}", serverURL)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, script)
		})

		// GET /install.ps1 — unauthenticated, serves dynamic PowerShell install script (Windows)
		mux.HandleFunc("/install.ps1", func(w http.ResponseWriter, r *http.Request) {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			serverURL := scheme + "://" + r.Host
			script := strings.ReplaceAll(installScriptTemplatePS1, "{{SERVER_URL}}", serverURL)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, script)
		})

		// POST /api/enroll — unauthenticated, agent registration
		mux.HandleFunc("/api/enroll", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			ip := realIP(r)
			if !enrollRL.Allow(ip) {
				http.Error(w, `{"error":"too many enrollment attempts, try again later"}`, http.StatusTooManyRequests)
				return
			}
			var req struct {
				Token      string `json:"token"`
				Hostname   string `json:"hostname"`
				OS         string `json:"os"`
				Arch       string `json:"arch"`
				PMGVersion string `json:"pmg_version"`
				LocalIP    string `json:"local_ip"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.Token == "" || req.Hostname == "" {
				http.Error(w, "invalid request: token and hostname are required", http.StatusBadRequest)
				return
			}
			if len(req.Token) > 128 || len(req.Hostname) > 253 || len(req.OS) > 64 || len(req.Arch) > 32 || len(req.PMGVersion) > 128 || len(req.LocalIP) > 45 {
				http.Error(w, "invalid request: field too long", http.StatusBadRequest)
				return
			}
			tok, err := enrollment.ValidateAndConsume(req.Token)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			// Determine group: use token's group or find/create "Enrolled Agents"
			groupID := tok.GroupID
			if groupID == "" && deps.Groups != nil {
				const autoGroupName = "Enrolled Agents"
				for _, g := range deps.Groups.ListGroups() {
					if g.Name == autoGroupName {
						groupID = g.ID
						break
					}
				}
				if groupID == "" {
					g, gErr := deps.Groups.CreateGroup(autoGroupName)
					if gErr != nil {
						http.Error(w, "internal error", http.StatusInternalServerError)
						return
					}
					groupID = g.ID
				}
			}

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

			// Determine endpoint reported to the enrolling agent.
			// Priority:
			//   1. Dashboard-configured public endpoint (admin sets once via Settings UI)
			//   2. CLI flag / env var (--grpc-public-addr / PMG_CLOUD_GRPC_PUBLIC_ADDR)
			//   3. Auto-detect from request host (fallback, replaces port with :8443)
			endpoint := ""
			insecure := deps.GRPCInsecure

			if deps.Config != nil {
				if cfg := deps.Config.Get(); cfg.PublicEndpoint != "" {
					endpoint = cfg.PublicEndpoint
					insecure = !cfg.AgentUseTLS
				}
			}
			if endpoint == "" {
				endpoint = deps.GRPCAddr
			}
			if endpoint == "" {
				host := r.Host
				if idx := strings.LastIndex(host, ":"); idx != -1 {
					host = host[:idx]
				}
				endpoint = host + ":8443"
			}

			writeJSON(w, map[string]any{
				"api_key":  plainKey,
				"endpoint": endpoint,
				"insecure": insecure,
				"group_id": groupID,
				"agent_id": agentID,
			})
		})

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

		// GET /api/agents — admin and editor only
		mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin && s.Role != RoleEditor {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, enrollment.ListAgents())
		})

		// PUT /api/agents/{id} — editor and admin (editor can only edit label)
		// DELETE /api/agents/{id} — admin only
		mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin && s.Role != RoleEditor {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			agentID := strings.TrimPrefix(r.URL.Path, "/api/agents/")
			if agentID == "" {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodPut:
				var body struct {
					GroupID     *string `json:"group_id"`
					RemoveGroup bool    `json:"remove_group"`
					Label       *string `json:"label"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				// Editor can only edit label, not group
				if s.Role == RoleEditor {
					if (body.GroupID != nil && *body.GroupID != "") || body.RemoveGroup {
						http.Error(w, `{"error":"editors cannot change agent groups"}`, http.StatusForbidden)
						return
					}
				}
				// Group and label are independent: apply only the fields present
				// so a label edit does not clear the group and vice versa.
				if body.GroupID != nil || body.RemoveGroup {
					newGroup := ""
					if body.GroupID != nil {
						newGroup = *body.GroupID
					}
					if body.RemoveGroup {
						newGroup = ""
					}
					if err := enrollment.AssignAgentGroup(agentID, newGroup); err != nil {
						http.Error(w, err.Error(), http.StatusNotFound)
						return
					}
					if deps.Audit != nil {
						deps.Audit.Log("agent_group_assigned", agentID, fmt.Sprintf("group=%s", newGroup))
					}
				}
				if body.Label != nil {
					label := strings.TrimSpace(*body.Label)
					if len(label) > 64 {
						http.Error(w, "label too long (max 64)", http.StatusBadRequest)
						return
					}
					if err := enrollment.SetAgentLabel(agentID, label); err != nil {
						http.Error(w, err.Error(), http.StatusNotFound)
						return
					}
					if deps.Audit != nil {
						deps.Audit.Log("agent_label_set", agentID, label)
					}
				}
				writeJSON(w, map[string]bool{"ok": true})
			case http.MethodDelete:
				// Only admin can delete agents
				if s.Role != RoleAdmin {
					http.Error(w, `{"error":"only admins can delete agents"}`, http.StatusForbidden)
					return
				}
				if err := enrollment.RemoveAgent(agentID); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("agent_removed", agentID, "")
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// GET /api/enrollment-tokens — admin only
		// POST /api/enrollment-tokens — admin only
		mux.HandleFunc("/api/enrollment-tokens", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, enrollment.ListTokens())
			case http.MethodPost:
				var body struct {
					Label    string `json:"label"`
					GroupID  string `json:"group_id"`
					MaxUses  int    `json:"max_uses"`
					TTLHours int    `json:"ttl_hours"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				ttl := time.Duration(body.TTLHours) * time.Hour
				plaintext, tok, err := enrollment.CreateToken(body.Label, body.GroupID, s.Username, body.MaxUses, ttl)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("enrollment_token_created", tok.ID, fmt.Sprintf("label=%s group=%s", tok.Label, tok.GroupID))
				}
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, map[string]any{
					"token":  tok,
					"secret": plaintext, // shown exactly once
				})
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// DELETE /api/enrollment-tokens/{id} — admin only
		mux.HandleFunc("/api/enrollment-tokens/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			tokenID := strings.TrimPrefix(r.URL.Path, "/api/enrollment-tokens/")
			if tokenID == "" {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := enrollment.RevokeToken(tokenID); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if deps.Audit != nil {
				deps.Audit.Log("enrollment_token_revoked", tokenID, "")
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	// --- Auth & user-management endpoints ---
	if deps.Users != nil && deps.Sessions != nil {
		users := deps.Users
		sessions := deps.Sessions
		loginRL := newIPRateLimiter()

		// POST /auth/login — no session required
		mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !loginRL.Allow(realIP(r)) {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"too many login attempts, try again later"}`, http.StatusTooManyRequests)
				return
			}
			var creds struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			u, ok := users.CheckPassword(creds.Username, creds.Password)
			if !ok {
				http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
				return
			}
			sid, err := sessions.Create(u)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     SessionCookieName,
				Value:    sid,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(sessionTTL.Seconds()),
			})
			u.PasswordHash = ""
			if deps.Audit != nil {
				deps.Audit.Log("login", u.Username, "")
			}
			writeJSON(w, u)
		})

		// POST /auth/logout
		mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(SessionCookieName); err == nil {
				sessions.Delete(c.Value)
				if dep := deps.Audit; dep != nil {
					if s, ok := sessions.Get(c.Value); ok {
						dep.Log("logout", s.Username, "")
					}
				}
			}
			http.SetCookie(w, &http.Cookie{
				Name:   SessionCookieName,
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})
			writeJSON(w, map[string]bool{"ok": true})
		})

		// GET /api/me
		mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			u, found := users.FindByID(s.UserID)
			if !found {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			u.PasswordHash = ""
			writeJSON(w, u)
		})

		// POST /api/me/password — change own password
		mux.HandleFunc("/api/me/password", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			var body struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
				http.Error(w, "password required", http.StatusBadRequest)
				return
			}
			if err := users.UpdatePassword(s.UserID, body.Password); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// Invalidate all other sessions so old devices are kicked out.
			if c, err := r.Cookie(SessionCookieName); err == nil {
				sessions.DeleteByUserExcept(s.UserID, c.Value)
			}
			if deps.Audit != nil {
				deps.Audit.Log("password_changed", s.Username, "self")
			}
			writeJSON(w, map[string]bool{"ok": true})
		})

		// GET/POST /api/users — admin only
		mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, users.ListUsers())
			case http.MethodPost:
				var body struct {
					Username string `json:"username"`
					Password string `json:"password"`
					Role     string `json:"role"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				u, err := users.CreateUser(body.Username, body.Password, body.Role)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				u.PasswordHash = ""
				if deps.Audit != nil {
					deps.Audit.Log("user_created", u.Username, fmt.Sprintf("role=%s", u.Role))
				}
				writeJSON(w, u)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// PUT/DELETE /api/users/{id} — admin only
		mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			uid := strings.TrimPrefix(r.URL.Path, "/api/users/")
			if uid == "" {
				http.Error(w, "id required", http.StatusBadRequest)
				return
			}
			switch r.Method {
			case http.MethodPut:
				var body struct {
					Password string `json:"password"`
					Role     string `json:"role"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				if body.Password != "" {
					if err := users.UpdatePassword(uid, body.Password); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					sessions.DeleteByUser(uid)
					if deps.Audit != nil {
						u, _ := users.FindByID(uid)
						deps.Audit.Log("password_changed", u.Username, fmt.Sprintf("by=%s", s.Username))
					}
				}
				if body.Role != "" {
					if err := users.UpdateRole(uid, body.Role); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					if deps.Audit != nil {
						u, _ := users.FindByID(uid)
						deps.Audit.Log("role_changed", u.Username, fmt.Sprintf("role=%s by=%s", body.Role, s.Username))
					}
				}
				u, _ := users.FindByID(uid)
				u.PasswordHash = ""
				writeJSON(w, u)
			case http.MethodDelete:
				if uid == s.UserID {
					http.Error(w, "cannot delete your own account", http.StatusBadRequest)
					return
				}
				u, _ := users.FindByID(uid)
				if err := users.DeleteUser(uid); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if deps.Audit != nil {
					deps.Audit.Log("user_deleted", u.Username, fmt.Sprintf("by=%s", s.Username))
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}

	if deps.Users != nil && deps.Sessions != nil {
		return sessionMiddleware(mux, deps.Sessions)
	}
	return mux
}

// sessionMiddleware protects /api/* routes (except /auth/* and unauthenticated enroll) with session auth.
func sessionMiddleware(h http.Handler, sessions *SessionStore) http.Handler {
	// unauthenticated API routes — session is attached if present but never required
	unauthAPI := map[string]bool{
		"/api/me":        true,
		"/api/enroll":    true,
		"/api/heartbeat": true, // agent API key auth, not session
		"/api/sync":      true, // agent sync, not session
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always pass: static files, auth endpoints, healthz, install.sh
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/") || unauthAPI[path] {
			// Attach session if present (for /api/me etc.) but never block
			if strings.HasPrefix(path, "/api/") {
				if c, err := r.Cookie(SessionCookieName); err == nil {
					if s, ok := sessions.Get(c.Value); ok {
						r = r.WithContext(context.WithValue(r.Context(), ctxSession, s))
					}
				}
			}
			h.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(SessionCookieName)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		s, ok := sessions.Get(c.Value)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxSession, s))
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeEventsCSV(w http.ResponseWriter, events []Event) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="events.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"received_at", "endpoint_id", "tenant_id", "event_type",
		"package_name", "package_version", "ecosystem", "action", "is_malware",
	})
	for _, ev := range events {
		malware := ""
		if ev.IsMalware != nil {
			malware = fmt.Sprintf("%v", *ev.IsMalware)
		}
		_ = cw.Write([]string{
			ev.ReceivedAt.UTC().Format(time.RFC3339),
			ev.EndpointID,
			ev.TenantID,
			ev.EventType,
			ev.PackageName,
			ev.PackageVersion,
			ev.Ecosystem,
			ev.Action,
			malware,
		})
	}
	cw.Flush()
}

func writeEndpointsCSV(w http.ResponseWriter, list []EndpointInfo) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="endpoints.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"endpoint_id", "hostname", "os", "arch",
		"last_seen", "sessions", "tool_version", "total_packages", "blocked_packages",
	})
	for _, ep := range list {
		_ = cw.Write([]string{
			ep.EndpointID,
			ep.Hostname,
			ep.OS,
			ep.Arch,
			ep.LastSeen.UTC().Format(time.RFC3339),
			strconv.Itoa(ep.Sessions),
			ep.ToolVersion,
			strconv.Itoa(ep.TotalPackages),
			strconv.Itoa(ep.BlockedPackages),
		})
	}
	cw.Flush()
}

// parseDateRange parses from/to query params (YYYY-MM-DD). Empty strings default
// to a wide range (epoch to now+1day).
func parseDateRange(fromStr, toStr string) (time.Time, time.Time, error) {
	from := time.Time{}
	to := time.Now().UTC().Add(24 * time.Hour)

	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			return from, to, fmt.Errorf("invalid from date: %w", err)
		}
		from = t.UTC()
	}
	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			return from, to, fmt.Errorf("invalid to date: %w", err)
		}
		// include the entire to-day
		to = t.UTC().Add(24*time.Hour - time.Nanosecond)
	}
	return from, to, nil
}

func splitComma(s string) map[string]bool {
	m := make(map[string]bool)
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

func parseDays(r *http.Request, def int) int {
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 0 {
			if n > 365 {
				n = 365
			}
			return n
		}
	}
	return def
}

func filterByGroup(events []Event, groupID string) []Event {
	if groupID == "" {
		return events
	}
	out := make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.GroupID == groupID {
			out = append(out, ev)
		}
	}
	return out
}
