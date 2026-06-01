package dashboard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFindMissingAgents_FlagsStale(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-80 * time.Hour)
	fresh := now.Add(-1 * time.Hour)
	agents := []Agent{
		{ID: "a", Hostname: "old", LastSeen: &stale},
		{ID: "b", Hostname: "new", LastSeen: &fresh},
		{ID: "c", Hostname: "never", LastSeen: nil},
	}
	missing := findMissingAgents(agents, now, 72*time.Hour)
	assert.Len(t, missing, 1)
	assert.Equal(t, "old", missing[0].Hostname)
}

func TestFindMissingAgents_NoneWhenAllFresh(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(-1 * time.Hour)
	agents := []Agent{{ID: "a", Hostname: "h", LastSeen: &fresh}}
	assert.Empty(t, findMissingAgents(agents, now, 72*time.Hour))
}
