package models

import "time"

// LiveMigration is a snapshot of the currently running migration.
// Used by admin UI for live progress display.
type LiveMigration struct {
	UserID        int64
	MaxChatName   string
	TotalMessages int
	SentMessages  int
	Percent       int
	ETA           string
	StartedAt     time.Time
	Elapsed       time.Duration
	Speed         float64 // msg/s
	Paused        bool
}
