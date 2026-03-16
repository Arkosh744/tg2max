package bot

import (
	"context"
	"sync"
)

type State int

const (
	StateIdle            State = iota // Nothing happening
	StateAwaitingExport               // Waiting for ZIP upload
	StateAwaitingChatID              // ZIP uploaded, waiting for Max chat ID
	StateAwaitingConfirm             // Preview shown, waiting for confirm
	StateMigrating                   // Migration in progress
)

type Session struct {
	UserID     int64
	MaxChatID  int64
	ExportPath string // path to extracted result.json
	ExportDir  string // temp dir with extracted files
	State      State
	Cancel     context.CancelFunc
	mu         sync.Mutex
}

type SessionStore struct {
	sessions map[int64]*Session
	mu       sync.RWMutex
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[int64]*Session),
	}
}

func (s *SessionStore) Get(userID int64) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[userID]
}

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

func (s *SessionStore) Delete(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[userID]; ok && sess.Cancel != nil {
		sess.Cancel()
	}
	delete(s.sessions, userID)
}
