package dashboard

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const auditFileName = "audit.jsonl"

// AuditEntry is a single append-only audit log record.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	Subject   string    `json:"subject"`
	Detail    string    `json:"detail,omitempty"`
}

// AuditLog appends structured audit entries to a JSONL file.
type AuditLog struct {
	path string
	mu   sync.RWMutex
}

// NewAuditLog creates an AuditLog that writes to audit.jsonl inside dataDir.
func NewAuditLog(dataDir string) *AuditLog {
	return &AuditLog{path: filepath.Join(dataDir, auditFileName)}
}

// Log writes an audit entry. On failure it logs a warning and continues.
func (al *AuditLog) Log(action, subject, detail string) {
	entry := AuditEntry{
		Timestamp: time.Now().UTC(),
		Action:    action,
		Subject:   subject,
		Detail:    detail,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("audit marshal error", "err", err)
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	f, err := os.OpenFile(al.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("audit open error", "err", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		slog.Warn("audit write error", "err", err)
	}
}

// Read returns audit entries newest-first. limit=0 returns all entries.
func (al *AuditLog) Read(limit int) ([]AuditEntry, error) {
	al.mu.RLock()
	defer al.mu.RUnlock()

	f, err := os.Open(al.path)
	if os.IsNotExist(err) {
		return []AuditEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []AuditEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err == nil {
			entries = append(entries, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// reverse to newest-first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}
