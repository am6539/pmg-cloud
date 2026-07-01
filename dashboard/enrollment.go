package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	enrollmentFileName   = "enrollment.json"
	enrollTokenPrefix    = "pmgenroll_"
	enrollTokenRandBytes = 32
)

// EnrollmentToken is a one-time (or limited-use) token used to enroll an agent.
// The plaintext token is returned once at creation time and never stored.
type EnrollmentToken struct {
	ID          string     `json:"id"`
	Label       string     `json:"label,omitempty"`
	TokenHash   string     `json:"token_hash"`
	TokenPrefix string     `json:"token_prefix"` // first 16 chars for display
	GroupID     string     `json:"group_id,omitempty"`
	MaxUses     int        `json:"max_uses"`  // 0 = unlimited
	UseCount    int        `json:"use_count"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	Revoked     bool       `json:"revoked,omitempty"`
}

// Agent is a registered machine that enrolled via an EnrollmentToken.
type Agent struct {
	ID         string     `json:"id"`
	Hostname   string     `json:"hostname"`
	Label      string     `json:"label,omitempty"` // admin-assigned friendly name (e.g. owner/dev)
	OS         string     `json:"os"`
	Arch       string     `json:"arch"`
	PMGVersion string     `json:"pmg_version,omitempty"`
	RemoteIP   string     `json:"remote_ip,omitempty"`
	LocalIP    string     `json:"local_ip,omitempty"`
	GroupID    string     `json:"group_id,omitempty"`
	APIKeyID   string     `json:"api_key_id"`
	EnrolledAt time.Time  `json:"enrolled_at"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	Removed    bool       `json:"removed,omitempty"` // true = agent removed but events kept for audit
}

type enrollmentFile struct {
	Tokens []EnrollmentToken `json:"tokens"`
	Agents []Agent           `json:"agents"`
}

// EnrollmentStore persists enrollment tokens and agents to enrollment.json in dataDir.
type EnrollmentStore struct {
	path string
	mu   sync.RWMutex
	data enrollmentFile
}

// NewEnrollmentStore opens (or creates) enrollment.json in dataDir.
func NewEnrollmentStore(dataDir string) (*EnrollmentStore, error) {
	es := &EnrollmentStore{
		path: filepath.Join(dataDir, enrollmentFileName),
	}
	if err := es.load(); err != nil {
		return nil, err
	}
	return es, nil
}

func (es *EnrollmentStore) load() error {
	data, err := os.ReadFile(es.path)
	if os.IsNotExist(err) {
		es.data = enrollmentFile{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read enrollment file: %w", err)
	}
	return json.Unmarshal(data, &es.data)
}

func (es *EnrollmentStore) save() error {
	data, err := json.MarshalIndent(es.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(es.path, data, 0o600)
}

// CreateToken generates a new enrollment token and stores its hash.
// ttl == 0 means no expiry.
func (es *EnrollmentStore) CreateToken(label, groupID, createdBy string, maxUses int, ttl time.Duration) (plaintext string, t EnrollmentToken, err error) {
	raw := make([]byte, enrollTokenRandBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", EnrollmentToken{}, fmt.Errorf("generate token: %w", err)
	}
	plaintext = enrollTokenPrefix + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))

	t = EnrollmentToken{
		ID:          genID(),
		Label:       label,
		TokenHash:   hex.EncodeToString(sum[:]),
		TokenPrefix: plaintext[:16],
		GroupID:     groupID,
		MaxUses:     maxUses,
		UseCount:    0,
		CreatedAt:   time.Now().UTC(),
		CreatedBy:   createdBy,
	}
	if ttl > 0 {
		exp := time.Now().UTC().Add(ttl)
		t.ExpiresAt = &exp
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	es.data.Tokens = append(es.data.Tokens, t)
	return plaintext, t, es.save()
}

// ListTokens returns all enrollment tokens.
func (es *EnrollmentStore) ListTokens() []EnrollmentToken {
	es.mu.RLock()
	defer es.mu.RUnlock()
	out := make([]EnrollmentToken, len(es.data.Tokens))
	copy(out, es.data.Tokens)
	return out
}

// RevokeToken marks a token as revoked by ID.
func (es *EnrollmentStore) RevokeToken(id string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, t := range es.data.Tokens {
		if t.ID == id {
			es.data.Tokens[i].Revoked = true
			return es.save()
		}
	}
	return fmt.Errorf("token not found")
}

// ValidateAndConsume checks a plaintext token for validity and increments its use count.
func (es *EnrollmentStore) ValidateAndConsume(plaintext string) (EnrollmentToken, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])

	es.mu.Lock()
	defer es.mu.Unlock()
	for i, t := range es.data.Tokens {
		if t.TokenHash != hash {
			continue
		}
		if t.Revoked {
			return EnrollmentToken{}, fmt.Errorf("token has been revoked")
		}
		if t.ExpiresAt != nil && time.Now().UTC().After(*t.ExpiresAt) {
			return EnrollmentToken{}, fmt.Errorf("token has expired")
		}
		if t.MaxUses > 0 && t.UseCount >= t.MaxUses {
			return EnrollmentToken{}, fmt.Errorf("token has reached its maximum use count")
		}
		es.data.Tokens[i].UseCount++
		if err := es.save(); err != nil {
			return EnrollmentToken{}, fmt.Errorf("persist token use: %w", err)
		}
		return es.data.Tokens[i], nil
	}
	return EnrollmentToken{}, fmt.Errorf("invalid token")
}

// RegisterAgent stores a new agent record.
func (es *EnrollmentStore) RegisterAgent(a Agent) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.data.Agents = append(es.data.Agents, a)
	return es.save()
}

// ListAgents returns all registered (non-removed) agents.
func (es *EnrollmentStore) ListAgents() []Agent {
	es.mu.RLock()
	defer es.mu.RUnlock()
	var out []Agent
	for _, a := range es.data.Agents {
		if !a.Removed {
			out = append(out, a)
		}
	}
	return out
}

// ListAllAgents returns all agents including removed ones (for audit/endpoints).
func (es *EnrollmentStore) ListAllAgents() []Agent {
	es.mu.RLock()
	defer es.mu.RUnlock()
	out := make([]Agent, len(es.data.Agents))
	copy(out, es.data.Agents)
	return out
}

// GetAgentByID returns an agent by ID (including removed agents) and a found flag.
func (es *EnrollmentStore) GetAgentByID(id string) (Agent, bool) {
	es.mu.RLock()
	defer es.mu.RUnlock()
	for _, a := range es.data.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// FindActiveAgentByHostnameAndIP returns the first non-removed agent whose
// hostname (case-insensitive) and local IP both match. Used to detect a
// re-enrolling machine so /api/enroll can update it instead of creating a
// duplicate. Hostname alone is not a reliable identifier — distinct
// physical machines can share a hostname — so an empty localIP always
// misses rather than risk merging unrelated agents.
func (es *EnrollmentStore) FindActiveAgentByHostnameAndIP(hostname, localIP string) (Agent, bool) {
	if localIP == "" {
		return Agent{}, false
	}
	es.mu.RLock()
	defer es.mu.RUnlock()
	for _, a := range es.data.Agents {
		if a.Removed {
			continue
		}
		if strings.EqualFold(a.Hostname, hostname) && a.LocalIP == localIP {
			return a, true
		}
	}
	return Agent{}, false
}

// ReenrollAgent updates an existing agent's connection/enrollment metadata
// in place. ID, EnrolledAt, and Label are left untouched so re-enrollment
// history and any admin-assigned name survive.
func (es *EnrollmentStore) ReenrollAgent(id, os, arch, pmgVersion, remoteIP, localIP, groupID, apiKeyID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == id {
			es.data.Agents[i].OS = os
			es.data.Agents[i].Arch = arch
			es.data.Agents[i].PMGVersion = pmgVersion
			es.data.Agents[i].RemoteIP = remoteIP
			es.data.Agents[i].LocalIP = localIP
			es.data.Agents[i].GroupID = groupID
			es.data.Agents[i].APIKeyID = apiKeyID
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}

// AssignAgentGroup updates an agent's group assignment.
func (es *EnrollmentStore) AssignAgentGroup(agentID, groupID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == agentID {
			es.data.Agents[i].GroupID = groupID
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}

// SetAgentLabel updates an agent's admin-assigned friendly name.
func (es *EnrollmentStore) SetAgentLabel(agentID, label string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == agentID {
			es.data.Agents[i].Label = label
			return es.save()
		}
	}
	return fmt.Errorf("agent not found")
}

// TouchAgentByAPIKeyID updates the LastSeen timestamp (and PMGVersion, LocalIP,
// RemoteIP when non-empty) of the agent whose APIKeyID matches keyID.
// Called on every successful sync and heartbeat so the Agents tab reflects
// live activity and the currently-running PMG version. Returns nil (not an
// error) if no agent matches, since a touch miss is non-fatal.
func (es *EnrollmentStore) TouchAgentByAPIKeyID(keyID, version, localIP, remoteIP string) error {
	if keyID == "" {
		return nil
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	now := time.Now().UTC()
	for i, a := range es.data.Agents {
		if a.APIKeyID == keyID {
			es.data.Agents[i].LastSeen = &now
			if version != "" {
				es.data.Agents[i].PMGVersion = version
			}
			if localIP != "" {
				es.data.Agents[i].LocalIP = localIP
			}
			if remoteIP != "" {
				es.data.Agents[i].RemoteIP = remoteIP
			}
			return es.save()
		}
	}
	return nil
}

// RemoveAgent marks an agent as removed (keeps events for audit).
func (es *EnrollmentStore) RemoveAgent(id string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	for i, a := range es.data.Agents {
		if a.ID == id {
			es.data.Agents[i].Removed = true
			return es.save()
		}
	}
	// Orphan endpoint (events exist but no enrollment record) - return success
	// since the goal is to mark as removed, and non-existent is effectively removed
	return nil
}
