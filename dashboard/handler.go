package dashboard

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
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
	Mirror     *MalwareMirror
	Groups     *GroupStore      // may be nil
	Config     *ConfigStore     // may be nil
	Audit      *AuditLog        // may be nil
	Webhook    *WebhookDelivery // may be nil
	Users      *UserStore       // may be nil; enables session-based auth
	Sessions   *SessionStore    // required when Users is set
	Enrollment *EnrollmentStore // may be nil; enables agent enrollment
	GRPCAddr   string           // gRPC endpoint reported to enrolling agents
}

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

# Install PMG binary
echo "Installing PMG..."
curl -fsSL https://raw.githubusercontent.com/am6539/pmg/main/install.sh | sh

# Enroll with server
echo "Enrolling with PMG Cloud..."
pmg cloud enroll --endpoint="$PMG_SERVER" --token="$PMG_TOKEN"

# Wire PMG into shell (aliases + shims)
echo "Wiring PMG into your shell..."
pmg setup install

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
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ComputePackageStats(events))
	})

	// API: endpoints — exact match (list)
	mux.HandleFunc("/api/endpoints", func(w http.ResponseWriter, r *http.Request) {
		events, err := reader.LoadEvents(0) // all time
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = filterByGroup(events, r.URL.Query().Get("group_id"))
		if r.URL.Query().Get("format") == "csv" {
			writeEndpointsCSV(w, EndpointList(events))
			return
		}
		writeJSON(w, EndpointList(events))
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
		events, err := reader.LoadEvents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var filtered []Event
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
		writeJSON(w, mirror.Status())
	})

	mux.HandleFunc("/api/malware/refresh", func(w http.ResponseWriter, r *http.Request) {
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

	// Config management APIs — only when Config is wired.
	if deps.Config != nil {
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
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
	}

	// Audit log API — only when Audit is wired.
	if deps.Audit != nil {
		mux.HandleFunc("/api/audit", func(w http.ResponseWriter, r *http.Request) {
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

	// Enrollment APIs — always active when Enrollment is wired.
	if deps.Enrollment != nil {
		enrollment := deps.Enrollment
		enrollRL := newIPRateLimiter() // tighter: 5 req/min per IP

		// GET /install.sh — unauthenticated, serves dynamic install script
		mux.HandleFunc("/install.sh", func(w http.ResponseWriter, r *http.Request) {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			serverURL := scheme + "://" + r.Host
			script := strings.ReplaceAll(installScriptTemplate, "{{SERVER_URL}}", serverURL)
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
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
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

			// Determine gRPC endpoint
			endpoint := deps.GRPCAddr
			if endpoint == "" {
				// Derive from request host by replacing port with 8443
				host := r.Host
				if idx := strings.LastIndex(host, ":"); idx != -1 {
					host = host[:idx]
				}
				endpoint = host + ":8443"
			}

			writeJSON(w, map[string]any{
				"api_key":  plainKey,
				"endpoint": endpoint,
				"insecure": false,
				"group_id": groupID,
				"agent_id": agentID,
			})
		})

		// GET /api/agents — admin only
		mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
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
			writeJSON(w, enrollment.ListAgents())
		})

		// PUT/DELETE /api/agents/{id} — admin only
		mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
			s, ok := sessionFromContext(r)
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if s.Role != RoleAdmin {
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
					GroupID     string `json:"group_id"`
					RemoveGroup bool   `json:"remove_group"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				newGroup := body.GroupID
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
				writeJSON(w, map[string]bool{"ok": true})
			case http.MethodDelete:
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
		"/api/me":     true,
		"/api/enroll": true,
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
		"endpoint_id", "machine_id", "hostname", "os", "arch",
		"last_seen", "sessions", "tool_version", "total_packages", "blocked_packages",
	})
	for _, ep := range list {
		_ = cw.Write([]string{
			ep.EndpointID,
			ep.MachineID,
			ep.Hostname,
			ep.OS,
			ep.Arch,
			ep.LastSeen.UTC().Format(time.RFC3339),
			strconv.Itoa(ep.Sessions),
			ep.ToolVersion,
			strconv.FormatUint(ep.TotalPackages, 10),
			strconv.FormatUint(ep.BlockedPackages, 10),
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
