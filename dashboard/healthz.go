package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type healthComponent struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type healthResult struct {
	OK         bool                       `json:"ok"`
	Uptime     string                     `json:"uptime"`
	Components map[string]healthComponent `json:"components"`
}

// HealthzHandler is an unauthenticated liveness+readiness probe.
// Checks data dir writability and malware feed freshness.
func HealthzHandler(dataDir string, mirror *MalwareMirror) http.Handler {
	start := time.Now()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		components := map[string]healthComponent{}
		allOK := true

		// Data dir writability
		probe := filepath.Join(dataDir, ".healthcheck")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			components["data_dir"] = healthComponent{Status: "error", Detail: err.Error()}
			allOK = false
		} else {
			_ = os.Remove(probe)
			components["data_dir"] = healthComponent{Status: "ok"}
		}

		// Malware feed freshness
		if mirror != nil {
			st := mirror.Status()
			switch {
			case !st.NPM.OK || !st.PyPI.OK:
				components["malware_feed"] = healthComponent{Status: "error", Detail: "one or more feeds failed"}
				allOK = false
			case st.NPM.LastUpdated == nil:
				components["malware_feed"] = healthComponent{Status: "unknown", Detail: "not yet fetched"}
			case time.Since(*st.NPM.LastUpdated) > 25*time.Hour:
				components["malware_feed"] = healthComponent{
					Status: "stale",
					Detail: fmt.Sprintf("last updated %s", st.NPM.LastUpdated.Format(time.RFC3339)),
				}
			default:
				components["malware_feed"] = healthComponent{
					Status: "ok",
					Detail: fmt.Sprintf("npm=%d pypi=%d entries", st.NPM.EntryCount, st.PyPI.EntryCount),
				}
			}
		}

		res := healthResult{
			OK:         allOK,
			Uptime:     time.Since(start).Truncate(time.Second).String(),
			Components: components,
		}

		w.Header().Set("Content-Type", "application/json")
		if !allOK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(res)
	})
}
