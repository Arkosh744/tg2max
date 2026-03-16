package telegram

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

const testFixture = "testdata/result.json"

func TestReadAll_MessageCount(t *testing.T) {
	reader := NewReader(testFixture)
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	// 6 total entries in fixture, 1 is service type -> 5 messages expected
	if got := len(msgs); got != 5 {
		t.Errorf("expected 5 messages, got %d", got)
	}
}

func TestReadAll_ServiceMessagesSkipped(t *testing.T) {
	reader := NewReader(testFixture)
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	for _, m := range msgs {
		if m.ID == 3 {
			t.Error("service message with id=3 should have been filtered out")
		}
	}
}

func TestReadAll_PlainTextMessage(t *testing.T) {
	reader := NewReader(testFixture)
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, msgs, 1)

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
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, msgs, 2)

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
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, msgs, 4)

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
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, msgs, 5)

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
	msgs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}

	msg := findByID(t, msgs, 6)

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
