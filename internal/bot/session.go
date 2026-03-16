package bot

import (
	"context"
	"sync"
)

// State represents the current phase of a user's bot interaction.
type State int

const (
	StateIdle State = iota
	StateAwaitingExport
	StateMigrating
)

// Session holds per-user state during the migration workflow.
type Session struct {
	UserID    int64
	MaxChatID int64
	ExportPath string // path to extracted result.json
	ExportDir  string // temp dir with extracted files
	State      State
	Cancel     context.CancelFunc
	mu         sync.Mutex
}

// SetState updates the session state in a thread-safe manner.
func (s *Session) SetState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
}

// GetState returns the current session state in a thread-safe manner.
func (s *Session) GetState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.State
}

// SessionStore manages active user sessions.
type SessionStore struct {
	sessions map[int64]*Session
	mu       sync.RWMutex
}

// NewSessionStore creates an empty session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[int64]*Session),
	}
}

// Get returns a session for the given user or nil if none exists.
func (s *SessionStore) Get(userID int64) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[userID]
}

// GetOrCreate returns an existing session or creates a new one in Idle state.
func (s *SessionStore) GetOrCreate(userID int64) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[userID]; ok {
		return sess
	}
	sess := &Session{
		UserID: userID,
		State:  StateIdle,
	}
	s.sessions[userID] = sess
	return sess
}

// Delete removes a session and cancels any running context.
func (s *SessionStore) Delete(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[userID]; ok && sess.Cancel != nil {
		sess.Cancel()
	}
	delete(s.sessions, userID)
}
