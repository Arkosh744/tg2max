package bot

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arkosh/tg2max/internal/tgclient"
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

	// Clone flow states (userbot channel clone)
	StateAwaitingPhone         State = 10 // Waiting for TG phone number
	StateAwaitingCode          State = 11 // Waiting for TG auth code
	StateAwaitingPassword      State = 12 // Waiting for 2FA password
	StateAwaitingChannelSearch State = 13 // Authenticated, waiting for channel name
	StateAwaitingChannelSelect State = 14 // Channel list shown, waiting for selection
	StateAwaitingDestChoice    State = 15 // Source selected, waiting for TG/Max choice
	StateAwaitingCloneChat     State = 16 // Max chosen, waiting for Max chat selection
	StateAwaitingCloneConfirm  State = 17 // All set, waiting for confirm
	StateCloneMigrating        State = 18 // Clone migration in progress
	StateClonePaused           State = 19 // Clone migration paused
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
	PauseCh chan struct{} // signals pause/resume to the migration goroutine

	// Migration metadata for busy lock and DB logging
	MigrationDBID  int64     // DB migration record ID
	LastUploadID   int64     // DB upload ID for FK
	MigrationStart time.Time // when migration goroutine started
	CursorFile     string    // path to cursor.json for progress reads
	CursorName     string    // cursor chat name for progress lookup

	// Clone rate limiting
	CloneAttempts    int       // number of /clone attempts in current hour
	CloneWindowStart time.Time // when the current rate limit window started

	// Clone flow fields (userbot channel clone)
	TGClient          *tgclient.Client              // MTProto client for this user
	TGAuth            *tgclient.BotConversationAuth  // auth flow coordinator
	TGRunCancel       context.CancelFunc             // cancels the MTProto client Run goroutine
	SourceChannel     *tgclient.ChannelInfo          // selected source TG channel
	DestType          string                         // "max" or "tg"
	CloneChannelID    int64                          // destination channel ID
	CloneChannelName  string                         // destination channel name
	TempMediaDir      string                         // temp dir for downloaded media

	mu sync.Mutex
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
	sess := s.sessions[userID]
	s.mu.RUnlock()
	if sess != nil {
		sess.mu.Lock()
		sess.LastActive = time.Now()
		sess.mu.Unlock()
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

// GetActiveMigration returns the session of the user currently running a migration,
// or nil if no migration is in progress. Only one migration runs at a time (Max API 1 RPS limit).
func (s *SessionStore) GetActiveMigration() *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		sess.mu.Lock()
		state := sess.State
		sess.mu.Unlock()
		if state == StateMigrating || state == StatePaused ||
			state == StateCloneMigrating || state == StateClonePaused {
			return sess
		}
	}
	return nil
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
					if sess.State == StateMigrating || sess.State == StatePaused ||
					sess.State == StateCloneMigrating || sess.State == StateClonePaused {
					continue
				}
					if time.Since(sess.LastActive) > sessionTTL {
						if sess.Cancel != nil {
							sess.Cancel()
						}
						if sess.ExportDir != "" {
							os.RemoveAll(filepath.Dir(sess.ExportDir))
						}
						delete(s.sessions, id)
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}
