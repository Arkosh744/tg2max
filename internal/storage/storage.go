package storage

import (
	"context"
	"time"
)

// User represents a Telegram user record.
type User struct {
	TelegramID  int64
	Username    string
	FirstName   string
	LastName    string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// Upload represents a ZIP upload event.
type Upload struct {
	ID           int64
	UserID       int64
	Filename     string
	FileSize     int64
	ExportHash   string
	ChatCount    int
	MessageCount int
	UploadedAt   time.Time
}

// Migration represents a migration run record.
type Migration struct {
	ID              int64
	UserID          int64
	UploadID        int64
	MaxChatID       int64
	MaxChatName     string
	FilterType      string
	FilterMonths    int
	TotalMessages   int
	SentMessages    int
	Status          string // "started", "completed", "failed", "cancelled"
	StartedAt       time.Time
	FinishedAt      *time.Time
	ErrorMessage    string
	DurationSeconds int
}

// UserStats holds aggregate statistics for admin reporting.
type UserStats struct {
	TotalUsers      int
	TotalMigrations int
	Completed       int
	Failed          int
	Cancelled       int
	TotalSent       int
	AvgDurationSec  int
}

// HistoryEntry is a single row in a user's migration history.
type HistoryEntry struct {
	MaxChatName     string
	Status          string
	SentMessages    int
	TotalMessages   int
	DurationSeconds int
	StartedAt       time.Time
}

// MigrationFilter defines pagination and filtering for ListMigrations.
type MigrationFilter struct {
	Status  string // "", "completed", "failed", "cancelled", "started"
	UserID  int64  // 0 = all users
	Page    int
	PerPage int
}

// UserRow is a user with aggregated migration stats.
type UserRow struct {
	TelegramID     int64
	Username       string
	FirstName      string
	LastName       string
	MigrationCount int
	LastActiveAt   time.Time
}

// Storage provides persistent storage for user data, uploads, and migrations.
type Storage interface {
	// UpsertUser creates or updates a user record.
	UpsertUser(ctx context.Context, u User) error

	// SaveUpload records a ZIP upload event, returns the generated upload ID.
	SaveUpload(ctx context.Context, u Upload) (int64, error)

	// StartMigration creates a migration record with status "started".
	StartMigration(ctx context.Context, m Migration) (int64, error)

	// FinishMigration updates an existing migration with final status, counts, and error.
	FinishMigration(ctx context.Context, id int64, status string, sent int, errMsg string) error

	// GetActiveMigration returns the currently running migration (status="started"),
	// or nil if none is active.
	GetActiveMigration(ctx context.Context) (*Migration, error)

	// GetStats returns aggregate statistics for admin reporting.
	GetStats(ctx context.Context) (UserStats, error)

	// GetUserHistory returns the last N migration entries for a given user.
	GetUserHistory(ctx context.Context, userID int64, limit int) ([]HistoryEntry, error)

	// ListMigrations returns paginated migrations with optional filtering.
	// Returns migrations and total count for pagination.
	ListMigrations(ctx context.Context, f MigrationFilter) ([]Migration, int, error)

	// GetMigration returns a single migration by ID.
	GetMigration(ctx context.Context, id int64) (*Migration, error)

	// ListUsers returns paginated users with migration count.
	ListUsers(ctx context.Context, page, perPage int) ([]UserRow, int, error)

	// GetUser returns a single user by telegram ID.
	GetUser(ctx context.Context, telegramID int64) (*User, error)

	// GetRecentMigrations returns the last N migrations for the dashboard.
	GetRecentMigrations(ctx context.Context, limit int) ([]Migration, error)

	// Close closes the storage connection.
	Close() error
}
