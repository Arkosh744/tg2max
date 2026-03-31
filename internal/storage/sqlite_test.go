package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *SQLite {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := User{TelegramID: 123, Username: "alice", FirstName: "Alice", LastName: "A"}
	if err := s.UpsertUser(ctx, u); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second call updates username
	u.Username = "alice2"
	if err := s.UpsertUser(ctx, u); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var username string
	if err := s.db.QueryRow("SELECT username FROM users WHERE telegram_id = 123").Scan(&username); err != nil {
		t.Fatalf("query: %v", err)
	}
	if username != "alice2" {
		t.Errorf("username = %q, want alice2", username)
	}
}

func TestSaveUpload(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Must create user first (FK constraint)
	if err := s.UpsertUser(ctx, User{TelegramID: 1}); err != nil {
		t.Fatal(err)
	}

	id, err := s.SaveUpload(ctx, Upload{
		UserID:       1,
		Filename:     "export.zip",
		FileSize:     1024,
		ExportHash:   "abc123",
		MessageCount: 500,
	})
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected id > 0, got %d", id)
	}
}

func TestMigrationLifecycle(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.UpsertUser(ctx, User{TelegramID: 1}); err != nil {
		t.Fatal(err)
	}

	id, err := s.StartMigration(ctx, Migration{
		UserID:        1,
		MaxChatID:     999,
		MaxChatName:   "Test Chat",
		TotalMessages: 100,
	})
	if err != nil {
		t.Fatalf("StartMigration: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected id > 0, got %d", id)
	}

	// Active migration should be found
	active, err := s.GetActiveMigration(ctx)
	if err != nil {
		t.Fatalf("GetActiveMigration: %v", err)
	}
	if active == nil {
		t.Fatal("expected active migration, got nil")
	}
	if active.ID != id {
		t.Errorf("active.ID = %d, want %d", active.ID, id)
	}

	// Simulate some work time
	time.Sleep(10 * time.Millisecond)

	// Finish migration
	if err := s.FinishMigration(ctx, id, "completed", 95, ""); err != nil {
		t.Fatalf("FinishMigration: %v", err)
	}

	// No active migration now
	active, err = s.GetActiveMigration(ctx)
	if err != nil {
		t.Fatalf("GetActiveMigration after finish: %v", err)
	}
	if active != nil {
		t.Errorf("expected nil, got migration %d", active.ID)
	}

	// Verify stored values
	var status string
	var sent, dur int
	err = s.db.QueryRow("SELECT status, sent_messages, duration_seconds FROM migrations WHERE id = ?", id).
		Scan(&status, &sent, &dur)
	if err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if sent != 95 {
		t.Errorf("sent = %d, want 95", sent)
	}
}

func TestGetActiveMigration_None(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	m, err := s.GetActiveMigration(ctx)
	if err != nil {
		t.Fatalf("GetActiveMigration: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil, got %+v", m)
	}
}

func TestGetStats(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.UpsertUser(ctx, User{TelegramID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, User{TelegramID: 2}); err != nil {
		t.Fatal(err)
	}

	id1, _ := s.StartMigration(ctx, Migration{UserID: 1, MaxChatID: 1, TotalMessages: 100})
	s.FinishMigration(ctx, id1, "completed", 100, "")

	id2, _ := s.StartMigration(ctx, Migration{UserID: 2, MaxChatID: 2, TotalMessages: 50})
	s.FinishMigration(ctx, id2, "failed", 30, "network error")

	st, err := s.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if st.TotalUsers != 2 {
		t.Errorf("TotalUsers = %d, want 2", st.TotalUsers)
	}
	if st.TotalMigrations != 2 {
		t.Errorf("TotalMigrations = %d, want 2", st.TotalMigrations)
	}
	if st.Completed != 1 {
		t.Errorf("Completed = %d, want 1", st.Completed)
	}
	if st.Failed != 1 {
		t.Errorf("Failed = %d, want 1", st.Failed)
	}
	if st.TotalSent != 130 {
		t.Errorf("TotalSent = %d, want 130", st.TotalSent)
	}
}

func TestGetUserHistory(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.UpsertUser(ctx, User{TelegramID: 1}); err != nil {
		t.Fatal(err)
	}

	id1, _ := s.StartMigration(ctx, Migration{UserID: 1, MaxChatID: 1, MaxChatName: "Chat A", TotalMessages: 100})
	s.FinishMigration(ctx, id1, "completed", 100, "")

	id2, _ := s.StartMigration(ctx, Migration{UserID: 1, MaxChatID: 2, MaxChatName: "Chat B", TotalMessages: 50})
	s.FinishMigration(ctx, id2, "failed", 25, "timeout")

	entries, err := s.GetUserHistory(ctx, 1, 10)
	if err != nil {
		t.Fatalf("GetUserHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].MaxChatName != "Chat B" {
		t.Errorf("first entry = %q, want Chat B", entries[0].MaxChatName)
	}
	if entries[1].Status != "completed" {
		t.Errorf("second entry status = %q, want completed", entries[1].Status)
	}
}

func TestGetStats_Empty(t *testing.T) {
	s := newTestDB(t)
	st, err := s.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats on empty db: %v", err)
	}
	if st.TotalMigrations != 0 {
		t.Errorf("TotalMigrations = %d, want 0", st.TotalMigrations)
	}
	if st.TotalUsers != 0 {
		t.Errorf("TotalUsers = %d, want 0", st.TotalUsers)
	}
}

func TestSchemaMigrationIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s1, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	// Second open should not fail
	s2, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	s2.Close()
}
