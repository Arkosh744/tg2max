package telegram

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

const testFixture = "testdata/result.json"

func TestReadAll_MessageCount(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	// 6 total entries in fixture, 1 is service type -> 5 messages expected
	if got := len(result.Messages); got != 5 {
		t.Errorf("expected 5 messages, got %d", got)
	}
}

func TestReadAll_ServiceMessagesSkipped(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	for _, m := range result.Messages {
		if m.ID == 3 {
			t.Error("service message with id=3 should have been filtered out")
		}
	}
}

func TestReadAll_PlainTextMessage(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, result.Messages, 1)

	if msg.Author != "Alice" {
		t.Errorf("expected author Alice, got %q", msg.Author)
	}
	if msg.AuthorID != "user111" {
		t.Errorf("expected author_id user111, got %q", msg.AuthorID)
	}

	expectedTime := time.Unix(1773741600, 0)
	if !msg.Timestamp.Equal(expectedTime) {
		t.Errorf("expected timestamp %v, got %v", expectedTime, msg.Timestamp)
	}

	if len(msg.RawParts) != 1 {
		t.Fatalf("expected 1 text part, got %d", len(msg.RawParts))
	}
	if msg.RawParts[0].Type != "plain" {
		t.Errorf("expected part type plain, got %q", msg.RawParts[0].Type)
	}
	if msg.RawParts[0].Text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", msg.RawParts[0].Text)
	}
}

func TestReadAll_FormattedTextMessage(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, result.Messages, 2)

	if len(msg.RawParts) != 4 {
		t.Fatalf("expected 4 text parts, got %d", len(msg.RawParts))
	}

	expected := []models.TextPart{
		{Type: "plain", Text: "Check this "},
		{Type: "bold", Text: "important"},
		{Type: "plain", Text: " link: "},
		{Type: "text_link", Text: "click here", Href: "https://example.com"},
	}

	for i, want := range expected {
		got := msg.RawParts[i]
		if got.Type != want.Type {
			t.Errorf("part[%d] type: expected %q, got %q", i, want.Type, got.Type)
		}
		if got.Text != want.Text {
			t.Errorf("part[%d] text: expected %q, got %q", i, want.Text, got.Text)
		}
		if got.Href != want.Href {
			t.Errorf("part[%d] href: expected %q, got %q", i, want.Href, got.Href)
		}
	}
}

func TestReadAll_PhotoMessage(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, result.Messages, 4)

	if len(msg.Media) != 1 {
		t.Fatalf("expected 1 media attachment, got %d", len(msg.Media))
	}

	media := msg.Media[0]
	if media.Type != models.MediaPhoto {
		t.Errorf("expected media type %q, got %q", models.MediaPhoto, media.Type)
	}
	expectedPath := filepath.Join(reader.baseDir, "photos/photo_1.jpg")
	if media.FilePath != expectedPath {
		t.Errorf("expected file path %q, got %q", expectedPath, media.FilePath)
	}
	if media.FileName != "photo_1.jpg" {
		t.Errorf("expected file name photo_1.jpg, got %q", media.FileName)
	}
}

func TestReadAll_DocumentMessage(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, result.Messages, 5)

	if len(msg.Media) != 1 {
		t.Fatalf("expected 1 media attachment, got %d", len(msg.Media))
	}

	media := msg.Media[0]
	if media.Type != models.MediaDocument {
		t.Errorf("expected media type %q, got %q", models.MediaDocument, media.Type)
	}
	if media.MimeType != "application/pdf" {
		t.Errorf("expected mime type application/pdf, got %q", media.MimeType)
	}
	if media.FileName != "document.pdf" {
		t.Errorf("expected file name document.pdf, got %q", media.FileName)
	}
}

func TestReadAll_ForwardedMessage(t *testing.T) {
	reader := NewReader(testFixture)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, result.Messages, 6)

	if msg.ForwardedFrom != "News Channel" {
		t.Errorf("expected forwarded_from 'News Channel', got %q", msg.ForwardedFrom)
	}
}

func TestReadAll_CancelledContext(t *testing.T) {
	reader := NewReader(testFixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := reader.ReadAll(ctx)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestReadAll_FileNotFound(t *testing.T) {
	reader := NewReader("testdata/nonexistent.json")
	_, err := reader.ReadAll(context.Background())
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestTgText_UnmarshalJSON_String(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []tgTextPart
	}{
		{
			name:  "simple string",
			input: `"Hello world"`,
			want:  []tgTextPart{{Type: "plain", Text: "Hello world"}},
		},
		{
			name:  "empty string",
			input: `""`,
			want:  []tgTextPart{{Type: "plain", Text: ""}},
		},
		{
			name:  "string with special chars",
			input: `"line1\nline2"`,
			want:  []tgTextPart{{Type: "plain", Text: "line1\nline2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var text tgText
			if err := json.Unmarshal([]byte(tt.input), &text); err != nil {
				t.Fatalf("UnmarshalJSON returned error: %v", err)
			}
			if len(text.Parts) != len(tt.want) {
				t.Fatalf("expected %d parts, got %d", len(tt.want), len(text.Parts))
			}
			for i, want := range tt.want {
				got := text.Parts[i]
				if got.Type != want.Type || got.Text != want.Text {
					t.Errorf("part[%d]: expected {%q, %q}, got {%q, %q}",
						i, want.Type, want.Text, got.Type, got.Text)
				}
			}
		})
	}
}

func TestTgText_UnmarshalJSON_Array(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []tgTextPart
	}{
		{
			name:  "mixed types",
			input: `[{"type":"plain","text":"hello "},{"type":"bold","text":"world"}]`,
			want: []tgTextPart{
				{Type: "plain", Text: "hello "},
				{Type: "bold", Text: "world"},
			},
		},
		{
			name:  "with href",
			input: `[{"type":"text_link","text":"click","href":"https://example.com"}]`,
			want: []tgTextPart{
				{Type: "text_link", Text: "click", Href: "https://example.com"},
			},
		},
		{
			name:  "empty array",
			input: `[]`,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var text tgText
			if err := json.Unmarshal([]byte(tt.input), &text); err != nil {
				t.Fatalf("UnmarshalJSON returned error: %v", err)
			}
			if len(text.Parts) != len(tt.want) {
				t.Fatalf("expected %d parts, got %d", len(tt.want), len(text.Parts))
			}
			for i, want := range tt.want {
				got := text.Parts[i]
				if got.Type != want.Type || got.Text != want.Text || got.Href != want.Href {
					t.Errorf("part[%d]: expected {%q, %q, %q}, got {%q, %q, %q}",
						i, want.Type, want.Text, want.Href, got.Type, got.Text, got.Href)
				}
			}
		})
	}
}

// findByID is a test helper to locate a message by ID or fail the test.
func findByID(t *testing.T, msgs []models.Message, id int) models.Message {
	t.Helper()
	for _, m := range msgs {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("message with id=%d not found", id)
	return models.Message{}
}

// writeInlineExport creates a temporary result.json with a single message and
// returns a Reader pointing at it.
func writeInlineExport(t *testing.T, msgJSON string) *Reader {
	t.Helper()
	dir := t.TempDir()
	payload := `{"name":"inline","messages":[` + msgJSON + `]}`
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(payload), 0644); err != nil {
		t.Fatalf("writeInlineExport: %v", err)
	}
	return NewReader(path)
}

// ─── Poll ─────────────────────────────────────────────────────────────────────

func TestConvertMessage_PollData(t *testing.T) {
	msgJSON := `{
		"id": 10,
		"type": "message",
		"date_unixtime": "1000000",
		"from": "Alice",
		"from_id": "user111",
		"text": "",
		"poll": {
			"question": "Favourite color?",
			"answers": [
				{"text": "Red",  "voters": 5},
				{"text": "Blue", "voters": 3}
			],
			"total_voters": 8
		}
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]

	var pollPart *models.TextPart
	for i := range msg.RawParts {
		if strings.Contains(msg.RawParts[i].Text, "📊 Опрос:") {
			pollPart = &msg.RawParts[i]
			break
		}
	}
	if pollPart == nil {
		t.Fatalf("no TextPart with '📊 Опрос:' found; parts: %+v", msg.RawParts)
	}
	if !strings.Contains(pollPart.Text, "Favourite color?") {
		t.Errorf("poll part should contain question, got: %q", pollPart.Text)
	}
	if !strings.Contains(pollPart.Text, "Red") || !strings.Contains(pollPart.Text, "Blue") {
		t.Errorf("poll part should contain answer options, got: %q", pollPart.Text)
	}
}

// ─── Contact ──────────────────────────────────────────────────────────────────

func TestConvertMessage_ContactInformation(t *testing.T) {
	msgJSON := `{
		"id": 20,
		"type": "message",
		"date_unixtime": "1000000",
		"from": "Alice",
		"from_id": "user111",
		"text": "",
		"contact_information": {
			"first_name": "John",
			"last_name":  "Doe",
			"phone_number": "+79991234567"
		}
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]

	var contactPart *models.TextPart
	for i := range msg.RawParts {
		if strings.Contains(msg.RawParts[i].Text, "👤 Контакт:") {
			contactPart = &msg.RawParts[i]
			break
		}
	}
	if contactPart == nil {
		t.Fatalf("no TextPart with '👤 Контакт:' found; parts: %+v", msg.RawParts)
	}
	if !strings.Contains(contactPart.Text, "John") || !strings.Contains(contactPart.Text, "Doe") {
		t.Errorf("contact part should contain name, got: %q", contactPart.Text)
	}
	if !strings.Contains(contactPart.Text, "+79991234567") {
		t.Errorf("contact part should contain phone, got: %q", contactPart.Text)
	}
}

// ─── Location ─────────────────────────────────────────────────────────────────

func TestConvertMessage_LocationInformation(t *testing.T) {
	msgJSON := `{
		"id": 30,
		"type": "message",
		"date_unixtime": "1000000",
		"from": "Bob",
		"from_id": "user222",
		"text": "",
		"location_information": {
			"latitude":  55.7558,
			"longitude": 37.6173
		}
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]

	var locPart *models.TextPart
	for i := range msg.RawParts {
		if strings.Contains(msg.RawParts[i].Text, "📍 Геолокация:") {
			locPart = &msg.RawParts[i]
			break
		}
	}
	if locPart == nil {
		t.Fatalf("no TextPart with '📍 Геолокация:' found; parts: %+v", msg.RawParts)
	}
	if !strings.Contains(locPart.Text, "55.7558") {
		t.Errorf("location part should contain latitude, got: %q", locPart.Text)
	}
}

// ─── safeMediaPath ────────────────────────────────────────────────────────────

func TestSafeMediaPath_NormalPath(t *testing.T) {
	reader := NewReader("/some/export/result.json")
	got, err := reader.safeMediaPath("photos/photo1.jpg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "/some/export/") {
		t.Errorf("expected path inside export dir, got %q", got)
	}
	if !strings.HasSuffix(got, "photo1.jpg") {
		t.Errorf("expected path to end with photo1.jpg, got %q", got)
	}
}

func TestSafeMediaPath_TraversalRejected(t *testing.T) {
	reader := NewReader("/some/export/result.json")
	_, err := reader.safeMediaPath("../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal, got nil")
	}
}

func TestSafeMediaPath_AbsolutePathRejected(t *testing.T) {
	reader := NewReader("/some/export/result.json")
	_, err := reader.safeMediaPath("/etc/passwd")
	if err == nil {
		t.Error("expected error for absolute path, got nil")
	}
}

// ─── StickerEmoji ─────────────────────────────────────────────────────────────

func TestConvertMessage_StickerEmoji(t *testing.T) {
	msgJSON := `{
		"id": 40,
		"type": "message",
		"date_unixtime": "1000000",
		"from": "Alice",
		"from_id": "user111",
		"text": "",
		"sticker_emoji": "🔥",
		"file": "stickers/sticker1.webp",
		"media_type": "sticker"
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.StickerEmoji != "🔥" {
		t.Errorf("StickerEmoji: got %q, want %q", msg.StickerEmoji, "🔥")
	}
}

// ─── resolveMediaType ─────────────────────────────────────────────────────────

func TestResolveMediaType(t *testing.T) {
	reader := NewReader("/some/export/result.json")
	tests := []struct {
		input string
		want  models.MediaType
	}{
		{"video_file", models.MediaVideo},
		{"audio_file", models.MediaAudio},
		{"voice_message", models.MediaVoice},
		{"video_message", models.MediaVideo},
		{"sticker", models.MediaSticker},
		{"animation", models.MediaAnimation},
		{"unknown", models.MediaDocument},
		{"", models.MediaDocument},
	}
	for _, tt := range tests {
		got := reader.resolveMediaType(tt.input)
		if got != tt.want {
			t.Errorf("resolveMediaType(%q): got %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── parseTimestamp fallback ──────────────────────────────────────────────────

func TestParseTimestamp_FallbackToDateField(t *testing.T) {
	// When date_unixtime is missing, parseTimestamp falls back to the date string.
	msgJSON := `{
		"id": 50,
		"type": "message",
		"date": "2026-03-14T10:00:00",
		"from": "Alice",
		"from_id": "user111",
		"text": "no unixtime"
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	expected := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)
	if !result.Messages[0].Timestamp.Equal(expected) {
		t.Errorf("Timestamp: got %v, want %v", result.Messages[0].Timestamp, expected)
	}
}

func TestParseTimestamp_NoValidTimestamp(t *testing.T) {
	// Both date and date_unixtime absent → message is skipped (counted in Skipped).
	msgJSON := `{
		"id": 60,
		"type": "message",
		"from": "Alice",
		"from_id": "user111",
		"text": "timestamp-less"
	}`
	reader := writeInlineExport(t, msgJSON)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages (all skipped), got %d", len(result.Messages))
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
}

// ─── UnmarshalJSON mixed array ────────────────────────────────────────────────

func TestTgText_UnmarshalJSON_MixedStringAndObject(t *testing.T) {
	input := `["plain text", {"type":"bold","text":"bold text"}]`
	var text tgText
	if err := json.Unmarshal([]byte(input), &text); err != nil {
		t.Fatalf("UnmarshalJSON returned error: %v", err)
	}
	if len(text.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(text.Parts))
	}
	if text.Parts[0].Type != "plain" || text.Parts[0].Text != "plain text" {
		t.Errorf("part[0]: got {%q, %q}, want {plain, plain text}", text.Parts[0].Type, text.Parts[0].Text)
	}
	if text.Parts[1].Type != "bold" || text.Parts[1].Text != "bold text" {
		t.Errorf("part[1]: got {%q, %q}, want {bold, bold text}", text.Parts[1].Type, text.Parts[1].Text)
	}
}

func TestTgText_UnmarshalJSON_InvalidArray(t *testing.T) {
	// An array element that is neither a string nor a valid tgTextPart object.
	// Actually tgTextPart accepts anything because all fields are optional strings —
	// verify that a completely malformed payload fails at the top-level array decode.
	input := `[{invalid json`
	var text tgText
	err := json.Unmarshal([]byte(input), &text)
	if err == nil {
		t.Error("expected error for malformed JSON array, got nil")
	}
}
