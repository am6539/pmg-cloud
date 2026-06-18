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

// Reader provides cached event reads and aggregation methods.
type Reader struct {
	dataDir   string
	mu        sync.RWMutex
	cached    []Event
	cachedAt  time.Time
	cacheTTL  time.Duration
	cacheHash string // file mod hash
}

func NewReader(dataDir string) *Reader {
	return &Reader{
		dataDir:  dataDir,
		cacheTTL: 10 * time.Second,
	}
}

func (r *Reader) eventFilesInRange(from, to time.Time) ([]string, error) {
	// from is inclusive, to is exclusive
	paths := []string{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		fname := d.Format("2006-01-02") + ".jsonl"
		p := filepath.Join(r.dataDir, fname)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (r *Reader) fileHash(paths []string) (string, error) {
	var sb strings.Builder
	for _, p := range paths {
		stat, err := os.Stat(p)
		if err != nil {
			continue
		}
		sb.WriteString(p)
		sb.WriteString(":")
		sb.WriteString(stat.ModTime().Format(time.RFC3339Nano))
		sb.WriteString(";")
	}
	return sb.String(), nil
}

// LoadEvents reads all events from JSONL files within the last `days` days.
// If days == 0, loads all available event files.
func (r *Reader) LoadEvents(days int) ([]Event, error) {
	now := time.Now().UTC()
	var from time.Time
	if days > 0 {
		from = now.AddDate(0, 0, -days).Truncate(24 * time.Hour)
	} else {
		// all time → pick a date far in the past
		from = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	to := now.AddDate(0, 0, 1).Truncate(24 * time.Hour)
	return r.loadEventsRange(from, to)
}

// LoadEventsRange loads events with received_at between from and to (inclusive).
// For custom date range queries (CSV export).
func (r *Reader) LoadEventsRange(from, to time.Time) ([]Event, error) {
	return r.loadEventsRange(from, to.AddDate(0, 0, 1).Truncate(24*time.Hour))
}

func (r *Reader) loadEventsRange(from, to time.Time) ([]Event, error) {
	files, err := r.eventFilesInRange(from, to)
	if err != nil {
		return nil, err
	}
	hash, _ := r.fileHash(files)

	r.mu.RLock()
	if time.Since(r.cachedAt) < r.cacheTTL && r.cacheHash == hash {
		out := make([]Event, len(r.cached))
		copy(out, r.cached)
		r.mu.RUnlock()
		return out, nil
	}
	r.mu.RUnlock()

	events := []Event{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var ev Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				events = append(events, ev)
			}
		}
		f.Close()
	}

	r.mu.Lock()
	r.cached = events
	r.cachedAt = time.Now()
	r.cacheHash = hash
	r.mu.Unlock()

	return events, nil
}

func Aggregate(events []Event) Stats {
	epMap := make(map[string]struct{})
	invMap := make(map[string]struct{})
	var analyzed uint64
	blocked, malicious, suspicious := 0, 0, 0
	ecoMap := make(map[string]int)
	outMap := make(map[string]int)
	dayMap := make(map[string]int)

	for _, ev := range events {
		if ev.EndpointID != "" {
			epMap[ev.EndpointID] = struct{}{}
		}
		if ev.InvocationID != "" {
			invMap[ev.InvocationID] = struct{}{}
		}
		if ev.EventType == "package_decision" {
			analyzed++
			if ev.Action == "blocked" {
				blocked++
			}
			if ev.IsMalware != nil && *ev.IsMalware {
				malicious++
			} else if ev.IsVerified != nil && !*ev.IsVerified {
				suspicious++
			}
			if ev.Ecosystem != "" {
				ecoMap[ev.Ecosystem]++
			}
		}
		if ev.EventType == "session_summary" && ev.Outcome != "" {
			outMap[ev.Outcome]++
		}
		day := ev.ReceivedAt.UTC().Format("2006-01-02")
		dayMap[day]++
	}

	days := make([]DayBucket, 0, len(dayMap))
	for k, v := range dayMap {
		days = append(days, DayBucket{Date: k, Count: v})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })

	recent := events
	if len(recent) > 50 {
		recent = recent[len(recent)-50:]
	}
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].ReceivedAt.After(recent[j].ReceivedAt)
	})

	return Stats{
		Endpoints:          len(epMap),
		Sessions:           len(invMap),
		PackagesAnalyzed:   analyzed,
		MaliciousPackages:  malicious,
		BlockedPackages:    blocked,
		SuspiciousPackages: suspicious,
		ByEcosystem:        ecoMap,
		ByOutcome:          outMap,
		EventsPerDay:       days,
		RecentEvents:       recent,
	}
}

// PackageStat holds per-package event counts.
type PackageStat struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Blocked   int    `json:"blocked"`
	Allowed   int    `json:"allowed"`
}

func ComputePackageStats(events []Event) []PackageStat {
	type key struct {
		eco, name string
	}
	m := make(map[key]*PackageStat)
	for _, ev := range events {
		if ev.EventType != "package_decision" || ev.PackageName == "" {
			continue
		}
		k := key{ev.Ecosystem, ev.PackageName}
		ps, ok := m[k]
		if !ok {
			ps = &PackageStat{Ecosystem: ev.Ecosystem, Name: ev.PackageName}
			m[k] = ps
		}
		if ev.Action == "blocked" {
			ps.Blocked++
		} else if ev.Action == "allowed" {
			ps.Allowed++
		}
	}
	out := make([]PackageStat, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].Blocked + out[i].Allowed
		tj := out[j].Blocked + out[j].Allowed
		return ti > tj
	})
	return out
}

// EndpointInfo is a merged view of an enrolled agent + event-derived stats.
type EndpointInfo struct {
	EndpointID      string     `json:"endpoint_id"`
	Hostname        string     `json:"hostname"`
	Label           string     `json:"label,omitempty"`
	OS              string     `json:"os"`
	Arch            string     `json:"arch"`
	ToolVersion     string     `json:"tool_version,omitempty"`
	LocalIP         string     `json:"local_ip,omitempty"`
	RemoteIP        string     `json:"remote_ip,omitempty"`
	GroupID         string     `json:"group_id,omitempty"`
	EnrolledAt      time.Time  `json:"enrolled_at"`
	FirstSeen       time.Time  `json:"first_seen"`
	LastSeen        time.Time  `json:"last_seen"`
	Sessions        int        `json:"sessions"`
	TotalPackages   int        `json:"total_packages"`
	BlockedPackages int        `json:"blocked_packages"`
	Removed         bool       `json:"removed"`
}

// MergeAgentEndpoints combines enrolled agents with event-derived endpoint stats.
func MergeAgentEndpoints(agents []Agent, events []Event) []EndpointInfo {
	type epData struct {
		firstSeen, lastSeen time.Time
		invocations         map[string]struct{}
		totalPkgs, blocked  int
	}
	m := make(map[string]*epData)
	for _, ev := range events {
		if ev.EndpointID == "" {
			continue
		}
		ed, ok := m[ev.EndpointID]
		if !ok {
			ed = &epData{invocations: make(map[string]struct{})}
			m[ev.EndpointID] = ed
		}
		if ed.firstSeen.IsZero() || ev.ReceivedAt.Before(ed.firstSeen) {
			ed.firstSeen = ev.ReceivedAt
		}
		if ev.ReceivedAt.After(ed.lastSeen) {
			ed.lastSeen = ev.ReceivedAt
		}
		if ev.InvocationID != "" {
			ed.invocations[ev.InvocationID] = struct{}{}
		}
		if ev.EventType == "package_decision" {
			ed.totalPkgs++
			if ev.Action == "blocked" {
				ed.blocked++
			}
		}
	}

	agentMap := make(map[string]Agent)
	for _, a := range agents {
		agentMap[a.ID] = a
	}

	endpoints := make([]EndpointInfo, 0, len(m))
	for epID, ed := range m {
		a, hasAgent := agentMap[epID]
		ei := EndpointInfo{
			EndpointID:      epID,
			FirstSeen:       ed.firstSeen,
			LastSeen:        ed.lastSeen,
			Sessions:        len(ed.invocations),
			TotalPackages:   ed.totalPkgs,
			BlockedPackages: ed.blocked,
		}
		if hasAgent {
			ei.Hostname = a.Hostname
			ei.Label = a.Label
			ei.OS = a.OS
			ei.Arch = a.Arch
			ei.ToolVersion = a.PMGVersion
			ei.LocalIP = a.LocalIP
			ei.RemoteIP = a.RemoteIP
			ei.GroupID = a.GroupID
			ei.EnrolledAt = a.EnrolledAt
			ei.Removed = a.Removed
			if a.LastSeen != nil && a.LastSeen.After(ei.LastSeen) {
				ei.LastSeen = *a.LastSeen
			}
		}
		endpoints = append(endpoints, ei)
	}

	// also include enrolled agents that have zero events
	for _, a := range agents {
		if _, seen := m[a.ID]; !seen {
			ei := EndpointInfo{
				EndpointID:  a.ID,
				Hostname:    a.Hostname,
				Label:       a.Label,
				OS:          a.OS,
				Arch:        a.Arch,
				ToolVersion: a.PMGVersion,
				LocalIP:     a.LocalIP,
				RemoteIP:    a.RemoteIP,
				GroupID:     a.GroupID,
				EnrolledAt:  a.EnrolledAt,
				FirstSeen:   a.EnrolledAt,
				Removed:     a.Removed,
			}
			if a.LastSeen != nil {
				ei.LastSeen = *a.LastSeen
			} else {
				ei.LastSeen = a.EnrolledAt
			}
			endpoints = append(endpoints, ei)
		}
	}

	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].LastSeen.After(endpoints[j].LastSeen)
	})
	return endpoints
}

// CIRepoStat holds per-repository CI event stats.
type CIRepoStat struct {
	Repository string    `json:"repository"`
	Count      int       `json:"count"`
	Blocked    int       `json:"blocked"`
	LastSeen   time.Time `json:"last_seen"`
}

// CIBranchStat holds per-branch CI event stats.
type CIBranchStat struct {
	Branch   string    `json:"branch"`
	Count    int       `json:"count"`
	Blocked  int       `json:"blocked"`
	LastSeen time.Time `json:"last_seen"`
}

// CIProviderStat holds per-provider CI event stats.
type CIProviderStat struct {
	Provider string `json:"provider"`
	Count    int    `json:"count"`
	Blocked  int    `json:"blocked"`
}

// CIStats aggregates CI-specific metrics.
type CIStats struct {
	TopRepositories []CIRepoStat     `json:"top_repositories"`
	TopBranches     []CIBranchStat   `json:"top_branches"`
	ByProvider      []CIProviderStat `json:"by_provider"`
	TotalCIEvents   int              `json:"total_ci_events"`
}

func ComputeCIStats(events []Event) CIStats {
	repoMap := make(map[string]*CIRepoStat)
	branchMap := make(map[string]*CIBranchStat)
	providerMap := make(map[string]*CIProviderStat)
	total := 0

	for _, ev := range events {
		if ev.CIProvider == "" {
			continue
		}
		total++
		blocked := 0
		if ev.EventType == "package_decision" && ev.Action == "blocked" {
			blocked = 1
		}

		// repo stats
		if ev.CIRepository != "" {
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
		}

		// branch stats
		if ev.CIBranch != "" {
			bs, ok := branchMap[ev.CIBranch]
			if !ok {
				bs = &CIBranchStat{Branch: ev.CIBranch}
				branchMap[ev.CIBranch] = bs
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

// DeleteEventsByEndpointID removes all events for a given endpoint from JSONL files.
// This rewrites each affected JSONL file, filtering out events with matching endpoint_id.
func (r *Reader) DeleteEventsByEndpointID(dataDir, endpointID string) error {
	// Load all events (no date filter)
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	files, err := r.eventFilesInRange(from, to)
	if err != nil {
		return err
	}

	// Process each file
	for _, path := range files {
		if err := deleteEventsFromFile(path, endpointID); err != nil {
			return err
		}
	}

	// Clear cache since we modified files
	r.mu.Lock()
	r.cached = nil
	r.cachedAt = time.Time{}
	r.cacheHash = ""
	r.mu.Unlock()

	return nil
}

func deleteEventsFromFile(path, endpointID string) error {
	// Read all lines
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // file doesn't exist, nothing to do
		}
		return err
	}
	defer f.Close()

	var kept []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		var ev Event
		if json.Unmarshal([]byte(line), &ev) == nil {
			if ev.EndpointID != endpointID {
				kept = append(kept, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	f.Close()

	// Write back filtered events
	tmpPath := path + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	for _, line := range kept {
		if _, err := tmp.WriteString(line + "\n"); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Replace original with temp
	return os.Rename(tmpPath, path)
}
