package migrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func (s *mockSender) Close() error { return nil }

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
			From:         fmt.Sprintf("User%d", i+1), // different authors to prevent grouping
			FromID:       fmt.Sprintf("user%d", i+1),
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

func (s *cancellingMockSender) Close() error { return nil }

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

// --- filterMessages tests ---

func Test_FilterMessages_All(t *testing.T) {
	msgs := []models.Message{
		{ID: 1, Timestamp: time.Now()},
		{ID: 2, Timestamp: time.Now(), Media: []models.MediaFile{{Type: models.MediaPhoto}}},
	}
	got := filterMessages(msgs, "", 0)
	if len(got) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got))
	}
}

func Test_FilterMessages_TextOnly(t *testing.T) {
	msgs := []models.Message{
		{ID: 1, Timestamp: time.Now()},
		{ID: 2, Timestamp: time.Now(), Media: []models.MediaFile{{Type: models.MediaPhoto}}},
		{ID: 3, Timestamp: time.Now()},
	}
	got := filterMessages(msgs, "text", 0)
	if len(got) != 2 {
		t.Errorf("expected 2 text-only messages, got %d", len(got))
	}
	for _, m := range got {
		if len(m.Media) > 0 {
			t.Errorf("message %d should have no media", m.ID)
		}
	}
}

func Test_FilterMessages_MediaOnly(t *testing.T) {
	msgs := []models.Message{
		{ID: 1, Timestamp: time.Now()},
		{ID: 2, Timestamp: time.Now(), Media: []models.MediaFile{{Type: models.MediaPhoto}}},
		{ID: 3, Timestamp: time.Now(), Media: []models.MediaFile{{Type: models.MediaVideo}}},
	}
	got := filterMessages(msgs, "media", 0)
	if len(got) != 2 {
		t.Errorf("expected 2 media messages, got %d", len(got))
	}
	for _, m := range got {
		if len(m.Media) == 0 {
			t.Errorf("message %d should have media", m.ID)
		}
	}
}

func Test_FilterMessages_DateCutoff(t *testing.T) {
	old := time.Now().AddDate(0, -12, 0)  // 12 months ago
	recent := time.Now().AddDate(0, -1, 0) // 1 month ago
	msgs := []models.Message{
		{ID: 1, Timestamp: old},
		{ID: 2, Timestamp: recent},
		{ID: 3, Timestamp: time.Now()},
	}
	// Filter: last 3 months — should keep IDs 2 and 3
	got := filterMessages(msgs, "", 3)
	if len(got) != 2 {
		t.Errorf("expected 2 recent messages, got %d", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 3 {
		t.Errorf("expected IDs [2,3], got [%d,%d]", got[0].ID, got[1].ID)
	}
}

func Test_FilterMessages_DateAndTypeCombo(t *testing.T) {
	old := time.Now().AddDate(0, -12, 0)
	recent := time.Now().AddDate(0, -1, 0)
	msgs := []models.Message{
		{ID: 1, Timestamp: recent},                                                            // recent text
		{ID: 2, Timestamp: recent, Media: []models.MediaFile{{Type: models.MediaPhoto}}},     // recent media
		{ID: 3, Timestamp: old},                                                               // old text
		{ID: 4, Timestamp: old, Media: []models.MediaFile{{Type: models.MediaPhoto}}},        // old media
	}
	// Only recent media
	got := filterMessages(msgs, "media", 3)
	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("expected only message ID=2, got %v", got)
	}
}

func Test_FilterMessages_EmptyInput(t *testing.T) {
	got := filterMessages(nil, "text", 6)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(got))
	}
}

// --- replyTextFor tests ---

func Test_ReplyTextFor_NilReplyToID(t *testing.T) {
	msg := models.Message{ID: 1}
	index := map[int]string{1: "some text"}
	got := replyTextFor(msg, index)
	if got != "" {
		t.Errorf("expected empty string for nil ReplyToID, got %q", got)
	}
}

func Test_ReplyTextFor_IDExistsInIndex(t *testing.T) {
	replyID := 5
	msg := models.Message{ID: 10, ReplyToID: &replyID}
	index := map[int]string{5: "original message text"}
	got := replyTextFor(msg, index)
	if got != "original message text" {
		t.Errorf("expected reply text, got %q", got)
	}
}

func Test_ReplyTextFor_IDNotInIndex(t *testing.T) {
	replyID := 99
	msg := models.Message{ID: 10, ReplyToID: &replyID}
	index := map[int]string{5: "other message"}
	got := replyTextFor(msg, index)
	if got != "" {
		t.Errorf("expected empty string for missing ID in index, got %q", got)
	}
}

// --- DryRun tests ---

func Test_DryRun_EmptyMessages(t *testing.T) {
	conv := converter.New()
	stats := DryRun(nil, conv)
	if stats.TotalInput != 0 {
		t.Errorf("expected TotalInput=0, got %d", stats.TotalInput)
	}
	if stats.OutputMessages != 0 {
		t.Errorf("expected OutputMessages=0, got %d", stats.OutputMessages)
	}
}

func Test_DryRun_SingleTextMessage(t *testing.T) {
	conv := converter.New()
	msgs := []models.Message{
		{
			ID:        1,
			Author:    "Alice",
			Timestamp: time.Now(),
			RawParts:  []models.TextPart{{Text: "Hello world"}},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.TotalInput != 1 {
		t.Errorf("expected TotalInput=1, got %d", stats.TotalInput)
	}
	if stats.OutputMessages != 1 {
		t.Errorf("expected OutputMessages=1, got %d", stats.OutputMessages)
	}
	if stats.TextOnlyCount != 1 {
		t.Errorf("expected TextOnlyCount=1, got %d", stats.TextOnlyCount)
	}
	if stats.GroupedCount != 0 {
		t.Errorf("expected GroupedCount=0, got %d", stats.GroupedCount)
	}
}

func Test_DryRun_GroupableMessages(t *testing.T) {
	conv := converter.New()
	now := time.Now()
	// Two messages from same author within 5 min, no media — should group
	msgs := []models.Message{
		{
			ID:        1,
			Author:    "Alice",
			Timestamp: now,
			RawParts:  []models.TextPart{{Text: "First"}},
		},
		{
			ID:        2,
			Author:    "Alice",
			Timestamp: now.Add(1 * time.Minute),
			RawParts:  []models.TextPart{{Text: "Second"}},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.TotalInput != 2 {
		t.Errorf("expected TotalInput=2, got %d", stats.TotalInput)
	}
	if stats.GroupedCount != 2 {
		t.Errorf("expected GroupedCount=2, got %d", stats.GroupedCount)
	}
	// Two input messages → one grouped output message (1 chunk)
	if stats.OutputMessages != 1 {
		t.Errorf("expected OutputMessages=1 after grouping, got %d", stats.OutputMessages)
	}
}

func Test_DryRun_MediaMessages(t *testing.T) {
	conv := converter.New()
	msgs := []models.Message{
		{
			ID:        1,
			Author:    "Bob",
			Timestamp: time.Now(),
			RawParts:  []models.TextPart{{Text: "Photo caption"}},
			Media:     []models.MediaFile{{Type: models.MediaPhoto, FileName: "img.jpg"}},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.MediaCount != 1 {
		t.Errorf("expected MediaCount=1, got %d", stats.MediaCount)
	}
	if stats.StickerCount != 0 {
		t.Errorf("expected StickerCount=0, got %d", stats.StickerCount)
	}
}

func Test_DryRun_StickerMessage(t *testing.T) {
	conv := converter.New()
	msgs := []models.Message{
		{
			ID:           1,
			Author:       "Carol",
			Timestamp:    time.Now(),
			StickerEmoji: "😂",
			Media:        []models.MediaFile{{Type: models.MediaSticker, FileName: "sticker.webp"}},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.StickerCount != 1 {
		t.Errorf("expected StickerCount=1, got %d", stats.StickerCount)
	}
	if stats.MediaCount != 0 {
		t.Errorf("expected MediaCount=0 (sticker handled separately), got %d", stats.MediaCount)
	}
	if stats.TextOnlyCount != 1 {
		t.Errorf("expected TextOnlyCount=1 (sticker sends as text), got %d", stats.TextOnlyCount)
	}
}

func Test_DryRun_LongMessageSplits(t *testing.T) {
	conv := converter.New()
	// Build a text longer than MaxMessageLength (4096 runes)
	longText := strings.Repeat("a", 5000)
	msgs := []models.Message{
		{
			ID:        1,
			Author:    "Dave",
			Timestamp: time.Now(),
			RawParts:  []models.TextPart{{Text: longText}},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.SplitCount != 1 {
		t.Errorf("expected SplitCount=1, got %d", stats.SplitCount)
	}
	if stats.OutputMessages < 2 {
		t.Errorf("expected OutputMessages>=2 for long message, got %d", stats.OutputMessages)
	}
}

func Test_DryRun_MultipleMediaAttachments(t *testing.T) {
	conv := converter.New()
	// Message with 3 media items: first sent with text, 2 additional = 2 extra outputs
	msgs := []models.Message{
		{
			ID:        1,
			Author:    "Eve",
			Timestamp: time.Now(),
			RawParts:  []models.TextPart{{Text: "Three files"}},
			Media: []models.MediaFile{
				{Type: models.MediaPhoto, FileName: "a.jpg"},
				{Type: models.MediaPhoto, FileName: "b.jpg"},
				{Type: models.MediaPhoto, FileName: "c.jpg"},
			},
		},
	}
	stats := DryRun(msgs, conv)
	if stats.MediaCount != 1 {
		t.Errorf("expected MediaCount=1, got %d", stats.MediaCount)
	}
	// 1 (text+first media) + 2 (additional media) = 3 output messages
	if stats.OutputMessages != 3 {
		t.Errorf("expected OutputMessages=3, got %d", stats.OutputMessages)
	}
}

// --- sendMessage with StickerEmoji tests ---

// trackingSender records which send methods were called.
type trackingSender struct {
	textCalls  int
	photoCalls int
	mediaCalls int
}

func (s *trackingSender) Close() error { return nil }

func (s *trackingSender) SendText(_ context.Context, _ int64, _ string) error {
	s.textCalls++
	return nil
}

func (s *trackingSender) SendWithPhoto(_ context.Context, _ int64, _ string, _ string) error {
	s.photoCalls++
	return nil
}

func (s *trackingSender) SendWithMedia(_ context.Context, _ int64, _ string, _ string, _ models.MediaType) error {
	s.mediaCalls++
	return nil
}

func Test_SendMessage_StickerEmojiSendsTextOnly(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	msg := models.Message{
		ID:           1,
		Author:       "Alice",
		Timestamp:    time.Now(),
		StickerEmoji: "😂",
		Media:        []models.MediaFile{{Type: models.MediaSticker, FileName: "sticker.webp", FilePath: "stickers/s.webp"}},
	}

	mediaErrors, failedFile, err := m.sendMessage(context.Background(), 42, msg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mediaErrors != 0 {
		t.Errorf("expected 0 media errors, got %d", mediaErrors)
	}
	if failedFile != "" {
		t.Errorf("expected empty failedFile, got %q", failedFile)
	}
	if tracker.textCalls != 1 {
		t.Errorf("expected 1 SendText call, got %d", tracker.textCalls)
	}
	if tracker.photoCalls != 0 {
		t.Errorf("expected 0 SendWithPhoto calls, got %d", tracker.photoCalls)
	}
	if tracker.mediaCalls != 0 {
		t.Errorf("expected 0 SendWithMedia calls, got %d", tracker.mediaCalls)
	}
}

func Test_SendMessage_TextOnlySendsText(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	msg := models.Message{
		ID:        2,
		Author:    "Bob",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Hello"}},
	}

	_, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tracker.textCalls != 1 {
		t.Errorf("expected 1 SendText call, got %d", tracker.textCalls)
	}
	if tracker.photoCalls != 0 || tracker.mediaCalls != 0 {
		t.Errorf("expected no media calls for text-only message")
	}
}

func Test_SendMessage_PhotoMessage(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	msg := models.Message{
		ID:        3,
		Author:    "Carol",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Photo caption"}},
		Media:     []models.MediaFile{{Type: models.MediaPhoto, FileName: "img.jpg", FilePath: "photos/img.jpg"}},
	}

	mediaErrors, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mediaErrors != 0 {
		t.Errorf("expected 0 media errors, got %d", mediaErrors)
	}
	if tracker.photoCalls != 1 {
		t.Errorf("expected 1 SendWithPhoto call, got %d", tracker.photoCalls)
	}
	if tracker.textCalls != 0 {
		t.Errorf("expected 0 SendText calls for photo message, got %d", tracker.textCalls)
	}
}

func Test_SendMessage_NonPhotoMedia(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	msg := models.Message{
		ID:        4,
		Author:    "Dave",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Video"}},
		Media:     []models.MediaFile{{Type: models.MediaVideo, FileName: "video.mp4", FilePath: "files/video.mp4"}},
	}

	mediaErrors, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mediaErrors != 0 {
		t.Errorf("expected 0 media errors, got %d", mediaErrors)
	}
	if tracker.mediaCalls != 1 {
		t.Errorf("expected 1 SendWithMedia call, got %d", tracker.mediaCalls)
	}
}

func Test_SendMessage_WithReplyContext(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	replyID := 10
	msg := models.Message{
		ID:        20,
		Author:    "Eve",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Reply body"}},
		ReplyToID: &replyID,
	}
	index := map[int]string{10: "original message preview"}

	_, _, err := m.sendMessage(context.Background(), 42, msg, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tracker.textCalls != 1 {
		t.Errorf("expected 1 SendText call, got %d", tracker.textCalls)
	}
}

// photoFailSender fails on SendWithPhoto but succeeds on SendText.
type photoFailSender struct {
	textMessages []sentMessage
}

func (s *photoFailSender) Close() error { return nil }

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

// --- SetPauseCh coverage ---

func Test_SetPauseCh_AssignsChannel(t *testing.T) {
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := New(newMockSender(-1), conv, "/tmp/cursor_test.json", log)

	ch := make(chan struct{}, 1)
	m.SetPauseCh(ch)

	if m.pauseCh != ch {
		t.Error("SetPauseCh did not assign the channel")
	}
}

// --- sendMessage: additional media attachment path ---

func Test_SendMessage_MultipleMediaAttachments(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	msg := models.Message{
		ID:        5,
		Author:    "Frank",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Two photos"}},
		Media: []models.MediaFile{
			{Type: models.MediaPhoto, FileName: "a.jpg", FilePath: "photos/a.jpg"},
			{Type: models.MediaPhoto, FileName: "b.jpg", FilePath: "photos/b.jpg"},
		},
	}

	mediaErrors, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mediaErrors != 0 {
		t.Errorf("expected 0 media errors, got %d", mediaErrors)
	}
	// First photo sent with SendWithPhoto, second also with SendWithPhoto
	if tracker.photoCalls != 2 {
		t.Errorf("expected 2 SendWithPhoto calls, got %d", tracker.photoCalls)
	}
}

func Test_SendMessage_MultipleMediaWithNonPhoto(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	// First is a photo (SendWithPhoto), second is a video (SendWithMedia)
	msg := models.Message{
		ID:        6,
		Author:    "Grace",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Mixed media"}},
		Media: []models.MediaFile{
			{Type: models.MediaPhoto, FileName: "cover.jpg", FilePath: "photos/cover.jpg"},
			{Type: models.MediaVideo, FileName: "clip.mp4", FilePath: "videos/clip.mp4"},
		},
	}

	mediaErrors, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mediaErrors != 0 {
		t.Errorf("expected 0 media errors, got %d", mediaErrors)
	}
	if tracker.photoCalls != 1 {
		t.Errorf("expected 1 SendWithPhoto call, got %d", tracker.photoCalls)
	}
	if tracker.mediaCalls != 1 {
		t.Errorf("expected 1 SendWithMedia call for video, got %d", tracker.mediaCalls)
	}
}

// stickerAdditionalSender tracks calls and sends photo alongside sticker media.
// This checks the additional media loop for sticker type.
func Test_SendMessage_AdditionalStickerMediaViaPhotoPath(t *testing.T) {
	tracker := &trackingSender{}
	conv := converter.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Migrator{sender: tracker, converter: conv, log: log}

	// A message with two media items where first is photo and second is sticker type
	// (tests the MediaSticker branch in the additional-media loop)
	msg := models.Message{
		ID:        7,
		Author:    "Hank",
		Timestamp: time.Now(),
		RawParts:  []models.TextPart{{Text: "Photo + sticker"}},
		Media: []models.MediaFile{
			{Type: models.MediaPhoto, FileName: "photo.jpg", FilePath: "photos/photo.jpg"},
			{Type: models.MediaSticker, FileName: "sticker.webp", FilePath: "stickers/s.webp"},
		},
	}

	_, _, err := m.sendMessage(context.Background(), 42, msg, map[int]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First photo → SendWithPhoto; second sticker → also SendWithPhoto (per switch in additional loop)
	if tracker.photoCalls != 2 {
		t.Errorf("expected 2 SendWithPhoto calls (photo + sticker), got %d", tracker.photoCalls)
	}
}

// --- cursor.Save error path: write to read-only directory ---

func Test_CursorSave_FailsOnReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	cursorFile := filepath.Join(dir, "cursor.json")
	cm := NewCursorManager(cursorFile)
	cm.Update("chat", 1, 10, 1)

	// Make directory read-only so Save cannot create temp file
	if err := os.Chmod(dir, 0555); err != nil {
		t.Skip("cannot change directory permissions, skipping")
	}
	defer os.Chmod(dir, 0755)

	err := cm.Save()
	if err == nil {
		t.Error("expected error when saving to read-only directory, got nil")
	}
}

// --- migrate: filter applied path ---

func Test_MigrateAll_WithFilterType(t *testing.T) {
	dir := t.TempDir()

	// Export with 3 text messages + 1 photo message
	exportData := `{
		"name": "Filter Chat",
		"type": "personal_chat",
		"id": 777,
		"messages": [
			{
				"id": 1, "type": "message",
				"date": "2026-03-14T10:00:00", "date_unixtime": "1773741600",
				"from": "User1", "from_id": "user1", "text": "Text 1"
			},
			{
				"id": 2, "type": "message",
				"date": "2026-03-14T10:01:00", "date_unixtime": "1773741660",
				"from": "User2", "from_id": "user2", "text": "Text 2"
			},
			{
				"id": 3, "type": "message",
				"date": "2026-03-14T10:02:00", "date_unixtime": "1773741720",
				"from": "User3", "from_id": "user3",
				"text": "Photo caption",
				"photo": "photos/img.jpg"
			}
		]
	}`
	exportPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(exportPath, []byte(exportData), 0644); err != nil {
		t.Fatalf("write export: %v", err)
	}

	sender := newMockSender(-1)
	m, _ := newTestMigrator(t, sender, dir)

	// FilterType="text" should skip the photo message, sending only 2 text messages
	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		{
			Name:         "filter-test",
			TGExportPath: exportPath,
			MaxChatID:    777,
			FilterType:   "text",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Sent != 2 {
		t.Errorf("expected Sent=2 (text-only filter), got %d", stats.Sent)
	}
	if len(sender.messages) != 2 {
		t.Errorf("expected 2 messages sent, got %d", len(sender.messages))
	}
}

func Test_MigrateAll_WithFilterMonths(t *testing.T) {
	dir := t.TempDir()

	// Build export with one very old message and one recent message
	// The old timestamp uses a date 2 years ago
	exportData := `{
		"name": "Month Filter Chat",
		"type": "personal_chat",
		"id": 888,
		"messages": [
			{
				"id": 1, "type": "message",
				"date": "2020-01-01T10:00:00", "date_unixtime": "1577872800",
				"from": "User1", "from_id": "user1", "text": "Old message"
			},
			{
				"id": 2, "type": "message",
				"date": "2026-03-14T10:00:00", "date_unixtime": "1773741600",
				"from": "User2", "from_id": "user2", "text": "Recent message"
			}
		]
	}`
	exportPath := filepath.Join(dir, "result.json")
	if err := os.WriteFile(exportPath, []byte(exportData), 0644); err != nil {
		t.Fatalf("write export: %v", err)
	}

	sender := newMockSender(-1)
	m, _ := newTestMigrator(t, sender, dir)

	// FilterMonths=3 should only send the recent message
	stats, err := m.MigrateAll(context.Background(), []models.ChatMapping{
		{
			Name:         "month-filter-test",
			TGExportPath: exportPath,
			MaxChatID:    888,
			FilterMonths: 3,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Sent != 1 {
		t.Errorf("expected Sent=1 (recent only), got %d", stats.Sent)
	}
}
