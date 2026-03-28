package bot

import (
	"context"
	"sync"
	"time"
)

type State int

const (
	StateIdle              State = iota // Nothing happening
	StateAwaitingChatSearch            // ZIP uploaded, waiting for chat name to search
	StateAwaitingChatID                // Fallback: waiting for manual Max chat ID
	StateAwaitingFilter                // Max chat selected, waiting for filter choice
	StateAwaitingConfirm               // Preview shown, waiting for confirm
	StateMigrating                     // Migration in progress
	StatePaused                        // Migration paused, waiting for resume
)

const sessionTTL = 24 * time.Hour

type Session struct {
	UserID         int64
	MaxChatID      int64
	MaxChatName    string // human-readable name of the selected Max chat
	ExportPath     string // path to extracted result.json
	ExportDir      string // temp dir with extracted files
	ExportHash     string // SHA-256 of result.json, used to skip re-analysis on re-upload
	State          State
	Cancel         context.CancelFunc
	LastActive     time.Time
	FilterType     string // "all", "text", "media" — default means all
	FilterMonths   int    // 0 = all time, 3 = last 3 months, 6 = last 6 months
	PauseCh        chan struct{} // signals pause/resume to the migration goroutine
	FailedMediaIDs []int        // message IDs where media upload failed
	mu             sync.Mutex
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
	sess := s.sessions[userID]
	if sess != nil {
		sess.LastActive = time.Now()
	}
	return sess
}

func (s *SessionStore) GetOrCreate(userID int64) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[userID]; ok {
		sess.LastActive = time.Now()
		return sess
	}
	sess := &Session{
		UserID:     userID,
		State:      StateIdle,
		LastActive: time.Now(),
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

// StartCleanup removes sessions inactive for longer than sessionTTL.
func (s *SessionStore) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				for id, sess := range s.sessions {
					if sess.State == StateMigrating || sess.State == StatePaused {
						continue
					}
					if time.Since(sess.LastActive) > sessionTTL {
						if sess.Cancel != nil {
							sess.Cancel()
						}
						delete(s.sessions, id)
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}
