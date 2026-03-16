package converter

import (
	"testing"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

func ts(year, month, day, hour, min int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, time.UTC)
}

func TestFormatTimestamp(t *testing.T) {
	c := New()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"january", ts(2025, 1, 5, 9, 3), "5 янв 2025, 09:03"},
		{"february", ts(2024, 2, 14, 18, 30), "14 фев 2024, 18:30"},
		{"march", ts(2026, 3, 1, 0, 0), "1 мар 2026, 00:00"},
		{"april", ts(2025, 4, 10, 12, 45), "10 апр 2025, 12:45"},
		{"may", ts(2025, 5, 31, 23, 59), "31 май 2025, 23:59"},
		{"june", ts(2025, 6, 15, 6, 7), "15 июн 2025, 06:07"},
		{"july", ts(2025, 7, 4, 14, 0), "4 июл 2025, 14:00"},
		{"august", ts(2025, 8, 20, 8, 10), "20 авг 2025, 08:10"},
		{"september", ts(2025, 9, 1, 11, 11), "1 сен 2025, 11:11"},
		{"october", ts(2025, 10, 31, 22, 0), "31 окт 2025, 22:00"},
		{"november", ts(2025, 11, 7, 3, 15), "7 ноя 2025, 03:15"},
		{"december", ts(2025, 12, 25, 17, 30), "25 дек 2025, 17:30"},
		{"midnight_padding", ts(2025, 1, 1, 0, 0), "1 янв 2025, 00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.FormatTimestamp(tt.time)
			if got != tt.want {
				t.Errorf("FormatTimestamp(%v) = %q, want %q", tt.time, got, tt.want)
			}
		})
	}
}

func TestConvertParts(t *testing.T) {
	c := New()

	tests := []struct {
		name  string
		parts []models.TextPart
		want  string
	}{
		{
			name:  "plain_text",
			parts: []models.TextPart{{Type: "", Text: "hello world"}},
			want:  "hello world",
		},
		{
			name:  "bold",
			parts: []models.TextPart{{Type: "bold", Text: "important"}},
			want:  "**important**",
		},
		{
			name:  "italic",
			parts: []models.TextPart{{Type: "italic", Text: "emphasis"}},
			want:  "*emphasis*",
		},
		{
			name:  "code_inline",
			parts: []models.TextPart{{Type: "code", Text: "fmt.Println()"}},
			want:  "`fmt.Println()`",
		},
		{
			name:  "code_block_pre",
			parts: []models.TextPart{{Type: "pre", Text: "func main() {\n\treturn\n}"}},
			want:  "```\nfunc main() {\n\treturn\n}\n```",
		},
		{
			name:  "text_link_with_href",
			parts: []models.TextPart{{Type: "text_link", Text: "click here", Href: "https://example.com"}},
			want:  "[click here](https://example.com)",
		},
		{
			name:  "link_with_href",
			parts: []models.TextPart{{Type: "link", Text: "example.com", Href: "https://example.com"}},
			want:  "[example.com](https://example.com)",
		},
		{
			name:  "text_link_no_href_fallback",
			parts: []models.TextPart{{Type: "text_link", Text: "plain fallback", Href: ""}},
			want:  "plain fallback",
		},
		{
			name:  "link_no_href_fallback",
			parts: []models.TextPart{{Type: "link", Text: "https://example.com", Href: ""}},
			want:  "https://example.com",
		},
		{
			name:  "mention",
			parts: []models.TextPart{{Type: "mention", Text: "@username"}},
			want:  "**@username**",
		},
		{
			name:  "strikethrough",
			parts: []models.TextPart{{Type: "strikethrough", Text: "deleted"}},
			want:  "~~deleted~~",
		},
		{
			name:  "blockquote_single_line",
			parts: []models.TextPart{{Type: "blockquote", Text: "quoted text"}},
			want:  "> quoted text",
		},
		{
			name:  "blockquote_multiline",
			parts: []models.TextPart{{Type: "blockquote", Text: "line one\nline two\nline three"}},
			want:  "> line one\n> line two\n> line three",
		},
		{
			name:  "empty_parts",
			parts: nil,
			want:  "",
		},
		{
			name:  "empty_slice",
			parts: []models.TextPart{},
			want:  "",
		},
		{
			name: "mixed_formatting",
			parts: []models.TextPart{
				{Type: "", Text: "Hello "},
				{Type: "bold", Text: "world"},
				{Type: "", Text: ", this is "},
				{Type: "italic", Text: "important"},
				{Type: "", Text: " and "},
				{Type: "code", Text: "code()"},
				{Type: "", Text: "!"},
			},
			want: "Hello **world**, this is *important* and `code()`!",
		},
		{
			name: "bold_then_link",
			parts: []models.TextPart{
				{Type: "bold", Text: "See: "},
				{Type: "text_link", Text: "docs", Href: "https://docs.go.dev"},
			},
			want: "**See: **[docs](https://docs.go.dev)",
		},
		{
			name:  "unknown_type_treated_as_plain",
			parts: []models.TextPart{{Type: "unknown_future_type", Text: "some text"}},
			want:  "some text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.ConvertParts(tt.parts)
			if got != tt.want {
				t.Errorf("ConvertParts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatForMax_PlainText(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Alice",
		Timestamp: ts(2025, 3, 15, 14, 30),
		RawParts:  []models.TextPart{{Type: "", Text: "Hello everyone"}},
	}

	got := c.FormatForMax(msg)
	want := "**Alice** · 15 мар 2025, 14:30\nHello everyone"
	if got != want {
		t.Errorf("FormatForMax plain =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_Forwarded(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:        "Bob",
		Timestamp:     ts(2025, 6, 1, 10, 0),
		ForwardedFrom: "Charlie",
		RawParts:      []models.TextPart{{Type: "", Text: "Original message"}},
	}

	got := c.FormatForMax(msg)
	want := "**Bob** · 1 июн 2025, 10:00\n↩️ Переслано от Charlie\nOriginal message"
	if got != want {
		t.Errorf("FormatForMax forwarded =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_WithMedia(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Dave",
		Timestamp: ts(2025, 12, 25, 18, 0),
		RawParts:  []models.TextPart{{Type: "", Text: "Check this out"}},
		Media: []models.MediaFile{
			{Type: models.MediaPhoto, FileName: "photo.jpg"},
		},
	}

	got := c.FormatForMax(msg)
	want := "**Dave** · 25 дек 2025, 18:00\nCheck this out\n📎 photo.jpg"
	if got != want {
		t.Errorf("FormatForMax media =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_MultipleMedia(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Eve",
		Timestamp: ts(2025, 1, 1, 0, 0),
		RawParts:  []models.TextPart{{Type: "bold", Text: "Files"}},
		Media: []models.MediaFile{
			{Type: models.MediaDocument, FileName: "report.pdf"},
			{Type: models.MediaPhoto, FileName: "screenshot.png"},
		},
	}

	got := c.FormatForMax(msg)
	want := "**Eve** · 1 янв 2025, 00:00\n**Files**\n📎 report.pdf\n📎 screenshot.png"
	if got != want {
		t.Errorf("FormatForMax multi-media =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_EmptyBody(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Frank",
		Timestamp: ts(2025, 8, 10, 12, 0),
		RawParts:  nil,
	}

	got := c.FormatForMax(msg)
	want := "**Frank** · 10 авг 2025, 12:00\n"
	if got != want {
		t.Errorf("FormatForMax empty =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_EmptyBodyWithMedia(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Grace",
		Timestamp: ts(2025, 5, 20, 9, 15),
		RawParts:  nil,
		Media: []models.MediaFile{
			{Type: models.MediaVideo, FileName: "clip.mp4"},
		},
	}

	got := c.FormatForMax(msg)
	want := "**Grace** · 20 май 2025, 09:15\n\n📎 clip.mp4"
	if got != want {
		t.Errorf("FormatForMax empty+media =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_ForwardedWithMediaAndFormatting(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:        "Hank",
		Timestamp:     ts(2026, 2, 14, 20, 0),
		ForwardedFrom: "Ivy",
		RawParts: []models.TextPart{
			{Type: "", Text: "Look at this "},
			{Type: "bold", Text: "amazing"},
			{Type: "", Text: " code:\n"},
			{Type: "pre", Text: "fmt.Println(\"hello\")"},
		},
		Media: []models.MediaFile{
			{Type: models.MediaDocument, FileName: "main.go"},
		},
	}

	got := c.FormatForMax(msg)
	want := "**Hank** · 14 фев 2026, 20:00\n" +
		"↩️ Переслано от Ivy\n" +
		"Look at this **amazing** code:\n" +
		"```\nfmt.Println(\"hello\")\n```" +
		"\n📎 main.go"
	if got != want {
		t.Errorf("FormatForMax complex =\n%q\nwant:\n%q", got, want)
	}
}

func TestSplitMessage_ShortMessage(t *testing.T) {
	c := New()
	text := "Hello world"
	chunks := c.SplitMessage(text, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected %q, got %q", text, chunks[0])
	}
}

func TestSplitMessage_ExactLimit(t *testing.T) {
	c := New()
	text := "12345"
	chunks := c.SplitMessage(text, 5)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestSplitMessage_SplitsAtNewline(t *testing.T) {
	c := New()
	text := "line one\nline two\nline three"
	// maxLen=18 -> "line one\nline two\n" is 18 chars
	chunks := c.SplitMessage(text, 18)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %q", len(chunks), chunks)
	}
	if chunks[0] != "line one\nline two\n" {
		t.Errorf("chunk[0] = %q", chunks[0])
	}
	if chunks[1] != "line three" {
		t.Errorf("chunk[1] = %q", chunks[1])
	}
}

func TestSplitMessage_SplitsAtSpace(t *testing.T) {
	c := New()
	// No newlines, should split at space boundary
	text := "word1 word2 word3 word4"
	chunks := c.SplitMessage(text, 12)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %q", len(chunks), chunks)
	}
	// Verify no chunk exceeds maxLen
	for i, chunk := range chunks {
		if len(chunk) > 12 {
			t.Errorf("chunk[%d] exceeds maxLen: %q (len=%d)", i, chunk, len(chunk))
		}
	}
}

func TestSplitMessage_ForceSplitNoBreakpoint(t *testing.T) {
	c := New()
	text := "abcdefghijklmnop"
	chunks := c.SplitMessage(text, 5)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %q", len(chunks), chunks)
	}
	if chunks[0] != "abcde" {
		t.Errorf("chunk[0] = %q, want %q", chunks[0], "abcde")
	}
}

func TestSplitMessage_DefaultMaxLen(t *testing.T) {
	c := New()
	text := "short"
	chunks := c.SplitMessage(text, 0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with default maxLen, got %d", len(chunks))
	}
}

func TestSplitMessage_EmptyString(t *testing.T) {
	c := New()
	chunks := c.SplitMessage("", 100)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %q", chunks)
	}
}
