package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const configFileName = "config.json"

// AlertConfig controls alerting thresholds.
type AlertConfig struct {
	BlockedPerMinute int  `json:"blocked_per_minute"` // 0 = disabled
	MalwareAny       bool `json:"malware_any"`
}

// WebhookEntry is a single configured webhook destination.
type WebhookEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	OnMalware bool   `json:"on_malware"`
	OnBlocked bool   `json:"on_blocked"`
	Enabled   bool   `json:"enabled"`
}

// ServerConfig is the full server configuration persisted to config.json.
type ServerConfig struct {
	RetentionDays int            `json:"retention_days"`
	Alert         AlertConfig    `json:"alert"`
	Webhooks      []WebhookEntry `json:"webhooks"`
}

// ConfigStore manages server configuration in a thread-safe way.
type ConfigStore struct {
	path string
	mu   sync.RWMutex
	data ServerConfig
}

// NewConfigStore opens (or creates) the config.json file inside dataDir.
// Default RetentionDays is 30.
func NewConfigStore(dataDir string) (*ConfigStore, error) {
	cs := &ConfigStore{
		path: filepath.Join(dataDir, configFileName),
	}
	if err := cs.load(); err != nil {
		return nil, err
	}
	return cs, nil
}

func (cs *ConfigStore) load() error {
	data, err := os.ReadFile(cs.path)
	if os.IsNotExist(err) {
		cs.data = ServerConfig{RetentionDays: 30}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}
	if err := json.Unmarshal(data, &cs.data); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}
	if cs.data.RetentionDays == 0 {
		cs.data.RetentionDays = 30
	}
	return nil
}

func (cs *ConfigStore) save() error {
	data, err := json.MarshalIndent(cs.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cs.path, data, 0o600)
}

// Get returns a copy of the current ServerConfig.
func (cs *ConfigStore) Get() ServerConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.data
}

// Update replaces the entire server configuration and persists it.
func (cs *ConfigStore) Update(cfg ServerConfig) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.data = cfg
	return cs.save()
}

// AddWebhook appends a new webhook entry (assigning a generated ID) and persists.
func (cs *ConfigStore) AddWebhook(wh WebhookEntry) (WebhookEntry, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	wh.ID = genID()
	cs.data.Webhooks = append(cs.data.Webhooks, wh)
	return wh, cs.save()
}

// UpdateWebhook replaces an existing webhook entry by ID.
func (cs *ConfigStore) UpdateWebhook(wh WebhookEntry) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for i, w := range cs.data.Webhooks {
		if w.ID == wh.ID {
			cs.data.Webhooks[i] = wh
			return cs.save()
		}
	}
	return fmt.Errorf("webhook not found")
}

// DeleteWebhook removes a webhook entry by ID and persists.
func (cs *ConfigStore) DeleteWebhook(id string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	hooks := cs.data.Webhooks[:0]
	found := false
	for _, w := range cs.data.Webhooks {
		if w.ID == id {
			found = true
			continue
		}
		hooks = append(hooks, w)
	}
	if !found {
		return fmt.Errorf("webhook not found")
	}
	cs.data.Webhooks = hooks
	return cs.save()
}
