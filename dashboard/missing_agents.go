package dashboard

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// findMissingAgents returns agents whose last heartbeat is older than threshold.
// Agents that have never checked in (LastSeen == nil) are excluded.
func findMissingAgents(agents []Agent, now time.Time, threshold time.Duration) []Agent {
	var out []Agent
	for _, a := range agents {
		if a.LastSeen == nil {
			continue
		}
		if now.Sub(*a.LastSeen) >= threshold {
			out = append(out, a)
		}
	}
	return out
}

// MissingAgentMonitor periodically alerts when agents stop sending heartbeats.
type MissingAgentMonitor struct {
	enrollment *EnrollmentStore
	config     *ConfigStore
	threshold  time.Duration
	interval   time.Duration

	mu      sync.Mutex
	alerted map[string]bool // agent ID → already alerted (de-dupe)
}

// NewMissingAgentMonitor builds a monitor. threshold matches the dashboard
// "Missing PMG" cutoff (72h); interval is how often the sweep runs.
func NewMissingAgentMonitor(enrollment *EnrollmentStore, config *ConfigStore) *MissingAgentMonitor {
	return &MissingAgentMonitor{
		enrollment: enrollment,
		config:     config,
		threshold:  72 * time.Hour,
		interval:   1 * time.Hour,
		alerted:    map[string]bool{},
	}
}

// Run blocks, sweeping every interval until stop is closed. Call as a goroutine.
func (m *MissingAgentMonitor) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *MissingAgentMonitor) sweep() {
	if m.enrollment == nil || m.config == nil {
		return
	}
	missing := findMissingAgents(m.enrollment.ListAgents(), time.Now().UTC(), m.threshold)
	missingIDs := map[string]bool{}
	for _, a := range missing {
		missingIDs[a.ID] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Clear alert state for agents that came back, so they can re-alert later.
	for id := range m.alerted {
		if !missingIDs[id] {
			delete(m.alerted, id)
		}
	}
	for _, a := range missing {
		if m.alerted[a.ID] {
			continue // already alerted this outage
		}
		m.alerted[a.ID] = true
		msg := fmt.Sprintf("⚠️ PMG agent missing: %s has not sent a heartbeat in over %dh — PMG may have been removed", a.Hostname, int(m.threshold.Hours()))
		for _, ch := range m.config.Get().AlertChannels {
			if !ch.Enabled || !ch.OnMissing {
				continue
			}
			go func(c AlertChannel, text string) {
				if err := sendAlert(c, text); err != nil {
					slog.Warn("missing-agent alert failed", "kind", c.Kind, "err", err)
				}
			}(ch, msg)
		}
	}
}
