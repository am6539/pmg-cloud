package dashboard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteOldFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	n, err := DeleteOldFiles(dir, 30)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestDeleteOldFiles_ZeroRetentionDisabled(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().UTC().AddDate(0, 0, -100).Format("20060102")
	writeEventsFile(t, dir, old, nil)

	n, err := DeleteOldFiles(dir, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	_, statErr := os.Stat(filepath.Join(dir, "events-"+old+".jsonl"))
	assert.NoError(t, statErr, "file must survive when retentionDays=0")
}

func TestDeleteOldFiles_DeletesOldFiles(t *testing.T) {
	dir := t.TempDir()

	old := time.Now().UTC().AddDate(0, 0, -35).Format("20060102")    // 35 days old → deleted
	recent := time.Now().UTC().AddDate(0, 0, -5).Format("20060102") // 5 days old → kept
	writeEventsFile(t, dir, old, nil)
	writeEventsFile(t, dir, recent, nil)

	n, err := DeleteOldFiles(dir, 30)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, statErr := os.Stat(filepath.Join(dir, "events-"+old+".jsonl"))
	assert.True(t, os.IsNotExist(statErr), "old file must be deleted")

	_, statErr = os.Stat(filepath.Join(dir, "events-"+recent+".jsonl"))
	assert.NoError(t, statErr, "recent file must be kept")
}

func TestDeleteOldFiles_KeepsAllWithinRetention(t *testing.T) {
	dir := t.TempDir()

	for _, daysAgo := range []int{0, 1, 7, 14, 29} {
		d := time.Now().UTC().AddDate(0, 0, -daysAgo).Format("20060102")
		writeEventsFile(t, dir, d, nil)
	}

	n, err := DeleteOldFiles(dir, 30)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestDeleteOldFiles_DeletesMultipleOldFiles(t *testing.T) {
	dir := t.TempDir()

	for _, daysAgo := range []int{31, 60, 90} {
		d := time.Now().UTC().AddDate(0, 0, -daysAgo).Format("20060102")
		writeEventsFile(t, dir, d, nil)
	}
	writeEventsFile(t, dir, time.Now().UTC().Format("20060102"), nil) // today → kept

	n, err := DeleteOldFiles(dir, 30)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestDeleteOldFiles_IgnoresNonMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-notadate.jsonl"), []byte("{}"), 0o644))

	n, err := DeleteOldFiles(dir, 30)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
