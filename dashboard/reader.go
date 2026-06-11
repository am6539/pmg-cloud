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
	RemoteIP     string     `json:"remote_ip,omitempty"`
	EventType    string     `json:"event_type"`
	// package_decision
	Ecosystem      string `json:"ecosystem"`
	PackageName    string `json:"package_name"`
	PackageVersion string `json:"package_version"`
	Action         string `json:"action"`
	IsMalware      *bool  `json:"is_malware"`
	IsVerified     *bool  `json:"is_verified"`
	AnalysisID     string `json:"analysis_id"`
	// invocation context fields
	CIProvider       string `json:"ci_provider,omitempty"`
	CIRepository     string `json:"ci_repository,omitempty"`
	CIBranch         string `json:"ci_branch,omitempty"`
	CICommitSHA      string `json:"ci_commit_sha,omitempty"`
	AgentName        string `json:"agent_name,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	Command          string `json:"command,omitempty"`
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
	Name                 string     `json:"name"`
	Ecosystem            string     `json:"ecosystem,omitempty"`
	Count                int        `json:"count"`
	BlockedCount         int        `json:"blocked_count,omitempty"`
	MalwareCount         int        `json:"malware_count,omitempty"`
	CooldownBlockedCount int        `json:"cooldown_blocked_count,omitempty"`
	Versions             []string   `json:"versions,omitempty"`
	LastSeen             *time.Time `json:"last_seen,omitempty"`
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
	Label             string    `json:"label,omitempty"` // admin-assigned friendly name from the enrolled agent
	OS                string    `json:"os"`
	Arch              string    `json:"arch"`
	RemoteIP          string    `json:"remote_ip,omitempty"`
	LocalIP           string    `json:"local_ip,omitempty"`
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
	dataDir    string
	mu         sync.Mutex
	cache      map[int]cachedLoad
	rangeCache map[string]cachedLoad
}

const cacheTTL = 5 * time.Second

type cachedLoad struct {
	events   []Event
	cachedAt time.Time
}

func NewReader(dataDir string) *Reader {
	return &Reader{
		dataDir:    dataDir,
		cache:      make(map[int]cachedLoad),
		rangeCache: make(map[string]cachedLoad),
	}
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

// LoadEventsRange loads events with received_at between from and to (inclusive).
// Results are cached for 5 seconds keyed by the date range.
func (r *Reader) LoadEventsRange(from, to time.Time) ([]Event, error) {
	key := from.UTC().Format("20060102") + "-" + to.UTC().Format("20060102")

	r.mu.Lock()
	if c, ok := r.rangeCache[key]; ok && time.Since(c.cachedAt) < cacheTTL {
		r.mu.Unlock()
		return c.events, nil
	}
	r.mu.Unlock()

	events, err := r.loadRangeFromDisk(from, to)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.rangeCache[key] = cachedLoad{events: events, cachedAt: time.Now()}
	r.mu.Unlock()
	return events, nil
}

func (r *Reader) loadRangeFromDisk(from, to time.Time) ([]Event, error) {
	files, err := filepath.Glob(filepath.Join(r.dataDir, "events-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	fromDay := from.UTC().Truncate(24 * time.Hour)
	toDay := to.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour) // inclusive end

	events := make([]Event, 0)
	for _, f := range files {
		base := filepath.Base(f)
		dateStr := strings.TrimPrefix(strings.TrimSuffix(base, ".jsonl"), "events-")
		fileDate, err := time.Parse("20060102", dateStr)
		if err != nil {
			continue
		}
		if fileDate.Before(fromDay) || !fileDate.Before(toDay) {
			continue
		}

		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(fh)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			var ev Event
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
				if !ev.ReceivedAt.Before(from) && !ev.ReceivedAt.After(to) {
					events = append(events, ev)
				}
			}
		}
		_ = scanner.Err() // non-fatal: partial reads accepted
		fh.Close()
	}
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

	events := make([]Event, 0)
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
		_ = scanner.Err() // non-fatal: partial reads accepted
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
	// versionSets tracks distinct versions seen per package key so the table can
	// show which versions were involved without a drill-down.
	versionSets := make(map[string]map[string]struct{})
	ecoMap := make(map[string]int)
	epMap := make(map[string]int)

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
			if ev.Action == "BLOCKED" || ev.Action == "COOLDOWN_BLOCKED" {
				s.BlockedCount++
			}
			if ev.Action == "COOLDOWN_BLOCKED" {
				s.CooldownBlockedCount++
			}
			if ev.IsMalware != nil && *ev.IsMalware {
				s.MalwareCount++
			}
			if s.LastSeen == nil || ev.ReceivedAt.After(*s.LastSeen) {
				t := ev.ReceivedAt
				s.LastSeen = &t
			}
			if ev.PackageVersion != "" {
				if versionSets[key] == nil {
					versionSets[key] = make(map[string]struct{})
				}
				versionSets[key][ev.PackageVersion] = struct{}{}
			}
			pkgMap[key] = s
		}
		if ev.Ecosystem != "" {
			ecoMap[ev.Ecosystem]++
		}
		if ev.EndpointID != "" {
			epMap[ev.EndpointID]++
		}
	}

	// Materialize sorted version lists onto each package stat.
	for key, set := range versionSets {
		versions := make([]string, 0, len(set))
		for v := range set {
			versions = append(versions, v)
		}
		sort.Strings(versions)
		s := pkgMap[key]
		s.Versions = versions
		pkgMap[key] = s
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
			if ev.RemoteIP != "" {
				st.info.RemoteIP = ev.RemoteIP
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

// MergeAgentEndpoints adds enrolled agents that have no matching event-sourced endpoint.
// Correlation is by hostname (case-insensitive). For endpoints that DO match an enrolled
// agent, the agent's PMGVersion overlays the event-sourced ToolVersion: the agent record
// is kept fresh by heartbeat, so it reflects self-updates that have not yet produced a new
// install event. New agents get LastSeen = EnrolledAt and Sessions = 0.
func MergeAgentEndpoints(endpoints []EndpointInfo, agents []Agent) []EndpointInfo {
	agentByHost := make(map[string]Agent, len(agents))
	for _, a := range agents {
		agentByHost[strings.ToLower(a.Hostname)] = a
	}

	seen := make(map[string]struct{}, len(endpoints))
	for i := range endpoints {
		host := strings.ToLower(endpoints[i].Hostname)
		seen[host] = struct{}{}
		a, ok := agentByHost[host]
		if !ok {
			continue
		}
		if a.PMGVersion != "" {
			endpoints[i].ToolVersion = a.PMGVersion
		}
		if a.Label != "" {
			endpoints[i].Label = a.Label
		}
		if a.LocalIP != "" {
			endpoints[i].LocalIP = a.LocalIP
		}
		if a.RemoteIP != "" {
			endpoints[i].RemoteIP = a.RemoteIP
		}
		// Heartbeat keeps Agent.LastSeen fresh even without a new install event,
		// so use it when newer to keep the Endpoints online/offline status in sync
		// with the Agents tab.
		if a.LastSeen != nil && a.LastSeen.After(endpoints[i].LastSeen) {
			endpoints[i].LastSeen = *a.LastSeen
		}
	}
	for _, a := range agents {
		if _, ok := seen[strings.ToLower(a.Hostname)]; ok {
			continue
		}
		lastSeen := a.EnrolledAt
		if a.LastSeen != nil {
			lastSeen = *a.LastSeen
		}
		endpoints = append(endpoints, EndpointInfo{
			EndpointID:  a.ID,
			MachineID:   a.ID,
			Hostname:    a.Hostname,
			Label:       a.Label,
			OS:          a.OS,
			Arch:        a.Arch,
			RemoteIP:    a.RemoteIP,
			LocalIP:     a.LocalIP,
			LastSeen:    lastSeen,
			ToolVersion: a.PMGVersion,
		})
		seen[strings.ToLower(a.Hostname)] = struct{}{}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].LastSeen.After(endpoints[j].LastSeen)
	})
	return endpoints
}

// CIRepoStat aggregates event counts per CI repository.
type CIRepoStat struct {
	Repository string    `json:"repository"`
	Count      int       `json:"count"`
	Blocked    int       `json:"blocked"`
	LastSeen   time.Time `json:"last_seen"`
}

// CIBranchStat aggregates event counts per branch within a CI repository.
type CIBranchStat struct {
	Branch     string    `json:"branch"`
	Repository string    `json:"repository"`
	Count      int       `json:"count"`
	Blocked    int       `json:"blocked"`
	LastSeen   time.Time `json:"last_seen"`
}

// CIProviderStat aggregates event counts per CI provider.
type CIProviderStat struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
	Blocked  int    `json:"blocked"`
}

// CIStats is returned by GET /api/ci-stats.
type CIStats struct {
	TopRepositories []CIRepoStat     `json:"top_repositories"`
	TopBranches     []CIBranchStat   `json:"top_branches"`
	ByProvider      []CIProviderStat `json:"by_provider"`
	TotalCIEvents   int              `json:"total_ci_events"`
}

// ComputeCIStats aggregates CI telemetry from events where CIRepository is set.
func ComputeCIStats(events []Event) CIStats {
	type repoKey = string
	type branchKey = string

	repoMap := make(map[repoKey]*CIRepoStat)
	branchMap := make(map[branchKey]*CIBranchStat)
	providerMap := make(map[string]*CIProviderStat)
	total := 0

	isBlocked := func(ev Event) bool {
		return ev.Action == "BLOCKED" || ev.Action == "COOLDOWN_BLOCKED"
	}

	for _, ev := range events {
		if ev.CIRepository == "" {
			continue
		}
		total++
		blocked := 0
		if isBlocked(ev) {
			blocked = 1
		}

		// repo stats
		rs, ok := repoMap[ev.CIRepository]
		if !ok {
			rs = &CIRepoStat{Repository: ev.CIRepository}
			repoMap[ev.CIRepository] = rs
		}
		rs.Count++
		rs.Blocked += blocked
		if ev.ReceivedAt.After(rs.LastSeen) {
			rs.LastSeen = ev.ReceivedAt
		}

		// branch stats
		if ev.CIBranch != "" {
			bk := ev.CIRepository + "|" + ev.CIBranch
			bs, ok := branchMap[bk]
			if !ok {
				bs = &CIBranchStat{Branch: ev.CIBranch, Repository: ev.CIRepository}
				branchMap[bk] = bs
			}
			bs.Count++
			bs.Blocked += blocked
			if ev.ReceivedAt.After(bs.LastSeen) {
				bs.LastSeen = ev.ReceivedAt
			}
		}

		// provider stats
		if ev.CIProvider != "" {
			ps, ok := providerMap[ev.CIProvider]
			if !ok {
				ps = &CIProviderStat{Provider: ev.CIProvider}
				providerMap[ev.CIProvider] = ps
			}
			ps.Count++
			ps.Blocked += blocked
		}
	}

	// flatten and sort repos by count
	repos := make([]CIRepoStat, 0, len(repoMap))
	for _, v := range repoMap {
		repos = append(repos, *v)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Count > repos[j].Count })
	if len(repos) > 10 {
		repos = repos[:10]
	}

	// flatten and sort branches by count
	branches := make([]CIBranchStat, 0, len(branchMap))
	for _, v := range branchMap {
		branches = append(branches, *v)
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Count > branches[j].Count })
	if len(branches) > 10 {
		branches = branches[:10]
	}

	// flatten providers
	providers := make([]CIProviderStat, 0, len(providerMap))
	for _, v := range providerMap {
		providers = append(providers, *v)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Count > providers[j].Count })

	return CIStats{
		TopRepositories: repos,
		TopBranches:     branches,
		ByProvider:      providers,
		TotalCIEvents:   total,
	}
}
