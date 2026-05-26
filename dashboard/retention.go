package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeleteOldFiles removes events-YYYYMMDD.jsonl files older than retentionDays.
// Pass retentionDays <= 0 to disable deletion. Returns the count of deleted files.
func DeleteOldFiles(dataDir string, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	files, err := filepath.Glob(filepath.Join(dataDir, "events-*.jsonl"))
	if err != nil {
		return 0, fmt.Errorf("glob events files: %w", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	var deleted int
	for _, f := range files {
		base := filepath.Base(f)
		dateStr := strings.TrimPrefix(strings.TrimSuffix(base, ".jsonl"), "events-")
		fileDate, err := time.Parse("20060102", dateStr)
		if err != nil {
			continue // skip files that don't match expected naming
		}
		if fileDate.Before(cutoff) {
			if err := os.Remove(f); err != nil {
				return deleted, fmt.Errorf("remove %s: %w", f, err)
			}
			deleted++
		}
	}
	return deleted, nil
}
