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

func TestEndpointList_DeduplicatesAndSorts(t *testing.T) {
	t1 := time.Now().Add(-10 * time.Minute)
	t2 := time.Now().Add(-5 * time.Minute)
	t3 := time.Now()

	events := []Event{
		{EventType: "SESSION_SUMMARY", EndpointID: "ep-a", Hostname: "host-a", ReceivedAt: t1},
		{EventType: "SESSION_SUMMARY", EndpointID: "ep-b", Hostname: "host-b", ReceivedAt: t2},
		{EventType: "SESSION_SUMMARY", EndpointID: "ep-a", Hostname: "host-a", ReceivedAt: t3},
		{EventType: "PACKAGE_DECISION", EndpointID: "ep-a", ReceivedAt: t3},
	}

	list := EndpointList(events)
	require.Len(t, list, 2)

	// ep-a last seen at t3, ep-b at t2 → ep-a must come first
	assert.Equal(t, "ep-a", list[0].EndpointID)
	assert.Equal(t, "ep-b", list[1].EndpointID)

	// ep-a has 2 SESSION_SUMMARY events
	assert.Equal(t, 2, list[0].Sessions)
	assert.Equal(t, "host-a", list[0].Hostname)
}

func TestEndpointList_IgnoresEmptyEndpointID(t *testing.T) {
	events := []Event{
		{EventType: "SESSION_SUMMARY", EndpointID: "", ReceivedAt: time.Now()},
	}
	list := EndpointList(events)
	assert.Empty(t, list)
}

func TestEndpointList_SortsByLastSeenDesc(t *testing.T) {
	t1 := time.Now().Add(-20 * time.Minute)
	t2 := time.Now().Add(-10 * time.Minute)
	t3 := time.Now()

	events := []Event{
		{EventType: "SESSION_SUMMARY", EndpointID: "oldest", ReceivedAt: t1},
		{EventType: "SESSION_SUMMARY", EndpointID: "mid", ReceivedAt: t2},
		{EventType: "SESSION_SUMMARY", EndpointID: "newest", ReceivedAt: t3},
	}

	list := EndpointList(events)
	require.Len(t, list, 3)
	assert.Equal(t, "newest", list[0].EndpointID)
	assert.Equal(t, "mid", list[1].EndpointID)
	assert.Equal(t, "oldest", list[2].EndpointID)
}
