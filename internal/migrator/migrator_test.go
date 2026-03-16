package migrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/pkg/models"
)

// --- mock sender ---

type sentMessage struct {
	chatID int64
	text   string
	photo  string
	media  string
}

type mockSender struct {
	messages []sentMessage
	failAt   int // fail when messages count reaches this (-1 = never fail)
}

func newMockSender(failAt int) *mockSender {
	return &mockSender{failAt: failAt}
}

func (s *mockSender) checkFail() error {
	if s.failAt >= 0 && len(s.messages) >= s.failAt {
		return fmt.Errorf("mock send error at message %d", len(s.messages))
	}
	return nil
}

func (s *mockSender) SendText(_ context.Context, chatID int64, text string) error {
	if err := s.checkFail(); err != nil {
		return err
	}
	s.messages = append(s.messages, sentMessage{chatID: chatID, text: text})
	return nil
}

func (s *mockSender) SendWithPhoto(_ context.Context, chatID int64, text string, photoPath string) error {
	if err := s.checkFail(); err != nil {
		return err
	}
	s.messages = append(s.messages, sentMessage{chatID: chatID, text: text, photo: photoPath})
	return nil
}

func (s *mockSender) SendWithMedia(_ context.Context, chatID int64, text string, mediaPath string, _ models.MediaType) error {
	if err := s.checkFail(); err != nil {
		return err
	}
	s.messages = append(s.messages, sentMessage{chatID: chatID, text: text, media: mediaPath})
	return nil
}

// --- helpers ---

// writeTempExport writes a minimal TG export JSON with N text messages (IDs 1..n) to dir/result.json.
func writeTempExport(t *testing.T, dir string, n int) string {
	t.Helper()

	type msg struct {
		ID           int    `json:"id"`
		Type         string `json:"type"`
		Date         string `json:"date"`
		DateUnixtime string `json:"date_unixtime"`
		From         string `json:"from"`
		FromID       string `json:"from_id"`
		Text         string `json:"text"`
	}

	messages := make([]msg, n)
	for i := range messages {
		messages[i] = msg{
			ID:           i + 1,
			Type:         "message",
			Date:         fmt.Sprintf("2026-03-14T10:%02d:00", i),
			DateUnixtime: fmt.Sprintf("%d", 1773741600+i*60),
			From:         "TestUser",
			FromID:       "user100",
			Text:         fmt.Sprintf("Message %d", i+1),
		}
	}

	export := struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		ID       int64  `json:"id"`
		Messages []msg  `json:"messages"`
	}{
		Name:     "Test Chat",
		Type:     "personal_chat",
		ID:       12345,
		Messages: messages,
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		t.Fatalf("marshal test export: %v", err)
	}

	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write test export: %v", err)
	}
	return path
}

func newTestMigrator(t *testing.T, sender Sender, cursorDir string) (*Migrator, string) {
	t.Helper()
	cursorFile := filepath.Join(cursorDir, "cursor.json")
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := New(sender, conv, cursorFile, log)
	return m, cursorFile
}

func makeMapping(name string, exportPath string, chatID int64) models.ChatMapping {
	return models.ChatMapping{
		Name:         name,
		TGExportPath: exportPath,
		MaxChatID:    chatID,
	}
}

// loadCursorLastID reads the cursor file and returns the last message ID for the given chat.
func loadCursorLastID(t *testing.T, cursorFile, chatName string) int {
	t.Helper()
	cm := NewCursorManager(cursorFile)
	if err := cm.Load(); err != nil {
		t.Fatalf("load cursor: %v", err)
	}
	return cm.GetLastMessageID(chatName)
}

// --- tests ---

func Test_MigrateAll_Success(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 5)
	sender := newMockSender(-1)
	m, _ := newTestMigrator(t, sender, dir)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("test", exportPath, 999),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Sent != 5 {
		t.Errorf("expected Sent=5, got %d", stats.Sent)
	}
	if stats.Skipped != 0 {
		t.Errorf("expected Skipped=0, got %d", stats.Skipped)
	}
	if stats.MediaErrors != 0 {
		t.Errorf("expected MediaErrors=0, got %d", stats.MediaErrors)
	}
	if len(sender.messages) != 5 {
		t.Errorf("expected 5 sent messages, got %d", len(sender.messages))
	}
	for _, sm := range sender.messages {
		if sm.chatID != 999 {
			t.Errorf("expected chatID=999, got %d", sm.chatID)
		}
	}
}

func Test_ResumeFromCursor(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 6)
	sender := newMockSender(-1)

	// Pre-seed cursor: last sent message ID = 2
	cursorFile := filepath.Join(dir, "cursor.json")
	cm := NewCursorManager(cursorFile)
	cm.Update("test", 2, 6, 2)
	if err := cm.Save(); err != nil {
		t.Fatalf("save cursor: %v", err)
	}

	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := New(sender, conv, cursorFile, log)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("test", exportPath, 999),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Messages 1,2 skipped (ID<=2), messages 3,4,5,6 sent
	if stats.Sent != 4 {
		t.Errorf("expected Sent=4, got %d", stats.Sent)
	}
	if stats.Skipped != 2 {
		t.Errorf("expected Skipped=2, got %d", stats.Skipped)
	}
	if len(sender.messages) != 4 {
		t.Errorf("expected 4 sent messages, got %d", len(sender.messages))
	}

	// Verify cursor was updated to last message
	lastID := loadCursorLastID(t, cursorFile, "test")
	if lastID != 6 {
		t.Errorf("expected cursor lastMessageID=6, got %d", lastID)
	}
}

func Test_CursorNotUpdatedOnSendFailure(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 5)

	// Fail at the 3rd send call (0-indexed: after 2 successful sends)
	sender := newMockSender(2)
	m, cursorFile := newTestMigrator(t, sender, dir)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("test", exportPath, 999),
	})

	if err == nil {
		t.Fatal("expected error from failed send")
	}

	// 2 messages were sent successfully before the failure
	if stats.Sent != 2 {
		t.Errorf("expected Sent=2, got %d", stats.Sent)
	}

	// Critical: cursor should point to message ID 2 (the last SUCCESSFULLY sent),
	// NOT message ID 3 (the one that failed).
	lastID := loadCursorLastID(t, cursorFile, "test")
	if lastID != 2 {
		t.Errorf("cursor should be at last successful message ID=2, got %d", lastID)
	}
}

func Test_StatsCounting(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 10)

	// Pre-seed cursor at message 3
	cursorFile := filepath.Join(dir, "cursor.json")
	cm := NewCursorManager(cursorFile)
	cm.Update("stats-test", 3, 10, 3)
	if err := cm.Save(); err != nil {
		t.Fatalf("save cursor: %v", err)
	}

	sender := newMockSender(-1)
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := New(sender, conv, cursorFile, log)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("stats-test", exportPath, 100),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Sent != 7 {
		t.Errorf("expected Sent=7, got %d", stats.Sent)
	}
	if stats.Skipped != 3 {
		t.Errorf("expected Skipped=3, got %d", stats.Skipped)
	}
	if stats.MediaErrors != 0 {
		t.Errorf("expected MediaErrors=0, got %d", stats.MediaErrors)
	}
	if stats.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func Test_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 100)
	sender := newMockSender(-1)
	m, cursorFile := newTestMigrator(t, sender, dir)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a few messages are sent by wrapping the sender
	cancelSender := &cancellingMockSender{
		inner:     sender,
		cancelAt:  5,
		cancelFn:  cancel,
		callCount: 0,
	}
	m.sender = cancelSender

	stats, err := m.MigrateAll(ctx, []models.ChatMapping{
		makeMapping("cancel-test", exportPath, 100),
	})

	if err == nil {
		t.Fatal("expected context cancellation error")
	}

	// Cursor should be saved
	lastID := loadCursorLastID(t, cursorFile, "cancel-test")
	if lastID <= 0 {
		t.Error("expected cursor to be saved with a positive lastMessageID")
	}

	// Sent count should be less than total
	if stats.Sent >= 100 {
		t.Errorf("expected partial send, got Sent=%d", stats.Sent)
	}
}

// cancellingMockSender wraps mockSender but cancels the context at a specific call count.
type cancellingMockSender struct {
	inner     *mockSender
	cancelAt  int
	cancelFn  context.CancelFunc
	callCount int
}

func (s *cancellingMockSender) SendText(ctx context.Context, chatID int64, text string) error {
	s.callCount++
	if s.callCount == s.cancelAt {
		s.cancelFn()
	}
	return s.inner.SendText(ctx, chatID, text)
}

func (s *cancellingMockSender) SendWithPhoto(ctx context.Context, chatID int64, text string, photoPath string) error {
	s.callCount++
	if s.callCount == s.cancelAt {
		s.cancelFn()
	}
	return s.inner.SendWithPhoto(ctx, chatID, text, photoPath)
}

func (s *cancellingMockSender) SendWithMedia(ctx context.Context, chatID int64, text string, mediaPath string, mediaType models.MediaType) error {
	s.callCount++
	if s.callCount == s.cancelAt {
		s.cancelFn()
	}
	return s.inner.SendWithMedia(ctx, chatID, text, mediaPath, mediaType)
}

func Test_DryRunWithMock(t *testing.T) {
	dir := t.TempDir()
	exportPath := writeTempExport(t, dir, 8)
	sender := newMockSender(-1)
	m, _ := newTestMigrator(t, sender, dir)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("dry", exportPath, 42),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Sent != 8 {
		t.Errorf("expected Sent=8, got %d", stats.Sent)
	}
	if stats.Skipped != 0 {
		t.Errorf("expected Skipped=0, got %d", stats.Skipped)
	}
	if stats.MediaErrors != 0 {
		t.Errorf("expected MediaErrors=0, got %d", stats.MediaErrors)
	}
	if len(sender.messages) != 8 {
		t.Errorf("expected 8 messages sent, got %d", len(sender.messages))
	}

	// Verify all messages went to the correct chatID
	for i, sm := range sender.messages {
		if sm.chatID != 42 {
			t.Errorf("message %d: expected chatID=42, got %d", i, sm.chatID)
		}
		if sm.text == "" {
			t.Errorf("message %d: expected non-empty text", i)
		}
	}
}

func Test_MultipleChatMappings(t *testing.T) {
	dir := t.TempDir()
	chat1Dir := filepath.Join(dir, "chat1")
	chat2Dir := filepath.Join(dir, "chat2")
	os.MkdirAll(chat1Dir, 0755)
	os.MkdirAll(chat2Dir, 0755)
	export1 := writeTempExport(t, chat1Dir, 3)
	export2 := writeTempExport(t, chat2Dir, 4)

	sender := newMockSender(-1)
	m, _ := newTestMigrator(t, sender, dir)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("chat1", export1, 100),
		makeMapping("chat2", export2, 200),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Sent != 7 {
		t.Errorf("expected Sent=7 (3+4), got %d", stats.Sent)
	}
	if len(sender.messages) != 7 {
		t.Errorf("expected 7 messages, got %d", len(sender.messages))
	}
}

func Test_MediaMessageSendFailFallback(t *testing.T) {
	dir := t.TempDir()

	// Create export with a photo message
	exportData := `{
		"name": "Media Chat",
		"type": "personal_chat",
		"id": 999,
		"messages": [
			{
				"id": 1,
				"type": "message",
				"date": "2026-03-14T10:00:00",
				"date_unixtime": "1773741600",
				"from": "Alice",
				"from_id": "user111",
				"text": "Photo message",
				"photo": "photos/img.jpg"
			}
		]
	}`
	exportPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(exportPath, []byte(exportData), 0644); err != nil {
		t.Fatalf("write export: %v", err)
	}

	// Sender that fails on photo but succeeds on text fallback
	sender := &photoFailSender{}
	m, _ := newTestMigrator(t, sender, dir)

	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		makeMapping("media-test", exportPath, 555),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Sent != 1 {
		t.Errorf("expected Sent=1, got %d", stats.Sent)
	}
	if stats.MediaErrors != 1 {
		t.Errorf("expected MediaErrors=1, got %d", stats.MediaErrors)
	}
}

// photoFailSender fails on SendWithPhoto but succeeds on SendText.
type photoFailSender struct {
	textMessages []sentMessage
}

func (s *photoFailSender) SendText(_ context.Context, chatID int64, text string) error {
	s.textMessages = append(s.textMessages, sentMessage{chatID: chatID, text: text})
	return nil
}

func (s *photoFailSender) SendWithPhoto(_ context.Context, _ int64, _ string, _ string) error {
	return fmt.Errorf("photo upload failed")
}

func (s *photoFailSender) SendWithMedia(_ context.Context, _ int64, _ string, _ string, _ models.MediaType) error {
	return fmt.Errorf("media upload failed")
}
