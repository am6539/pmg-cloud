package dashboard

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("retention_days must be >= 0")
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.data = cfg
	return cs.save()
}

// validateWebhookURL rejects non-http/https schemes, empty hosts, loopback,
// private, and link-local IP literals to prevent SSRF attacks.
func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL must have a host")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("webhook URL must not target localhost")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("webhook URL must not target a private or loopback address")
		}
	}
	return nil
}

// AddWebhook appends a new webhook entry (assigning a generated ID) and persists.
func (cs *ConfigStore) AddWebhook(wh WebhookEntry) (WebhookEntry, error) {
	if err := validateWebhookURL(wh.URL); err != nil {
		return WebhookEntry{}, err
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	wh.ID = genID()
	cs.data.Webhooks = append(cs.data.Webhooks, wh)
	return wh, cs.save()
}

// UpdateWebhook replaces an existing webhook entry by ID.
func (cs *ConfigStore) UpdateWebhook(wh WebhookEntry) error {
	if err := validateWebhookURL(wh.URL); err != nil {
		return err
	}
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
	hooks := make([]WebhookEntry, 0, len(cs.data.Webhooks))
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
