package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/arkosh/tg2max/internal/storage"
	"github.com/arkosh/tg2max/pkg/models"
)

type Bot struct {
	api            *tgbotapi.BotAPI
	sessions       *SessionStore
	storage        storage.Storage
	maxToken       string
	rps            float64
	tempDir        string
	tgAPIEndpoint  string
	tgAPIFilesDir  string
	allowedUserIDs map[int64]struct{}
	adminUserIDs   map[int64]struct{}
	waitingUsers   []int64 // users waiting for busy lock to clear
	log            *slog.Logger
	startedAt      time.Time
}

type Config struct {
	TelegramToken  string
	MaxToken       string
	RateLimitRPS   float64
	TempDir        string
	TGAPIEndpoint  string  // Local Bot API server URL, e.g. "http://localhost:8081"
	TGAPIFilesDir  string  // Host path to local Bot API files volume
	AllowedUserIDs []int64 // if empty, open to everyone (NOT recommended for production)
	AdminUserIDs   []int64 // users with access to /stats; defaults to AllowedUserIDs
	DBPath         string  // path to SQLite database; empty = no persistent storage
}

func New(cfg Config, log *slog.Logger) (*Bot, error) {
	var api *tgbotapi.BotAPI
	var err error
	if cfg.TGAPIEndpoint != "" {
		api, err = tgbotapi.NewBotAPIWithAPIEndpoint(cfg.TelegramToken, cfg.TGAPIEndpoint+"/bot%s/%s")
	} else {
		api, err = tgbotapi.NewBotAPI(cfg.TelegramToken)
	}
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}

	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if err := os.MkdirAll(cfg.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	if cfg.RateLimitRPS <= 0 {
		cfg.RateLimitRPS = 1
	}

	allowed := make(map[int64]struct{}, len(cfg.AllowedUserIDs))
	for _, id := range cfg.AllowedUserIDs {
		allowed[id] = struct{}{}
	}

	admins := make(map[int64]struct{}, len(cfg.AdminUserIDs))
	if len(cfg.AdminUserIDs) > 0 {
		for _, id := range cfg.AdminUserIDs {
			admins[id] = struct{}{}
		}
	} else {
		// Default: all allowed users are admins
		for id := range allowed {
			admins[id] = struct{}{}
		}
	}

	if len(cfg.AllowedUserIDs) == 0 {
		log.Warn("SECURITY: allowed_user_ids is empty — bot is open to ALL Telegram users")
	}

	var store storage.Storage
	if cfg.DBPath != "" {
		store, err = storage.NewSQLite(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
	} else {
		store = storage.Nop{}
	}

	return &Bot{
		api:            api,
		sessions:       NewSessionStore(),
		storage:        store,
		maxToken:       cfg.MaxToken,
		rps:            cfg.RateLimitRPS,
		tempDir:        cfg.TempDir,
		tgAPIEndpoint:  cfg.TGAPIEndpoint,
		tgAPIFilesDir:  cfg.TGAPIFilesDir,
		allowedUserIDs: allowed,
		adminUserIDs:   admins,
		log:            log,
		startedAt:      time.Now(),
	}, nil
}

// Close releases resources held by the bot (database connection, etc.).
func (b *Bot) Close() error {
	return b.storage.Close()
}

// Storage returns the bot's persistent storage (for admin UI).
func (b *Bot) Storage() storage.Storage {
	return b.storage
}

// Uptime returns how long the bot has been running.
func (b *Bot) Uptime() time.Duration {
	return time.Since(b.startedAt)
}

// ActiveMigration returns a snapshot of the currently running migration, or nil.
func (b *Bot) ActiveMigration() *models.LiveMigration {
	sess := b.sessions.GetActiveMigration()
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	cursorFile := sess.CursorFile
	cursorName := sess.CursorName
	startedAt := sess.MigrationStart
	maxChat := sess.MaxChatName
	userID := sess.UserID
	paused := sess.State == StatePaused
	sess.mu.Unlock()

	sent, total := readCursorProgress(cursorFile, cursorName)
	pct := 0
	if total > 0 {
		pct = sent * 100 / total
	}

	var speed float64
	eta := ""
	elapsed := time.Since(startedAt)
	if sent > 0 && elapsed.Seconds() > 0 {
		speed = float64(sent) / elapsed.Seconds()
		remaining := time.Duration(float64(total-sent) / speed * float64(time.Second))
		remaining = remaining.Round(time.Minute)
		if remaining < time.Minute {
			eta = "~1 мин"
		} else {
			hours := int(remaining.Hours())
			minutes := int(remaining.Minutes()) % 60
			if hours > 0 {
				eta = fmt.Sprintf("~%d ч %d мин", hours, minutes)
			} else {
				eta = fmt.Sprintf("~%d мин", minutes)
			}
		}
	}

	return &models.LiveMigration{
		UserID:        userID,
		MaxChatName:   maxChat,
		TotalMessages: total,
		SentMessages:  sent,
		Percent:       pct,
		ETA:           eta,
		StartedAt:     startedAt,
		Elapsed:       elapsed.Round(time.Second),
		Speed:         speed,
		Paused:        paused,
	}
}

func (b *Bot) isAuthorized(userID int64) bool {
	if len(b.allowedUserIDs) == 0 {
		return true
	}
	_, ok := b.allowedUserIDs[userID]
	return ok
}

func (b *Bot) isAdmin(userID int64) bool {
	_, ok := b.adminUserIDs[userID]
	return ok
}

func (b *Bot) Run(ctx context.Context) error {
	b.log.Info("bot started", "username", b.api.Self.UserName)
	b.sessions.StartCleanup(ctx)

	// Crash recovery: mark orphaned migrations as failed
	if active, err := b.storage.GetActiveMigration(ctx); err == nil && active != nil {
		b.log.Warn("found orphaned migration from previous crash", "id", active.ID, "user", active.UserID)
		if err := b.storage.FinishMigration(ctx, active.ID, "failed", active.SentMessages, "process crashed"); err != nil {
			b.log.Error("failed to recover orphaned migration", "error", err)
		}
	}

	// Register bot commands in Telegram menu
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начать работу"},
		tgbotapi.BotCommand{Command: "help", Description: "Справка"},
		tgbotapi.BotCommand{Command: "status", Description: "Текущий статус"},
		tgbotapi.BotCommand{Command: "history", Description: "История миграций"},
		tgbotapi.BotCommand{Command: "cancel", Description: "Отменить миграцию"},
		tgbotapi.BotCommand{Command: "reset", Description: "Сбросить сессию"},
		tgbotapi.BotCommand{Command: "stats", Description: "Статистика (админ)"},
	)
	b.api.Request(commands)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case update := <-updates:
			if update.CallbackQuery != nil {
				b.handleCallback(ctx, update.CallbackQuery)
				continue
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}
