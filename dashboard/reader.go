package dashboard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event mirrors the on-disk storedEvent JSON structure.
type Event struct {
	GroupID      string     `json:"group_id"`
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

// DayBucket holds an event count for a single UTC day.
type DayBucket struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
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
	EventsPerDay       []DayBucket    `json:"events_per_day"`
	RecentEvents       []Event        `json:"recent_events"`
}

// PackageStat holds a name/count pair for top-N rankings.
type PackageStat struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem,omitempty"`
	Count     int    `json:"count"`
}

// PackageStats is returned by GET /api/package-stats.
type PackageStats struct {
	TopPackages   []PackageStat `json:"top_packages"`
	TopEcosystems []PackageStat `json:"top_ecosystems"`
	TopEndpoints  []PackageStat `json:"top_endpoints"`
}

// EndpointInfo represents a unique endpoint seen in events.
type EndpointInfo struct {
	EndpointID        string    `json:"endpoint_id"`
	MachineID         string    `json:"machine_id"`
	Hostname          string    `json:"hostname"`
	OS                string    `json:"os"`
	Arch              string    `json:"arch"`
	LastSeen          time.Time `json:"last_seen"`
	Sessions          int       `json:"sessions"`
	ToolVersion       string    `json:"tool_version"`
	PackageManagers   []string  `json:"package_managers"`
	FlowTypes         []string  `json:"flow_types"`
	TotalPackages     uint64    `json:"total_packages"`
	BlockedPackages   uint64    `json:"blocked_packages"`
	SandboxEnabled    *bool     `json:"sandbox_enabled"`
	ParanoidMode      *bool     `json:"paranoid_mode"`
	TransitiveEnabled *bool     `json:"transitive_enabled"`
}

// Reader reads events from JSONL files in a directory.
// Results are cached for cacheTTL to avoid re-reading on every API request.
type Reader struct {
	dataDir string
	mu      sync.Mutex
	cache   map[int]cachedLoad
}

const cacheTTL = 5 * time.Second

type cachedLoad struct {
	events   []Event
	cachedAt time.Time
}

func NewReader(dataDir string) *Reader {
	return &Reader{dataDir: dataDir, cache: make(map[int]cachedLoad)}
}

// LoadEvents reads all events from JSONL files within the last `days` days.
// Pass days=0 to read all files. Results are cached for 5 seconds.
func (r *Reader) LoadEvents(days int) ([]Event, error) {
	r.mu.Lock()
	if c, ok := r.cache[days]; ok && time.Since(c.cachedAt) < cacheTTL {
		r.mu.Unlock()
		return c.events, nil
	}
	r.mu.Unlock()

	events, err := r.loadFromDisk(days)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cache[days] = cachedLoad{events: events, cachedAt: time.Now()}
	r.mu.Unlock()
	return events, nil
}

func (r *Reader) loadFromDisk(days int) ([]Event, error) {
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

	// events per day
	dayCount := make(map[string]int)
	for _, ev := range events {
		day := ev.ReceivedAt.UTC().Format("2006-01-02")
		dayCount[day]++
	}
	dayKeys := make([]string, 0, len(dayCount))
	for d := range dayCount {
		dayKeys = append(dayKeys, d)
	}
	sort.Strings(dayKeys)
	eventsPerDay := make([]DayBucket, 0, len(dayKeys))
	for _, d := range dayKeys {
		eventsPerDay = append(eventsPerDay, DayBucket{Date: d, Count: dayCount[d]})
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
		EventsPerDay:       eventsPerDay,
		RecentEvents:       recent,
	}
}

// ComputePackageStats returns top-10 packages, ecosystems, and endpoints by event count.
func ComputePackageStats(events []Event) PackageStats {
	pkgMap := make(map[string]PackageStat)
	ecoMap := make(map[string]int)
	epMap  := make(map[string]int)

	for _, ev := range events {
		if ev.EventType != "PACKAGE_DECISION" {
			continue
		}
		if ev.PackageName != "" {
			key := ev.Ecosystem + "|" + ev.PackageName
			s := pkgMap[key]
			s.Name = ev.PackageName
			s.Ecosystem = ev.Ecosystem
			s.Count++
			pkgMap[key] = s
		}
		if ev.Ecosystem != "" {
			ecoMap[ev.Ecosystem]++
		}
		if ev.EndpointID != "" {
			epMap[ev.EndpointID]++
		}
	}

	topN := func(list []PackageStat, n int) []PackageStat {
		sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
		if len(list) > n {
			return list[:n]
		}
		return list
	}

	pkgs := make([]PackageStat, 0, len(pkgMap))
	for _, v := range pkgMap {
		pkgs = append(pkgs, v)
	}
	ecos := make([]PackageStat, 0, len(ecoMap))
	for k, v := range ecoMap {
		ecos = append(ecos, PackageStat{Name: k, Count: v})
	}
	eps := make([]PackageStat, 0, len(epMap))
	for k, v := range epMap {
		eps = append(eps, PackageStat{Name: k, Count: v})
	}

	return PackageStats{
		TopPackages:   topN(pkgs, 10),
		TopEcosystems: topN(ecos, 10),
		TopEndpoints:  topN(eps, 10),
	}
}

// EndpointList returns deduplicated endpoint info sorted by last seen descending.
func EndpointList(events []Event) []EndpointInfo {
	type epState struct {
		info          EndpointInfo
		pkgManagers   map[string]struct{}
		flowTypes     map[string]struct{}
		latestSession time.Time
	}

	m := make(map[string]*epState)

	for _, ev := range events {
		if ev.EndpointID == "" {
			continue
		}
		st, ok := m[ev.EndpointID]
		if !ok {
			st = &epState{
				info: EndpointInfo{
					EndpointID: ev.EndpointID,
					MachineID:  ev.MachineID,
					Hostname:   ev.Hostname,
					OS:         ev.OS,
					Arch:       ev.Arch,
				},
				pkgManagers: make(map[string]struct{}),
				flowTypes:   make(map[string]struct{}),
			}
			m[ev.EndpointID] = st
		}

		if ev.ReceivedAt.After(st.info.LastSeen) {
			st.info.LastSeen = ev.ReceivedAt
			if ev.ToolVersion != "" {
				st.info.ToolVersion = ev.ToolVersion
			}
		}

		if ev.EventType == "SESSION_SUMMARY" {
			st.info.Sessions++
			st.info.TotalPackages += uint64(ev.TotalAnalyzed)
			st.info.BlockedPackages += uint64(ev.BlockedCount)
			if ev.PackageManager != "" {
				st.pkgManagers[ev.PackageManager] = struct{}{}
			}
			if ev.FlowType != "" {
				st.flowTypes[ev.FlowType] = struct{}{}
			}
			// latest session determines mode flags
			if ev.ReceivedAt.After(st.latestSession) {
				st.latestSession = ev.ReceivedAt
				st.info.SandboxEnabled    = ev.SandboxEnabled
				st.info.ParanoidMode      = ev.ParanoidMode
				st.info.TransitiveEnabled = ev.TransitiveEnabled
			}
		}
	}

	list := make([]EndpointInfo, 0, len(m))
	for _, st := range m {
		ep := st.info
		for pm := range st.pkgManagers {
			ep.PackageManagers = append(ep.PackageManagers, pm)
		}
		sort.Strings(ep.PackageManagers)
		for ft := range st.flowTypes {
			ep.FlowTypes = append(ep.FlowTypes, ft)
		}
		sort.Strings(ep.FlowTypes)
		list = append(list, ep)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastSeen.After(list[j].LastSeen)
	})
	return list
}
