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
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

const usersFileName = "users.json"

// DashUser is a CMS user account.
type DashUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash,omitempty"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	MustChange   bool      `json:"must_change_password,omitempty"`
}

type userStoreData struct {
	Users []DashUser `json:"users"`
}

// UserStore manages CMS user accounts persisted to users.json.
type UserStore struct {
	path string
	mu   sync.RWMutex
	data userStoreData
}

// NewUserStore opens (or creates) users.json in dataDir.
// If no users exist, a default admin/admin account is created with MustChange=true.
// If seedUser/seedPass are non-empty they are used instead of admin/admin.
func NewUserStore(dataDir, seedUser, seedPass string) (*UserStore, error) {
	us := &UserStore{path: filepath.Join(dataDir, usersFileName)}
	if err := us.load(); err != nil {
		return nil, err
	}
	if len(us.data.Users) == 0 {
		user := "admin"
		pass := "admin"
		if seedUser != "" {
			user = seedUser
		}
		if seedPass != "" {
			pass = seedPass
		}
		mustChange := pass == "admin"
		if _, err := us.unsafeCreate(user, pass, RoleAdmin, mustChange); err != nil {
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}
	return us, nil
}

func (us *UserStore) load() error {
	data, err := os.ReadFile(us.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read users file: %w", err)
	}
	return json.Unmarshal(data, &us.data)
}

func (us *UserStore) save() error {
	data, err := json.MarshalIndent(us.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(us.path, data, 0o600)
}

// hashPassword hashes password with a 16-byte random salt using SHA-256.
// Format: "<hex-salt>:<hex-hash>"
func hashPassword(password string) (string, error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(saltBytes) // 32 hex chars
	h := sha256.Sum256([]byte(salt + password))
	return salt + ":" + hex.EncodeToString(h[:]), nil
}

// checkPassword verifies plaintext against stored "salt:hash".
func checkPassword(stored, password string) bool {
	if len(stored) < 65 { // 32 salt + 1 colon + 64 hash
		return false
	}
	salt := stored[:32]
	if stored[32] != ':' {
		return false
	}
	expected := stored[33:]
	h := sha256.Sum256([]byte(salt + password))
	got := hex.EncodeToString(h[:])
	// constant-time comparison
	if len(got) != len(expected) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ expected[i]
	}
	return diff == 0
}

// unsafeCreate creates a user without holding the mutex (caller must handle locking).
func (us *UserStore) unsafeCreate(username, password, role string, mustChange bool) (DashUser, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return DashUser{}, err
	}
	u := DashUser{
		ID:           genID(),
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
		MustChange:   mustChange,
	}
	us.data.Users = append(us.data.Users, u)
	return u, us.save()
}

// ListUsers returns all users without password hashes.
func (us *UserStore) ListUsers() []DashUser {
	us.mu.RLock()
	defer us.mu.RUnlock()
	out := make([]DashUser, len(us.data.Users))
	for i, u := range us.data.Users {
		u.PasswordHash = ""
		out[i] = u
	}
	return out
}

// FindByUsername returns a user by username (includes hash for auth).
func (us *UserStore) FindByUsername(username string) (DashUser, bool) {
	us.mu.RLock()
	defer us.mu.RUnlock()
	for _, u := range us.data.Users {
		if u.Username == username {
			return u, true
		}
	}
	return DashUser{}, false
}

// FindByID returns a user by ID (includes hash).
func (us *UserStore) FindByID(id string) (DashUser, bool) {
	us.mu.RLock()
	defer us.mu.RUnlock()
	for _, u := range us.data.Users {
		if u.ID == id {
			return u, true
		}
	}
	return DashUser{}, false
}

// CheckPassword validates credentials and returns the user on success.
func (us *UserStore) CheckPassword(username, password string) (DashUser, bool) {
	u, ok := us.FindByUsername(username)
	if !ok {
		return DashUser{}, false
	}
	return u, checkPassword(u.PasswordHash, password)
}

// CreateUser adds a new user account.
func (us *UserStore) CreateUser(username, password, role string) (DashUser, error) {
	if username == "" {
		return DashUser{}, fmt.Errorf("username required")
	}
	if password == "" {
		return DashUser{}, fmt.Errorf("password required")
	}
	if role != RoleAdmin && role != RoleEditor && role != RoleViewer {
		return DashUser{}, fmt.Errorf("role must be admin, editor, or viewer")
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	for _, u := range us.data.Users {
		if u.Username == username {
			return DashUser{}, fmt.Errorf("username already exists")
		}
	}
	return us.unsafeCreate(username, password, role, false)
}

// UpdatePassword changes a user's password and clears MustChange.
func (us *UserStore) UpdatePassword(id, newPassword string) error {
	if newPassword == "" {
		return fmt.Errorf("password required")
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	for i, u := range us.data.Users {
		if u.ID == id {
			us.data.Users[i].PasswordHash = hash
			us.data.Users[i].MustChange = false
			return us.save()
		}
	}
	return fmt.Errorf("user not found")
}

// UpdateRole changes a user's role.
func (us *UserStore) UpdateRole(id, role string) error {
	if role != RoleAdmin && role != RoleEditor && role != RoleViewer {
		return fmt.Errorf("role must be admin, editor, or viewer")
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	for i, u := range us.data.Users {
		if u.ID == id {
			us.data.Users[i].Role = role
			return us.save()
		}
	}
	return fmt.Errorf("user not found")
}

// DeleteUser removes a user by ID, preventing deletion of the last admin.
func (us *UserStore) DeleteUser(id string) error {
	us.mu.Lock()
	defer us.mu.Unlock()
	users := make([]DashUser, 0, len(us.data.Users))
	found := false
	for _, u := range us.data.Users {
		if u.ID == id {
			found = true
			continue
		}
		users = append(users, u)
	}
	if !found {
		return fmt.Errorf("user not found")
	}
	hasAdmin := false
	for _, u := range users {
		if u.Role == RoleAdmin {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		return fmt.Errorf("cannot delete the last admin account")
	}
	us.data.Users = users
	return us.save()
}

// --- Session store ---

const (
	sessionTTL        = 8 * time.Hour
	SessionCookieName = "pmg_session"
)

// DashSession is an active login session.
type DashSession struct {
	UserID    string
	Username  string
	Role      string
	ExpiresAt time.Time
}

// SessionStore is an in-memory session store with automatic expiry cleanup.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]DashSession
}

func NewSessionStore() *SessionStore {
	ss := &SessionStore{sessions: make(map[string]DashSession)}
	go ss.cleanupLoop()
	return ss
}

// Create issues a new session for the given user and returns the session ID.
func (ss *SessionStore) Create(u DashUser) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	ss.mu.Lock()
	ss.sessions[id] = DashSession{
		UserID:    u.ID,
		Username:  u.Username,
		Role:      u.Role,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
	ss.mu.Unlock()
	return id, nil
}

// Get returns a valid session or (zero, false) if missing or expired.
func (ss *SessionStore) Get(id string) (DashSession, bool) {
	ss.mu.RLock()
	s, ok := ss.sessions[id]
	ss.mu.RUnlock()
	if !ok || time.Now().After(s.ExpiresAt) {
		return DashSession{}, false
	}
	return s, true
}

// Delete removes a session by ID.
func (ss *SessionStore) Delete(id string) {
	ss.mu.Lock()
	delete(ss.sessions, id)
	ss.mu.Unlock()
}

// DeleteByUser removes all sessions belonging to userID.
func (ss *SessionStore) DeleteByUser(userID string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for id, s := range ss.sessions {
		if s.UserID == userID {
			delete(ss.sessions, id)
		}
	}
}

// DeleteByUserExcept removes all sessions for userID except the given session ID.
func (ss *SessionStore) DeleteByUserExcept(userID, exceptSID string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for id, s := range ss.sessions {
		if s.UserID == userID && id != exceptSID {
			delete(ss.sessions, id)
		}
	}
}

func (ss *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ss.mu.Lock()
		now := time.Now()
		for id, s := range ss.sessions {
			if now.After(s.ExpiresAt) {
				delete(ss.sessions, id)
			}
		}
		ss.mu.Unlock()
	}
}
