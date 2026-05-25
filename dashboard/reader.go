package dashboard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Event mirrors the on-disk storedEvent JSON structure.
type Event struct {
	ReceivedAt   time.Time  `json:"received_at"`
	TenantID     string     `json:"tenant_id"`
	EventID      string     `json:"event_id"`
	InvocationID string     `json:"invocation_id"`
	ToolName     string     `json:"tool_name"`
	ToolVersion  string     `json:"tool_version"`
	EventTime    *time.Time `json:"event_time"`
	EndpointID   string     `json:"endpoint_id"`
	MachineID    string     `json:"machine_id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Arch         string     `json:"arch"`
	EventType    string     `json:"event_type"`
	// package_decision
	Ecosystem      string `json:"ecosystem"`
	PackageName    string `json:"package_name"`
	PackageVersion string `json:"package_version"`
	Action         string `json:"action"`
	IsMalware      *bool  `json:"is_malware"`
	IsVerified     *bool  `json:"is_verified"`
	AnalysisID     string `json:"analysis_id"`
	// session_summary
	PackageManager       string `json:"package_manager"`
	FlowType             string `json:"flow_type"`
	Outcome              string `json:"outcome"`
	TotalAnalyzed        uint32 `json:"total_analyzed"`
	AllowedCount         uint32 `json:"allowed_count"`
	BlockedCount         uint32 `json:"blocked_count"`
	ConfirmedCount       uint32 `json:"confirmed_count"`
	TrustedSkipped       uint32 `json:"trusted_skipped"`
	CooldownBlockedCount uint32 `json:"cooldown_blocked_count"`
	DurationMs           int64  `json:"duration_ms"`
	SandboxEnabled       *bool  `json:"sandbox_enabled"`
	ParanoidMode         *bool  `json:"paranoid_mode"`
	TransitiveEnabled    *bool  `json:"transitive_enabled"`
}

// Stats holds aggregated dashboard metrics.
type Stats struct {
	Endpoints          int            `json:"endpoints"`
	Sessions           int            `json:"sessions"`
	PackagesAnalyzed   uint64         `json:"packages_analyzed"`
	MaliciousPackages  int            `json:"malicious_packages"`
	BlockedPackages    int            `json:"blocked_packages"`
	SuspiciousPackages int            `json:"suspicious_packages"`
	ByEcosystem        map[string]int `json:"by_ecosystem"`
	ByOutcome          map[string]int `json:"by_outcome"`
	RecentEvents       []Event        `json:"recent_events"`
}

// EndpointInfo represents a unique endpoint seen in events.
type EndpointInfo struct {
	EndpointID string    `json:"endpoint_id"`
	MachineID  string    `json:"machine_id"`
	Hostname   string    `json:"hostname"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	LastSeen   time.Time `json:"last_seen"`
	Sessions   int       `json:"sessions"`
}

// Reader reads events from JSONL files in a directory.
type Reader struct {
	dataDir string
}

func NewReader(dataDir string) *Reader {
	return &Reader{dataDir: dataDir}
}

// LoadEvents reads all events from JSONL files within the last `days` days.
// Pass days=0 to read all files.
func (r *Reader) LoadEvents(days int) ([]Event, error) {
	files, err := filepath.Glob(filepath.Join(r.dataDir, "events-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files) // chronological order

	cutoff := time.Time{}
	if days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}

	var events []Event
	for _, f := range files {
		// parse date from filename: events-YYYYMMDD.jsonl
		base := filepath.Base(f)
		dateStr := strings.TrimPrefix(strings.TrimSuffix(base, ".jsonl"), "events-")
		if days > 0 {
			fileDate, err := time.Parse("20060102", dateStr)
			if err == nil && fileDate.Before(cutoff) {
				continue
			}
		}

		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(fh)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB per line
		for scanner.Scan() {
			var ev Event
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
				events = append(events, ev)
			}
		}
		fh.Close()
	}
	return events, nil
}

// Aggregate computes Stats from a slice of events.
func Aggregate(events []Event) Stats {
	endpoints := make(map[string]*EndpointInfo)
	byEcosystem := make(map[string]int)
	byOutcome := make(map[string]int)
	var sessions int
	var packagesAnalyzed uint64
	var malicious, blocked, suspicious int

	for _, ev := range events {
		// track endpoints
		if ev.EndpointID != "" {
			ep, ok := endpoints[ev.EndpointID]
			if !ok {
				ep = &EndpointInfo{
					EndpointID: ev.EndpointID,
					MachineID:  ev.MachineID,
					Hostname:   ev.Hostname,
					OS:         ev.OS,
					Arch:       ev.Arch,
				}
				endpoints[ev.EndpointID] = ep
			}
			if ev.ReceivedAt.After(ep.LastSeen) {
				ep.LastSeen = ev.ReceivedAt
			}
		}

		switch ev.EventType {
		case "SESSION_SUMMARY":
			sessions++
			packagesAnalyzed += uint64(ev.TotalAnalyzed)
			if ev.Outcome != "" {
				byOutcome[ev.Outcome]++
			}
			if ep, ok := endpoints[ev.EndpointID]; ok {
				ep.Sessions++
			}

		case "PACKAGE_DECISION":
			if ev.Ecosystem != "" {
				byEcosystem[ev.Ecosystem]++
			}
			if ev.IsMalware != nil && *ev.IsMalware {
				malicious++
			}
			if ev.Action == "BLOCKED" || ev.Action == "COOLDOWN_BLOCKED" {
				blocked++
				if ev.IsMalware == nil || !*ev.IsMalware {
					suspicious++ // blocked but not confirmed malware = suspicious
				}
			}
		}
	}

	// recent events: last 50, newest first
	recent := make([]Event, len(events))
	copy(recent, events)
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].ReceivedAt.After(recent[j].ReceivedAt)
	})
	if len(recent) > 50 {
		recent = recent[:50]
	}

	return Stats{
		Endpoints:          len(endpoints),
		Sessions:           sessions,
		PackagesAnalyzed:   packagesAnalyzed,
		MaliciousPackages:  malicious,
		BlockedPackages:    blocked,
		SuspiciousPackages: suspicious,
		ByEcosystem:        byEcosystem,
		ByOutcome:          byOutcome,
		RecentEvents:       recent,
	}
}

// EndpointList returns deduplicated endpoint info sorted by last seen descending.
func EndpointList(events []Event) []EndpointInfo {
	m := make(map[string]*EndpointInfo)
	for _, ev := range events {
		if ev.EndpointID == "" {
			continue
		}
		ep, ok := m[ev.EndpointID]
		if !ok {
			ep = &EndpointInfo{
				EndpointID: ev.EndpointID,
				MachineID:  ev.MachineID,
				Hostname:   ev.Hostname,
				OS:         ev.OS,
				Arch:       ev.Arch,
			}
			m[ev.EndpointID] = ep
		}
		if ev.ReceivedAt.After(ep.LastSeen) {
			ep.LastSeen = ev.ReceivedAt
		}
		if ev.EventType == "SESSION_SUMMARY" {
			ep.Sessions++
		}
	}
	list := make([]EndpointInfo, 0, len(m))
	for _, ep := range m {
		list = append(list, *ep)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastSeen.After(list[j].LastSeen)
	})
	return list
}
