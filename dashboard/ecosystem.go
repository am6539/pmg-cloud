package dashboard

import (
	"fmt"
	"log/slog"
	"time"
)

// EcosystemFinding is a single malicious package found on an agent's disk
// during a machine-wide ecosystem scan, as reported by POST /api/scan-report.
type EcosystemFinding struct {
	Ecosystem    string   `json:"ecosystem"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Verdict      string   `json:"verdict"`
	ReferenceURL string   `json:"reference_url,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	RemoveHint   string   `json:"remove_hint,omitempty"`
}

// EcosystemPackage is a clean (non-malicious) package found on an agent's disk
// during a machine-wide ecosystem scan, as reported by POST /api/scan-report.
type EcosystemPackage struct {
	Ecosystem string   `json:"ecosystem"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Paths     []string `json:"paths,omitempty"`
}

// EcosystemScanSummary carries aggregate counters for one completed ecosystem scan.
type EcosystemScanSummary struct {
	TotalPathsScanned  int     `json:"total_paths_scanned"`
	UniquePackages     int     `json:"unique_packages"`
	FlaggedCount       int     `json:"flagged_count"`
	SkippedDirs        int     `json:"skipped_dirs"`
	SkippedCloudChecks int     `json:"skipped_cloud_checks"`
	DurationSeconds    float64 `json:"duration_seconds"`
}

// EcosystemFindingView is a single finding enriched with the identity of the
// agent it was found on, for the dashboard's fleet-wide findings table.
type EcosystemFindingView struct {
	AgentID      string     `json:"agent_id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Ecosystem    string     `json:"ecosystem"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Verdict      string     `json:"verdict"`
	ReferenceURL string     `json:"reference_url,omitempty"`
	Paths        []string   `json:"paths,omitempty"`
	RemoveHint   string     `json:"remove_hint,omitempty"`
	DetectedAt   *time.Time `json:"detected_at,omitempty"`
}

// EcosystemFleetSummary is the aggregate counts shown as dashboard summary cards.
type EcosystemFleetSummary struct {
	AgentsScanned  int        `json:"agents_scanned"`
	TotalFindings  int        `json:"total_findings"`
	TotalPackages  int        `json:"total_packages"`
	LastScanAt     *time.Time `json:"last_scan_at,omitempty"`
}

// RequestScan flags the agent identified by agentID for a scan on its next
// heartbeat poll.
func (es *EnrollmentStore) RequestScan(agentID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == agentID {
			es.data.Agents[i].ScanRequested = true
			es.data.Agents[i].ScanState = "pending"
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}

// ConsumeScanRequest checks whether the agent identified by keyID has a
// pending scan request. If so, it clears the flag (fire-once — the caller is
// responsible for delivering this to the agent in the same response) and
// marks the agent "dispatched". Returns whether a scan was dispatched.
func (es *EnrollmentStore) ConsumeScanRequest(keyID string) bool {
	if keyID == "" {
		return false
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID && a.ScanRequested {
			es.data.Agents[i].ScanRequested = false
			es.data.Agents[i].ScanState = "dispatched"
			now := time.Now().UTC()
			es.data.Agents[i].ScanDispatchedAt = &now
			if err := es.save(); err != nil {
				slog.Warn("failed to persist scan dispatch state", "api_key_id", keyID, "err", err)
			}
			return true
		}
	}
	return false
}

// RecordScanStarted marks the agent identified by keyID as actively running a
// scan. A miss (no matching agent) is non-fatal, mirroring TouchAgentByAPIKeyID.
func (es *EnrollmentStore) RecordScanStarted(keyID string) error {
	if keyID == "" {
		return nil
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID {
			es.data.Agents[i].ScanState = "running"
			return es.save()
		}
	}
	return nil
}

// RecordScanCompleted stores findings, clean packages, and the scan summary for the agent
// identified by keyID, replacing any findings from a previous scan.
func (es *EnrollmentStore) RecordScanCompleted(keyID string, findings []EcosystemFinding, cleanPackages []EcosystemPackage, summary EcosystemScanSummary) error {
	if keyID == "" {
		return nil
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	now := time.Now().UTC()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID {
			es.data.Agents[i].ScanState = "completed"
			es.data.Agents[i].LastScanAt = &now
			es.data.Agents[i].LastScanSummary = &summary
			es.data.Agents[i].Findings = findings
			es.data.Agents[i].CleanPackages = cleanPackages
			return es.save()
		}
	}
	return nil
}

// ListEcosystemFindings returns every finding and clean package across all
// non-removed agents, enriched with agent identity, for the dashboard's
// fleet-wide findings table. Clean packages have Verdict set to "clean".
func (es *EnrollmentStore) ListEcosystemFindings() []EcosystemFindingView {
	es.mu.RLock()
	defer es.mu.RUnlock()
	var out []EcosystemFindingView
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		for _, f := range a.Findings {
			out = append(out, EcosystemFindingView{
				AgentID:      a.ID,
				Hostname:     a.Hostname,
				OS:           a.OS,
				Ecosystem:    f.Ecosystem,
				Name:         f.Name,
				Version:      f.Version,
				Verdict:      f.Verdict,
				ReferenceURL: f.ReferenceURL,
				Paths:        f.Paths,
				RemoveHint:   f.RemoveHint,
				DetectedAt:   a.LastScanAt,
			})
		}
		for _, p := range a.CleanPackages {
			out = append(out, EcosystemFindingView{
				AgentID:    a.ID,
				Hostname:   a.Hostname,
				OS:         a.OS,
				Ecosystem:  p.Ecosystem,
				Name:       p.Name,
				Version:    p.Version,
				Verdict:    "clean",
				Paths:      p.Paths,
				DetectedAt: a.LastScanAt,
			})
		}
	}
	return out
}

// EcosystemFleetSummaryStats returns fleet-wide aggregate counts for the
// dashboard's Ecosystem tab summary cards.
func (es *EnrollmentStore) EcosystemFleetSummaryStats() EcosystemFleetSummary {
	es.mu.RLock()
	defer es.mu.RUnlock()
	var summary EcosystemFleetSummary
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		if a.LastScanAt != nil {
			summary.AgentsScanned++
			if summary.LastScanAt == nil || a.LastScanAt.After(*summary.LastScanAt) {
				summary.LastScanAt = a.LastScanAt
			}
		}
		summary.TotalFindings += len(a.Findings)
		summary.TotalPackages += len(a.Findings) + len(a.CleanPackages)
	}
	return summary
}
