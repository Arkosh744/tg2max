package storage

import "context"

// Nop is a no-op storage that discards all writes.
// Used when no db_path is configured.
type Nop struct{}

func (Nop) UpsertUser(context.Context, User) error                              { return nil }
func (Nop) SaveUpload(context.Context, Upload) (int64, error)                   { return 0, nil }
func (Nop) StartMigration(context.Context, Migration) (int64, error)            { return 0, nil }
func (Nop) FinishMigration(context.Context, int64, string, int, string) error   { return nil }
func (Nop) GetActiveMigration(context.Context) (*Migration, error)              { return nil, nil }
func (Nop) GetStats(context.Context) (UserStats, error)                         { return UserStats{}, nil }
func (Nop) GetUserHistory(context.Context, int64, int) ([]HistoryEntry, error)          { return nil, nil }
func (Nop) ListMigrations(context.Context, MigrationFilter) ([]Migration, int, error)  { return nil, 0, nil }
func (Nop) GetMigration(context.Context, int64) (*Migration, error)                    { return nil, nil }
func (Nop) ListUsers(context.Context, int, int) ([]UserRow, int, error)                { return nil, 0, nil }
func (Nop) GetUser(context.Context, int64) (*User, error)                              { return nil, nil }
func (Nop) GetDailyStats(context.Context, int) ([]DailyStat, error)                    { return nil, nil }
func (Nop) GetRecentMigrations(context.Context, int) ([]Migration, error)              { return nil, nil }
func (Nop) Close() error                                                               { return nil }
