package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const updateConfigFileName = "update.json"

type BinaryMeta struct {
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
}

type UpdateConfig struct {
	TargetVersion string                `json:"target_version,omitempty"`
	PublishedAt   *time.Time            `json:"published_at,omitempty"`
	Binaries      map[string]BinaryMeta `json:"binaries"`
}

// AgentUpdateInfo is returned to a PMG agent on heartbeat.
type AgentUpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	Version         string `json:"version,omitempty"`
	DownloadURL     string `json:"download_url,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
}

type UpdateStore struct {
	dataDir string
	mu      sync.RWMutex
	cfg     UpdateConfig
}

func NewUpdateStore(dataDir string) (*UpdateStore, error) {
	us := &UpdateStore{dataDir: dataDir}
	if err := us.load(); err != nil {
		return nil, err
	}
	return us, nil
}

func (us *UpdateStore) load() error {
	data, err := os.ReadFile(filepath.Join(us.dataDir, updateConfigFileName))
	if os.IsNotExist(err) {
		us.cfg = UpdateConfig{Binaries: map[string]BinaryMeta{}}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read update config: %w", err)
	}
	if err := json.Unmarshal(data, &us.cfg); err != nil {
		return fmt.Errorf("parse update config: %w", err)
	}
	if us.cfg.Binaries == nil {
		us.cfg.Binaries = map[string]BinaryMeta{}
	}
	return nil
}

func (us *UpdateStore) save() error {
	data, err := json.MarshalIndent(us.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(us.dataDir, updateConfigFileName), data, 0o600)
}

func (us *UpdateStore) GetConfig() UpdateConfig {
	us.mu.RLock()
	defer us.mu.RUnlock()
	out := UpdateConfig{
		TargetVersion: us.cfg.TargetVersion,
		PublishedAt:   us.cfg.PublishedAt,
		Binaries:      make(map[string]BinaryMeta, len(us.cfg.Binaries)),
	}
	for k, v := range us.cfg.Binaries {
		out.Binaries[k] = v
	}
	return out
}

func (us *UpdateStore) BinaryPath(goos, arch string) string {
	name := "pmg-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(us.dataDir, "binaries", name)
}

func (us *UpdateStore) BinariesDir() string {
	return filepath.Join(us.dataDir, "binaries")
}

func (us *UpdateStore) StoreBinaryMeta(goos, arch string, meta BinaryMeta) error {
	us.mu.Lock()
	defer us.mu.Unlock()
	if meta.UploadedAt.IsZero() {
		meta.UploadedAt = time.Now().UTC()
	}
	us.cfg.Binaries[goos+"/"+arch] = meta
	return us.save()
}

func (us *UpdateStore) DeleteBinaryMeta(goos, arch string) error {
	us.mu.Lock()
	defer us.mu.Unlock()
	delete(us.cfg.Binaries, goos+"/"+arch)
	return us.save()
}

func (us *UpdateStore) SetTargetVersion(version string) error {
	us.mu.Lock()
	defer us.mu.Unlock()
	now := time.Now().UTC()
	us.cfg.TargetVersion = version
	us.cfg.PublishedAt = &now
	return us.save()
}

func (us *UpdateStore) UpdateInfoForAgent(goos, arch, currentVersion string) AgentUpdateInfo {
	us.mu.RLock()
	defer us.mu.RUnlock()
	if us.cfg.TargetVersion == "" || currentVersion == us.cfg.TargetVersion {
		return AgentUpdateInfo{}
	}
	meta, ok := us.cfg.Binaries[goos+"/"+arch]
	if !ok {
		return AgentUpdateInfo{}
	}
	downloadPath := "/download/pmg-" + goos + "-" + arch
	if goos == "windows" {
		downloadPath += ".exe"
	}
	return AgentUpdateInfo{
		UpdateAvailable: true,
		Version:         us.cfg.TargetVersion,
		DownloadURL:     downloadPath,
		SHA256:          meta.SHA256,
	}
}
