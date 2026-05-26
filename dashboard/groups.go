package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	groupsFileName  = "groups.json"
	apiKeyPrefix    = "pmg_"
	apiKeyRandBytes = 32 // 256 bits → 64 hex chars
)

// Group is a named container that owns a set of API keys and associated endpoints.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey is a credential tied to a Group.
// KeyHash stores sha256(plaintext); KeyPrefix stores the first 12 chars for display.
// The plaintext key is returned exactly once at creation time and never stored.
type APIKey struct {
	ID        string    `json:"id"`
	GroupID   string    `json:"group_id"`
	Name      string    `json:"name"`
	KeyHash   string    `json:"key_hash"`
	KeyPrefix string    `json:"key_prefix"`
	CreatedAt time.Time `json:"created_at"`
}

type groupsFile struct {
	Groups  []Group  `json:"groups"`
	APIKeys []APIKey `json:"api_keys"`
}

// GroupStore persists groups and API keys to a JSON file inside dataDir.
type GroupStore struct {
	path string
	mu   sync.RWMutex
	data groupsFile
}

// NewGroupStore opens (or creates) the groups.json file inside dataDir.
func NewGroupStore(dataDir string) (*GroupStore, error) {
	gs := &GroupStore{
		path: filepath.Join(dataDir, groupsFileName),
	}
	if err := gs.load(); err != nil {
		return nil, err
	}
	return gs, nil
}

func (gs *GroupStore) load() error {
	data, err := os.ReadFile(gs.path)
	if os.IsNotExist(err) {
		gs.data = groupsFile{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read groups file: %w", err)
	}
	return json.Unmarshal(data, &gs.data)
}

func (gs *GroupStore) save() error {
	data, err := json.MarshalIndent(gs.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(gs.path, data, 0o600)
}

// ListGroups returns all groups.
func (gs *GroupStore) ListGroups() []Group {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	out := make([]Group, len(gs.data.Groups))
	copy(out, gs.data.Groups)
	return out
}

// CreateGroup creates a new group with the given name.
func (gs *GroupStore) CreateGroup(name string) (Group, error) {
	if name == "" {
		return Group{}, fmt.Errorf("name required")
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	g := Group{
		ID:        genID(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	gs.data.Groups = append(gs.data.Groups, g)
	return g, gs.save()
}

// DeleteGroup removes a group and all its API keys.
func (gs *GroupStore) DeleteGroup(id string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	groups := gs.data.Groups[:0]
	found := false
	for _, g := range gs.data.Groups {
		if g.ID == id {
			found = true
			continue
		}
		groups = append(groups, g)
	}
	if !found {
		return fmt.Errorf("group not found")
	}
	gs.data.Groups = groups
	keys := gs.data.APIKeys[:0]
	for _, k := range gs.data.APIKeys {
		if k.GroupID != id {
			keys = append(keys, k)
		}
	}
	gs.data.APIKeys = keys
	return gs.save()
}

// CreateAPIKey generates a new API key for the given group.
// Returns the plaintext key (shown once) and the stored APIKey record.
func (gs *GroupStore) CreateAPIKey(groupID, name string) (plaintext string, key APIKey, err error) {
	if name == "" {
		return "", APIKey{}, fmt.Errorf("name required")
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	found := false
	for _, g := range gs.data.Groups {
		if g.ID == groupID {
			found = true
			break
		}
	}
	if !found {
		return "", APIKey{}, fmt.Errorf("group not found")
	}
	raw := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", APIKey{}, fmt.Errorf("generate key: %w", err)
	}
	plaintext = apiKeyPrefix + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	key = APIKey{
		ID:        genID(),
		GroupID:   groupID,
		Name:      name,
		KeyHash:   hex.EncodeToString(sum[:]),
		KeyPrefix: plaintext[:12],
		CreatedAt: time.Now().UTC(),
	}
	gs.data.APIKeys = append(gs.data.APIKeys, key)
	return plaintext, key, gs.save()
}

// ListAPIKeys returns all API keys for a group (key hash never exposed).
func (gs *GroupStore) ListAPIKeys(groupID string) []APIKey {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	var out []APIKey
	for _, k := range gs.data.APIKeys {
		if k.GroupID == groupID {
			out = append(out, k)
		}
	}
	return out
}

// RevokeAPIKey removes an API key from a group.
func (gs *GroupStore) RevokeAPIKey(groupID, keyID string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	keys := gs.data.APIKeys[:0]
	found := false
	for _, k := range gs.data.APIKeys {
		if k.GroupID == groupID && k.ID == keyID {
			found = true
			continue
		}
		keys = append(keys, k)
	}
	if !found {
		return fmt.Errorf("key not found")
	}
	gs.data.APIKeys = keys
	return gs.save()
}

// ResolveKey maps a plaintext API key to its group ID.
// Returns ("", false) if the key is not found.
func (gs *GroupStore) ResolveKey(plaintext string) (groupID string, ok bool) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	for _, k := range gs.data.APIKeys {
		if k.KeyHash == hash {
			return k.GroupID, true
		}
	}
	return "", false
}

// HasKeys reports whether any API keys exist in the store.
func (gs *GroupStore) HasKeys() bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return len(gs.data.APIKeys) > 0
}

// KeyCount returns the number of API keys per group ID.
func (gs *GroupStore) KeyCount() map[string]int {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	m := make(map[string]int)
	for _, k := range gs.data.APIKeys {
		m[k.GroupID]++
	}
	return m
}

func genID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
