package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Schema migrations applied sequentially. Each element is a single SQL batch.
var migrations = []string{
	// v1: initial schema — users, uploads, migrations
	`CREATE TABLE IF NOT EXISTS users (
		telegram_id   INTEGER PRIMARY KEY,
		username      TEXT NOT NULL DEFAULT '',
		first_name    TEXT NOT NULL DEFAULT '',
		last_name     TEXT NOT NULL DEFAULT '',
		first_seen_at TEXT NOT NULL,
		last_seen_at  TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS uploads (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL REFERENCES users(telegram_id),
		filename      TEXT NOT NULL DEFAULT '',
		file_size     INTEGER NOT NULL DEFAULT 0,
		export_hash   TEXT NOT NULL DEFAULT '',
		chat_count    INTEGER NOT NULL DEFAULT 0,
		message_count INTEGER NOT NULL DEFAULT 0,
		uploaded_at   TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS migrations (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id          INTEGER NOT NULL REFERENCES users(telegram_id),
		upload_id        INTEGER NOT NULL DEFAULT 0,
		max_chat_id      INTEGER NOT NULL,
		max_chat_name    TEXT NOT NULL DEFAULT '',
		filter_type      TEXT NOT NULL DEFAULT '',
		filter_months    INTEGER NOT NULL DEFAULT 0,
		total_messages   INTEGER NOT NULL DEFAULT 0,
		sent_messages    INTEGER NOT NULL DEFAULT 0,
		status           TEXT NOT NULL DEFAULT 'started',
		started_at       TEXT NOT NULL,
		finished_at      TEXT,
		error_message    TEXT NOT NULL DEFAULT '',
		duration_seconds INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_migrations_status ON migrations(status);
	CREATE INDEX IF NOT EXISTS idx_migrations_user   ON migrations(user_id);
	CREATE INDEX IF NOT EXISTS idx_uploads_user      ON uploads(user_id);`,

	// v2: userbot sessions + clone migration metadata
	`CREATE TABLE IF NOT EXISTS userbot_sessions (
		user_id      INTEGER PRIMARY KEY REFERENCES users(telegram_id),
		session_data BLOB NOT NULL,
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL
	);

	ALTER TABLE migrations ADD COLUMN source_type TEXT NOT NULL DEFAULT 'zip';
	ALTER TABLE migrations ADD COLUMN source_channel TEXT NOT NULL DEFAULT '';
	ALTER TABLE migrations ADD COLUMN dest_type TEXT NOT NULL DEFAULT 'max';`,
}

// SQLite implements Storage using a local SQLite database.
type SQLite struct {
	db *sql.DB
}

// NewSQLite opens (or creates) a SQLite database at dbPath and applies pending migrations.
func NewSQLite(dbPath string) (*SQLite, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i, m := range migrations {
		ver := i + 1
		if ver <= current {
			continue
		}
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("apply migration %d: %w", ver, err)
		}
		if _, err := s.db.Exec("INSERT INTO schema_version (version) VALUES (?)", ver); err != nil {
			return fmt.Errorf("record migration %d: %w", ver, err)
		}
	}
	return nil
}

func (s *SQLite) UpsertUser(ctx context.Context, u User) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (telegram_id, username, first_name, last_name, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(telegram_id) DO UPDATE SET
			username     = excluded.username,
			first_name   = excluded.first_name,
			last_name    = excluded.last_name,
			last_seen_at = ?`,
		u.TelegramID, u.Username, u.FirstName, u.LastName, now, now, now)
	if err != nil {
		return fmt.Errorf("upsert user %d: %w", u.TelegramID, err)
	}
	return nil
}

func (s *SQLite) SaveUpload(ctx context.Context, u Upload) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO uploads (user_id, filename, file_size, export_hash, chat_count, message_count, uploaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.UserID, u.Filename, u.FileSize, u.ExportHash, u.ChatCount, u.MessageCount,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("save upload for user %d: %w", u.UserID, err)
	}
	return res.LastInsertId()
}

func (s *SQLite) StartMigration(ctx context.Context, m Migration) (int64, error) {
	sourceType := m.SourceType
	if sourceType == "" {
		sourceType = "zip"
	}
	destType := m.DestType
	if destType == "" {
		destType = "max"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO migrations (user_id, upload_id, max_chat_id, max_chat_name,
			filter_type, filter_months, total_messages, status, started_at,
			source_type, source_channel, dest_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'started', ?, ?, ?, ?)`,
		m.UserID, m.UploadID, m.MaxChatID, m.MaxChatName,
		m.FilterType, m.FilterMonths, m.TotalMessages,
		time.Now().UTC().Format(time.RFC3339),
		sourceType, m.SourceChannel, destType)
	if err != nil {
		return 0, fmt.Errorf("start migration for user %d: %w", m.UserID, err)
	}
	return res.LastInsertId()
}

func (s *SQLite) FinishMigration(ctx context.Context, id int64, status string, sent int, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE migrations SET
			status           = ?,
			sent_messages    = ?,
			error_message    = ?,
			finished_at      = ?,
			duration_seconds = CAST((julianday(?) - julianday(started_at)) * 86400 AS INTEGER)
		WHERE id = ?`,
		status, sent, errMsg, now, now, id)
	if err != nil {
		return fmt.Errorf("finish migration %d: %w", id, err)
	}
	return nil
}

func (s *SQLite) GetActiveMigration(ctx context.Context) (*Migration, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, max_chat_id, max_chat_name, total_messages, sent_messages, started_at
		FROM migrations WHERE status = 'started'
		ORDER BY id DESC LIMIT 1`)

	var m Migration
	var startedAt string
	err := row.Scan(&m.ID, &m.UserID, &m.MaxChatID, &m.MaxChatName,
		&m.TotalMessages, &m.SentMessages, &startedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active migration: %w", err)
	}
	if t, parseErr := time.Parse(time.RFC3339, startedAt); parseErr == nil {
		m.StartedAt = t
	}
	m.Status = "started"
	return &m, nil
}

func (s *SQLite) GetStats(ctx context.Context) (UserStats, error) {
	var st UserStats
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users),
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed'    THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'cancelled'  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(sent_messages), 0),
			COALESCE(AVG(CASE WHEN status = 'completed' THEN duration_seconds END), 0)
		FROM migrations`).Scan(
		&st.TotalUsers, &st.TotalMigrations,
		&st.Completed, &st.Failed, &st.Cancelled,
		&st.TotalSent, &st.AvgDurationSec)
	if err != nil {
		return st, fmt.Errorf("get stats: %w", err)
	}
	return st, nil
}

func (s *SQLite) GetUserHistory(ctx context.Context, userID int64, limit int) ([]HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT max_chat_name, status, sent_messages, total_messages, duration_seconds, started_at
		FROM migrations
		WHERE user_id = ?
		ORDER BY id DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get history for user %d: %w", userID, err)
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var startedAt string
		if err := rows.Scan(&e.MaxChatName, &e.Status, &e.SentMessages, &e.TotalMessages, &e.DurationSeconds, &startedAt); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		if t, parseErr := time.Parse(time.RFC3339, startedAt); parseErr == nil {
			e.StartedAt = t
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLite) ListMigrations(ctx context.Context, f MigrationFilter) ([]Migration, int, error) {
	if f.PerPage <= 0 {
		f.PerPage = 20
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	offset := (f.Page - 1) * f.PerPage

	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM migrations
		WHERE (? = '' OR status = ?) AND (? = 0 OR user_id = ?)`,
		f.Status, f.Status, f.UserID, f.UserID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count migrations: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, upload_id, max_chat_id, max_chat_name,
			filter_type, filter_months, total_messages, sent_messages,
			status, started_at, COALESCE(finished_at, ''), error_message, duration_seconds,
			COALESCE(source_type, 'zip'), COALESCE(source_channel, ''), COALESCE(dest_type, 'max')
		FROM migrations
		WHERE (? = '' OR status = ?) AND (? = 0 OR user_id = ?)
		ORDER BY id DESC
		LIMIT ? OFFSET ?`,
		f.Status, f.Status, f.UserID, f.UserID, f.PerPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()

	var result []Migration
	for rows.Next() {
		var m Migration
		var startedAt, finishedAt string
		if err := rows.Scan(&m.ID, &m.UserID, &m.UploadID, &m.MaxChatID, &m.MaxChatName,
			&m.FilterType, &m.FilterMonths, &m.TotalMessages, &m.SentMessages,
			&m.Status, &startedAt, &finishedAt, &m.ErrorMessage, &m.DurationSeconds,
			&m.SourceType, &m.SourceChannel, &m.DestType); err != nil {
			return nil, 0, fmt.Errorf("scan migration: %w", err)
		}
		if t, e := time.Parse(time.RFC3339, startedAt); e == nil {
			m.StartedAt = t
		}
		if t, e := time.Parse(time.RFC3339, finishedAt); e == nil {
			m.FinishedAt = &t
		}
		result = append(result, m)
	}
	return result, total, rows.Err()
}

func (s *SQLite) GetMigration(ctx context.Context, id int64) (*Migration, error) {
	var m Migration
	var startedAt, finishedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, upload_id, max_chat_id, max_chat_name,
			filter_type, filter_months, total_messages, sent_messages,
			status, started_at, COALESCE(finished_at, ''), error_message, duration_seconds,
			COALESCE(source_type, 'zip'), COALESCE(source_channel, ''), COALESCE(dest_type, 'max')
		FROM migrations WHERE id = ?`, id).
		Scan(&m.ID, &m.UserID, &m.UploadID, &m.MaxChatID, &m.MaxChatName,
			&m.FilterType, &m.FilterMonths, &m.TotalMessages, &m.SentMessages,
			&m.Status, &startedAt, &finishedAt, &m.ErrorMessage, &m.DurationSeconds,
			&m.SourceType, &m.SourceChannel, &m.DestType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get migration %d: %w", id, err)
	}
	if t, e := time.Parse(time.RFC3339, startedAt); e == nil {
		m.StartedAt = t
	}
	if t, e := time.Parse(time.RFC3339, finishedAt); e == nil {
		m.FinishedAt = &t
	}
	return &m, nil
}

func (s *SQLite) ListUsers(ctx context.Context, page, perPage int) ([]UserRow, int, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * perPage

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT u.telegram_id, u.username, u.first_name, u.last_name,
			COALESCE((SELECT COUNT(*) FROM migrations m WHERE m.user_id = u.telegram_id), 0),
			u.last_seen_at
		FROM users u
		ORDER BY u.last_seen_at DESC
		LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var result []UserRow
	for rows.Next() {
		var u UserRow
		var lastSeen string
		if err := rows.Scan(&u.TelegramID, &u.Username, &u.FirstName, &u.LastName,
			&u.MigrationCount, &lastSeen); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		if t, e := time.Parse(time.RFC3339, lastSeen); e == nil {
			u.LastActiveAt = t
		}
		result = append(result, u)
	}
	return result, total, rows.Err()
}

func (s *SQLite) GetUser(ctx context.Context, telegramID int64) (*User, error) {
	var u User
	var firstSeen, lastSeen string
	err := s.db.QueryRowContext(ctx, `
		SELECT telegram_id, username, first_name, last_name, first_seen_at, last_seen_at
		FROM users WHERE telegram_id = ?`, telegramID).
		Scan(&u.TelegramID, &u.Username, &u.FirstName, &u.LastName, &firstSeen, &lastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user %d: %w", telegramID, err)
	}
	if t, e := time.Parse(time.RFC3339, firstSeen); e == nil {
		u.FirstSeenAt = t
	}
	if t, e := time.Parse(time.RFC3339, lastSeen); e == nil {
		u.LastSeenAt = t
	}
	return &u, nil
}

func (s *SQLite) GetDailyStats(ctx context.Context, days int) ([]DailyStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			date(started_at) AS day,
			COUNT(*) AS migrations,
			COALESCE(SUM(sent_messages), 0) AS messages,
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS errors
		FROM migrations
		WHERE started_at >= date('now', printf('-%d days', ?))
		GROUP BY day
		ORDER BY day`, days)
	if err != nil {
		return nil, fmt.Errorf("get daily stats: %w", err)
	}
	defer rows.Close()

	var result []DailyStat
	for rows.Next() {
		var d DailyStat
		if err := rows.Scan(&d.Date, &d.Migrations, &d.Messages, &d.Errors); err != nil {
			return nil, fmt.Errorf("scan daily stat: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *SQLite) GetRecentMigrations(ctx context.Context, limit int) ([]Migration, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, max_chat_name, total_messages, sent_messages,
			status, started_at, duration_seconds,
			COALESCE(source_type, 'zip'), COALESCE(source_channel, ''), COALESCE(dest_type, 'max')
		FROM migrations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent migrations: %w", err)
	}
	defer rows.Close()

	var result []Migration
	for rows.Next() {
		var m Migration
		var startedAt string
		if err := rows.Scan(&m.ID, &m.UserID, &m.MaxChatName, &m.TotalMessages, &m.SentMessages,
			&m.Status, &startedAt, &m.DurationSeconds,
			&m.SourceType, &m.SourceChannel, &m.DestType); err != nil {
			return nil, fmt.Errorf("scan recent migration: %w", err)
		}
		if t, e := time.Parse(time.RFC3339, startedAt); e == nil {
			m.StartedAt = t
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveUserbotSession(ctx context.Context, userID int64, data []byte) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot_sessions (user_id, session_data, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			session_data = excluded.session_data,
			updated_at   = excluded.updated_at`,
		userID, data, now, now)
	if err != nil {
		return fmt.Errorf("save userbot session for user %d: %w", userID, err)
	}
	return nil
}

func (s *SQLite) LoadUserbotSession(ctx context.Context, userID int64) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT session_data FROM userbot_sessions WHERE user_id = ?`, userID).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load userbot session for user %d: %w", userID, err)
	}
	return data, nil
}

func (s *SQLite) DeleteUserbotSession(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM userbot_sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete userbot session for user %d: %w", userID, err)
	}
	return nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}
