package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const policyFileName = "policy.json"

// PolicyRule is one org-wide block or allow entry. Version "*" matches any version.
type PolicyRule struct {
	ID        string    `json:"id"`
	Ecosystem string    `json:"ecosystem"` // "npm", "pypi", or "" for any
	Name      string    `json:"name"`
	Version   string    `json:"version"` // exact version or "*"
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Policy is the org-wide package policy served to agents.
type Policy struct {
	Blocklist []PolicyRule `json:"blocklist"`
	Allowlist []PolicyRule `json:"allowlist"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// PolicyStore persists the org policy to policy.json in dataDir.
type PolicyStore struct {
	path string
	mu   sync.RWMutex
	data Policy
}

func NewPolicyStore(dataDir string) (*PolicyStore, error) {
	ps := &PolicyStore{path: filepath.Join(dataDir, policyFileName)}
	if err := ps.load(); err != nil {
		return nil, err
	}
	return ps, nil
}

func (ps *PolicyStore) load() error {
	data, err := os.ReadFile(ps.path)
	if os.IsNotExist(err) {
		ps.data = Policy{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read policy file: %w", err)
	}
	return json.Unmarshal(data, &ps.data)
}

func (ps *PolicyStore) save() error {
	ps.data.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(ps.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ps.path, data, 0o600)
}

// Get returns a deep copy of the current policy.
func (ps *PolicyStore) Get() Policy {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	out := Policy{UpdatedAt: ps.data.UpdatedAt}
	out.Blocklist = append(out.Blocklist, ps.data.Blocklist...)
	out.Allowlist = append(out.Allowlist, ps.data.Allowlist...)
	return out
}

// AddRule appends a rule to "block" or "allow" list.
func (ps *PolicyStore) AddRule(list string, rule PolicyRule) error {
	if list != "block" && list != "allow" {
		return fmt.Errorf("list must be 'block' or 'allow'")
	}
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("rule name is required")
	}
	if rule.Version == "" {
		rule.Version = "*"
	}
	rule.ID = genID()
	rule.CreatedAt = time.Now().UTC()

	ps.mu.Lock()
	defer ps.mu.Unlock()
	if list == "block" {
		ps.data.Blocklist = append(ps.data.Blocklist, rule)
	} else {
		ps.data.Allowlist = append(ps.data.Allowlist, rule)
	}
	return ps.save()
}

// RemoveRule deletes a rule by ID from the given list.
func (ps *PolicyStore) RemoveRule(list, id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var target *[]PolicyRule
	switch list {
	case "block":
		target = &ps.data.Blocklist
	case "allow":
		target = &ps.data.Allowlist
	default:
		return fmt.Errorf("list must be 'block' or 'allow'")
	}
	out := (*target)[:0]
	found := false
	for _, r := range *target {
		if r.ID == id {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return fmt.Errorf("rule not found")
	}
	*target = out
	return ps.save()
}
