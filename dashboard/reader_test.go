package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeEventsFile writes a slice of events as JSONL to events-<dateStr>.jsonl in dir.
func writeEventsFile(t *testing.T, dir, dateStr string, events []Event) {
	t.Helper()
	path := filepath.Join(dir, "events-"+dateStr+".jsonl")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		require.NoError(t, enc.Encode(ev))
	}
}

func boolPtr(b bool) *bool { return &b }

func TestLoadEvents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := NewReader(dir)

	events, err := r.LoadEvents(0)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestLoadEvents_ReadsEvents(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().UTC().Format("20060102")
	writeEventsFile(t, dir, today, []Event{
		{EventID: "e1", EventType: "SESSION_SUMMARY", TotalAnalyzed: 5},
		{EventID: "e2", EventType: "PACKAGE_DECISION", PackageName: "lodash"},
	})

	r := NewReader(dir)
	events, err := r.LoadEvents(0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	ids := []string{events[0].EventID, events[1].EventID}
	assert.ElementsMatch(t, []string{"e1", "e2"}, ids)
}

func TestLoadEvents_SkipsOldFiles(t *testing.T) {
	dir := t.TempDir()

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("20060102")
	writeEventsFile(t, dir, yesterday, []Event{
		{EventID: "recent", EventType: "SESSION_SUMMARY"},
	})

	old := time.Now().UTC().AddDate(0, 0, -10).Format("20060102")
	writeEventsFile(t, dir, old, []Event{
		{EventID: "old-event", EventType: "SESSION_SUMMARY"},
	})

	r := NewReader(dir)
	events, err := r.LoadEvents(7)
	require.NoError(t, err)

	ids := make([]string, 0, len(events))
	for _, ev := range events {
		ids = append(ids, ev.EventID)
	}
	assert.Contains(t, ids, "recent")
	assert.NotContains(t, ids, "old-event")
}

func TestLoadEvents_CacheHitWithinTTL(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().UTC().Format("20060102")
	writeEventsFile(t, dir, today, []Event{
		{EventID: "e1", EventType: "SESSION_SUMMARY"},
	})

	r := NewReader(dir)

	first, err := r.LoadEvents(0)
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Add a second file after first call — cache should mask it
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("20060102")
	writeEventsFile(t, dir, yesterday, []Event{
		{EventID: "e2", EventType: "SESSION_SUMMARY"},
	})

	second, err := r.LoadEvents(0)
	require.NoError(t, err)
	// Within TTL: must return the same cached slice, not the new file
	assert.Len(t, second, 1)
	assert.Equal(t, "e1", second[0].EventID)
}

func TestAggregate_Counts(t *testing.T) {
	events := []Event{
		{
			EventType: "SESSION_SUMMARY", TotalAnalyzed: 10,
			EndpointID: "ep1", Outcome: "SUCCESS",
			ReceivedAt: time.Now(),
		},
		{
			EventType: "SESSION_SUMMARY", TotalAnalyzed: 5,
			EndpointID: "ep2", Outcome: "BLOCKED",
			ReceivedAt: time.Now(),
		},
		{
			// malicious + blocked
			EventType: "PACKAGE_DECISION", EndpointID: "ep1",
			IsMalware: boolPtr(true), Action: "BLOCKED", Ecosystem: "npm",
			ReceivedAt: time.Now(),
		},
		{
			// not malware + blocked → suspicious
			EventType: "PACKAGE_DECISION", EndpointID: "ep1",
			IsMalware: boolPtr(false), Action: "BLOCKED", Ecosystem: "npm",
			ReceivedAt: time.Now(),
		},
		{
			EventType: "PACKAGE_DECISION", EndpointID: "ep2",
			IsMalware: boolPtr(false), Action: "CONFIRMED", Ecosystem: "pypi",
			ReceivedAt: time.Now(),
		},
	}

	stats := Aggregate(events)

	assert.Equal(t, 2, stats.Sessions)
	assert.Equal(t, uint64(15), stats.PackagesAnalyzed)
	assert.Equal(t, 1, stats.MaliciousPackages)
	assert.Equal(t, 2, stats.BlockedPackages)
	assert.Equal(t, 1, stats.SuspiciousPackages)
	assert.Equal(t, 2, stats.Endpoints)
	assert.Equal(t, 2, stats.ByEcosystem["npm"])
	assert.Equal(t, 1, stats.ByEcosystem["pypi"])
	assert.Equal(t, 1, stats.ByOutcome["SUCCESS"])
	assert.Equal(t, 1, stats.ByOutcome["BLOCKED"])
}

func TestAggregate_DeduplicatesEndpoints(t *testing.T) {
	t1 := time.Now().Add(-5 * time.Minute)
	t2 := time.Now()

	events := []Event{
		{EventType: "SESSION_SUMMARY", EndpointID: "ep1", ReceivedAt: t1},
		{EventType: "SESSION_SUMMARY", EndpointID: "ep1", ReceivedAt: t2},
		{EventType: "SESSION_SUMMARY", EndpointID: "ep1", ReceivedAt: t1},
	}

	stats := Aggregate(events)
	assert.Equal(t, 1, stats.Endpoints)
}

func TestMergeAgentEndpoints_HostnameMatchPreservesSeparateIDs(t *testing.T) {
	now := time.Now().UTC()
	agents := []Agent{{ID: "agent-1", Hostname: "build-host", ScanState: "completed", LastScanAt: &now}}
	events := []Event{{EndpointID: "build-host", MachineID: "machine-1", InvocationID: "run-1", ReceivedAt: now}}

	got := MergeAgentEndpoints(agents, events)
	require.Len(t, got, 1)
	assert.Equal(t, "agent-1", got[0].AgentID)
	assert.Equal(t, "build-host", got[0].EndpointID)
	assert.Equal(t, "machine-1", got[0].MachineID)
	assert.True(t, got[0].Managed)
	assert.Equal(t, "completed", got[0].ScanState)
	assert.Equal(t, &now, got[0].LastScanAt)
}

func TestMergeAgentEndpoints_HostnameMatchIsCaseInsensitiveWithoutDuplicate(t *testing.T) {
	now := time.Now().UTC()
	agents := []Agent{{ID: "agent-1", Hostname: "build-host"}}
	events := []Event{{EndpointID: "BUILD-HOST", ReceivedAt: now}}

	got := MergeAgentEndpoints(agents, events)

	require.Len(t, got, 1)
	assert.Equal(t, "agent-1", got[0].AgentID)
	assert.Equal(t, "BUILD-HOST", got[0].EndpointID)
}

func TestMergeAgentEndpoints_ObservedEndpointIsNotManaged(t *testing.T) {
	now := time.Now().UTC()
	got := MergeAgentEndpoints(nil, []Event{{EndpointID: "ci-runner", MachineID: "machine-ci", ReceivedAt: now}})
	require.Len(t, got, 1)
	assert.Empty(t, got[0].AgentID)
	assert.Equal(t, "ci-runner", got[0].EndpointID)
	assert.False(t, got[0].Managed)
}

func TestMergeAgentEndpoints_LatestNonEmptyMachineIDWins(t *testing.T) {
	now := time.Now().UTC()
	got := MergeAgentEndpoints(nil, []Event{
		{EndpointID: "ci-runner", MachineID: "machine-new", ReceivedAt: now},
		{EndpointID: "ci-runner", MachineID: "machine-old", ReceivedAt: now.Add(-time.Hour)},
		{EndpointID: "ci-runner", ReceivedAt: now.Add(time.Hour)},
	})

	require.Len(t, got, 1)
	assert.Equal(t, "machine-new", got[0].MachineID)
}

func TestMergeAgentEndpoints_ObservedEndpointPopulatesHostnameOSArchRemoteIP(t *testing.T) {
	now := time.Now().UTC()
	got := MergeAgentEndpoints(nil, []Event{{
		EndpointID: "ci-runner",
		Hostname:   "ci-runner-host",
		OS:         "linux",
		Arch:       "amd64",
		RemoteIP:   "203.0.113.5",
		ReceivedAt: now,
	}})

	require.Len(t, got, 1)
	assert.Equal(t, "ci-runner-host", got[0].Hostname)
	assert.Equal(t, "linux", got[0].OS)
	assert.Equal(t, "amd64", got[0].Arch)
	assert.Equal(t, "203.0.113.5", got[0].RemoteIP)
}

func TestMergeAgentEndpoints_LatestNonEmptyHostnameOSArchRemoteIPWins(t *testing.T) {
	now := time.Now().UTC()
	got := MergeAgentEndpoints(nil, []Event{
		{
			EndpointID: "ci-runner",
			Hostname:   "old-host", OS: "linux", Arch: "amd64", RemoteIP: "10.0.0.1",
			ReceivedAt: now.Add(-time.Hour),
		},
		{
			EndpointID: "ci-runner",
			Hostname:   "new-host", OS: "darwin", Arch: "arm64", RemoteIP: "10.0.0.2",
			ReceivedAt: now,
		},
		{
			// Latest event overall, but omits these fields — must not blank out
			// the previously-captured non-empty values.
			EndpointID: "ci-runner",
			ReceivedAt: now.Add(time.Hour),
		},
	})

	require.Len(t, got, 1)
	assert.Equal(t, "new-host", got[0].Hostname)
	assert.Equal(t, "darwin", got[0].OS)
	assert.Equal(t, "arm64", got[0].Arch)
	assert.Equal(t, "10.0.0.2", got[0].RemoteIP)
}

func TestMergeAgentEndpoints_ZeroEventAgentIsManagedWithScanState(t *testing.T) {
	now := time.Now().UTC()
	summary := &EcosystemScanSummary{FlaggedCount: 2}
	agent := Agent{
		ID:               "agent-1",
		Hostname:         "build-host",
		ScanState:        "completed",
		ScanDispatchedAt: &now,
		LastScanAt:       &now,
		LastScanSummary:  summary,
	}

	got := MergeAgentEndpoints([]Agent{agent}, nil)

	require.Len(t, got, 1)
	assert.True(t, got[0].Managed)
	assert.Equal(t, "completed", got[0].ScanState)
	assert.Equal(t, &now, got[0].ScanDispatchedAt)
	assert.Equal(t, &now, got[0].LastScanAt)
	assert.Equal(t, summary, got[0].LastScanSummary)
}
