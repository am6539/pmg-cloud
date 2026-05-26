package dashboard

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

//go:embed static
var staticFiles embed.FS

// Handler returns an http.Handler for the dashboard.
// dataDir is the path to the JSONL event directory.
// mirror serves the Aikido malware feeds for air-gapped PMG agents.
// groups manages group/API-key lifecycle; may be nil (groups features disabled).
func Handler(dataDir string, mirror *MalwareMirror, groups *GroupStore) http.Handler {
	reader := NewReader(dataDir)
	mux := http.NewServeMux()

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

	// API: recent events list with optional filter
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		days := parseDays(r, 30)
		events, err := reader.LoadEvents(days)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = filterByGroup(events, r.URL.Query().Get("group_id"))
		// optional filter by event_type
		if et := r.URL.Query().Get("event_type"); et != "" {
			filtered := events[:0]
			for _, ev := range events {
				if ev.EventType == et {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}
		// optional filter by action (comma-separated: BLOCKED,COOLDOWN_BLOCKED)
		if a := r.URL.Query().Get("action"); a != "" {
			actions := splitComma(a)
			filtered := events[:0]
			for _, ev := range events {
				if actions[ev.Action] {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}
		// optional filter malware=true|false
		if m := r.URL.Query().Get("malware"); m != "" {
			want := m == "true"
			filtered := events[:0]
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
		// limit
		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if len(events) > limit {
			events = events[:limit]
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

	// API: endpoints
	mux.HandleFunc("/api/endpoints", func(w http.ResponseWriter, r *http.Request) {
		events, err := reader.LoadEvents(0) // all time
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		events = filterByGroup(events, r.URL.Query().Get("group_id"))
		writeJSON(w, EndpointList(events))
	})

	// Aikido malware feed mirror — paths match malware-list.aikido.dev so agents
	// need only change base_url, not the path.
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
		writeJSON(w, mirror.Status())
	})

	// Group management APIs — only registered when groups feature is enabled.
	if groups != nil {
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
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, g)

			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// DELETE /api/groups/{id}
		mux.HandleFunc("/api/groups/", func(w http.ResponseWriter, r *http.Request) {
			// path: /api/groups/{id}  or  /api/groups/{id}/keys  or  /api/groups/{id}/keys/{kid}
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
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// /api/groups/{id}/keys[/{kid}]
			if parts[1] != "keys" {
				http.NotFound(w, r)
				return
			}

			if len(parts) == 2 {
				// GET /api/groups/{id}/keys  or  POST /api/groups/{id}/keys
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
	json.NewEncoder(w).Encode(v)
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
			return n
		}
	}
	return def
}

func filterByGroup(events []Event, groupID string) []Event {
	if groupID == "" {
		return events
	}
	out := events[:0]
	for _, ev := range events {
		if ev.GroupID == groupID {
			out = append(out, ev)
		}
	}
	return out
}
