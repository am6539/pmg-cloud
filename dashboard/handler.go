package dashboard

import (
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

//go:embed static
var staticFiles embed.FS

// HandlerDeps bundles optional dependencies for the dashboard handler.
type HandlerDeps struct {
	Mirror  *MalwareMirror
	Groups  *GroupStore      // may be nil
	Config  *ConfigStore     // may be nil
	Audit   *AuditLog        // may be nil
	Webhook *WebhookDelivery // may be nil
}

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

	return mux
}

// HealthzHandler returns a minimal health-check handler suitable for use
// outside any auth middleware (load balancers, Docker HEALTHCHECK, etc.).
func HealthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
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
