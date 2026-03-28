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

	// ts() creates UTC times; FormatTimestamp converts to MSK (UTC+3)
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"january", ts(2025, 1, 5, 9, 3), "5 янв 2025, 12:03 MSK"},
		{"february", ts(2024, 2, 14, 18, 30), "14 фев 2024, 21:30 MSK"},
		{"march", ts(2026, 3, 1, 0, 0), "1 мар 2026, 03:00 MSK"},
		{"april", ts(2025, 4, 10, 12, 45), "10 апр 2025, 15:45 MSK"},
		{"may", ts(2025, 5, 31, 23, 59), "1 июн 2025, 02:59 MSK"},
		{"june", ts(2025, 6, 15, 6, 7), "15 июн 2025, 09:07 MSK"},
		{"july", ts(2025, 7, 4, 14, 0), "4 июл 2025, 17:00 MSK"},
		{"august", ts(2025, 8, 20, 8, 10), "20 авг 2025, 11:10 MSK"},
		{"september", ts(2025, 9, 1, 11, 11), "1 сен 2025, 14:11 MSK"},
		{"october", ts(2025, 10, 31, 22, 0), "1 ноя 2025, 01:00 MSK"},
		{"november", ts(2025, 11, 7, 3, 15), "7 ноя 2025, 06:15 MSK"},
		{"december", ts(2025, 12, 25, 17, 30), "25 дек 2025, 20:30 MSK"},
		{"midnight_padding", ts(2025, 1, 1, 0, 0), "1 янв 2025, 03:00 MSK"},
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

	got := c.FormatForMax(msg, "")
	want := "Alice · 15 мар 2025, 17:30 MSK\nHello everyone"
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

	got := c.FormatForMax(msg, "")
	want := "Bob · 1 июн 2025, 13:00 MSK\n↩️ Переслано от Charlie\nOriginal message"
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

	got := c.FormatForMax(msg, "")
	want := "Dave · 25 дек 2025, 21:00 MSK\nCheck this out\n📎 photo.jpg"
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

	got := c.FormatForMax(msg, "")
	want := "Eve · 1 янв 2025, 03:00 MSK\n**Files**\n📎 report.pdf\n📎 screenshot.png"
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

	got := c.FormatForMax(msg, "")
	want := "Frank · 10 авг 2025, 15:00 MSK\n"
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

	got := c.FormatForMax(msg, "")
	want := "Grace · 20 май 2025, 12:15 MSK\n\n📎 clip.mp4"
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

	got := c.FormatForMax(msg, "")
	want := "Hank · 14 фев 2026, 23:00 MSK\n" +
		"↩️ Переслано от Ivy\n" +
		"Look at this **amazing** code:\n" +
		"```\nfmt.Println(\"hello\")\n```" +
		"\n📎 main.go"
	if got != want {
		t.Errorf("FormatForMax complex =\n%q\nwant:\n%q", got, want)
	}
}

// --- Feature 1: Reply chains ---

func TestFormatForMax_WithReplyText(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Alice",
		Timestamp: ts(2025, 3, 15, 14, 30),
		RawParts:  []models.TextPart{{Type: "", Text: "Agreed!"}},
	}
	replyText := "Let's meet at 5pm"

	got := c.FormatForMax(msg, replyText)
	want := "Alice · 15 мар 2025, 17:30 MSK\n> ↪ Let's meet at 5pm\nAgreed!"
	if got != want {
		t.Errorf("FormatForMax with reply =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_ReplyTextTruncatedAt60(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Bob",
		Timestamp: ts(2025, 3, 15, 14, 30),
		RawParts:  []models.TextPart{{Type: "", Text: "Yes"}},
	}
	// 61 rune long reply text — should be truncated to 60 + "..."
	replyText := "123456789012345678901234567890123456789012345678901234567890X"

	got := c.FormatForMax(msg, replyText)
	truncated := "123456789012345678901234567890123456789012345678901234567890"
	want := "Bob · 15 мар 2025, 17:30 MSK\n> ↪ " + truncated + "...\nYes"
	if got != want {
		t.Errorf("FormatForMax reply truncation =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_ReplyTextExactly60(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:    "Carol",
		Timestamp: ts(2025, 3, 15, 14, 30),
		RawParts:  []models.TextPart{{Type: "", Text: "OK"}},
	}
	// Exactly 60 runes — should NOT be truncated
	replyText := "123456789012345678901234567890123456789012345678901234567890"

	got := c.FormatForMax(msg, replyText)
	want := "Carol · 15 мар 2025, 17:30 MSK\n> ↪ " + replyText + "\nOK"
	if got != want {
		t.Errorf("FormatForMax reply exact 60 =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatGroupForMax_FirstMessageGetsReplyContext(t *testing.T) {
	c := New()
	now := ts(2025, 3, 15, 14, 30)
	msgs := []models.Message{
		{
			Author:    "Alice",
			Timestamp: now,
			RawParts:  []models.TextPart{{Type: "", Text: "First message"}},
		},
		{
			Author:    "Alice",
			Timestamp: now.Add(1 * time.Minute),
			RawParts:  []models.TextPart{{Type: "", Text: "Second message"}},
		},
	}

	got := c.FormatGroupForMax(msgs, "original text")
	// Reply context appears once after the header, before first body
	want := "Alice · 15 мар 2025, 17:30 MSK\n> ↪ original text\nFirst message\nSecond message"
	if got != want {
		t.Errorf("FormatGroupForMax reply =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatGroupForMax_NoReplyContext(t *testing.T) {
	c := New()
	now := ts(2025, 3, 15, 14, 30)
	msgs := []models.Message{
		{
			Author:    "Alice",
			Timestamp: now,
			RawParts:  []models.TextPart{{Type: "", Text: "Hello"}},
		},
		{
			Author:    "Alice",
			Timestamp: now.Add(1 * time.Minute),
			RawParts:  []models.TextPart{{Type: "", Text: "World"}},
		},
	}

	got := c.FormatGroupForMax(msgs, "")
	want := "Alice · 15 мар 2025, 17:30 MSK\nHello\nWorld"
	if got != want {
		t.Errorf("FormatGroupForMax no reply =\n%q\nwant:\n%q", got, want)
	}
}

// --- Feature 2: Sticker emoji ---

func TestFormatForMax_StickerEmoji(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:       "Alice",
		Timestamp:    ts(2025, 3, 15, 14, 30),
		StickerEmoji: "😂",
		Media: []models.MediaFile{
			{Type: models.MediaSticker, FileName: "sticker.webp"},
		},
	}

	got := c.FormatForMax(msg, "")
	want := "Alice · 15 мар 2025, 17:30 MSK\n\n😂 (стикер)"
	if got != want {
		t.Errorf("FormatForMax sticker =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_StickerEmojiNoMedia(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:       "Bob",
		Timestamp:    ts(2025, 3, 15, 14, 30),
		StickerEmoji: "👍",
	}

	got := c.FormatForMax(msg, "")
	want := "Bob · 15 мар 2025, 17:30 MSK\n\n👍 (стикер)"
	if got != want {
		t.Errorf("FormatForMax sticker no media =\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatForMax_StickerWithReplyAndEmoji(t *testing.T) {
	c := New()
	msg := models.Message{
		Author:       "Carol",
		Timestamp:    ts(2025, 3, 15, 14, 30),
		StickerEmoji: "🔥",
	}

	got := c.FormatForMax(msg, "some replied text")
	want := "Carol · 15 мар 2025, 17:30 MSK\n> ↪ some replied text\n\n🔥 (стикер)"
	if got != want {
		t.Errorf("FormatForMax sticker+reply =\n%q\nwant:\n%q", got, want)
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

// --- SplitMessage rune-based splitting ---

func TestSplitMessage_CyrillicText(t *testing.T) {
	c := New()
	// Each Cyrillic char is 1 rune but 2 bytes; splitting must use rune count, not byte count.
	// 10 Cyrillic chars = 10 runes. maxLen=5 → should produce 2 chunks of 5 runes each.
	text := "абвгдеёжзи" // 10 Cyrillic runes
	chunks := c.SplitMessage(text, 5)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for 10-rune Cyrillic text with maxLen=5, got %d: %q", len(chunks), chunks)
	}
	if chunks[0] != "абвгд" {
		t.Errorf("chunk[0] = %q, want %q", chunks[0], "абвгд")
	}
	if chunks[1] != "еёжзи" {
		t.Errorf("chunk[1] = %q, want %q", chunks[1], "еёжзи")
	}
}

func TestSplitMessage_MixedASCIIAndCyrillic(t *testing.T) {
	c := New()
	// "abc" (3 runes) + " " (1 rune) + "абв" (3 runes) = 7 runes total.
	// maxLen=4, no newline → split at space after "abc ".
	text := "abc абв"
	chunks := c.SplitMessage(text, 4)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %q", len(chunks), chunks)
	}
	// Verify each chunk is at most 4 runes
	for i, chunk := range chunks {
		runeCount := len([]rune(chunk))
		if runeCount > 4 {
			t.Errorf("chunk[%d] %q has %d runes, exceeds maxLen=4", i, chunk, runeCount)
		}
	}
	// Verify all runes present when joined
	joined := ""
	for _, chunk := range chunks {
		joined += chunk
	}
	// Account for trailing space consumed by split
	if len([]rune(joined)) < len([]rune(text))-1 {
		t.Errorf("chunks don't reconstruct original text: joined=%q original=%q", joined, text)
	}
}

func TestSplitMessage_LongCyrillicNoBreakpoint(t *testing.T) {
	c := New()
	// 20 Cyrillic runes with no spaces or newlines → force-split at rune boundary
	text := "абвгдеёжзиклмнопрсту" // 20 runes
	chunks := c.SplitMessage(text, 7)
	for i, chunk := range chunks {
		runeLen := len([]rune(chunk))
		if runeLen > 7 {
			t.Errorf("chunk[%d] %q has %d runes, exceeds maxLen=7", i, chunk, runeLen)
		}
	}
	// Reconstruct and verify no data loss
	var reconstructed string
	for _, chunk := range chunks {
		reconstructed += chunk
	}
	if reconstructed != text {
		t.Errorf("reconstructed = %q, want %q", reconstructed, text)
	}
}

// --- CanGroup tests ---

func TestCanGroup(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		a    models.Message
		b    models.Message
		want bool
	}{
		{
			name: "same_author_within_5min_no_media",
			a:    models.Message{Author: "Alice", Timestamp: now},
			b:    models.Message{Author: "Alice", Timestamp: now.Add(3 * time.Minute)},
			want: true,
		},
		{
			name: "different_authors",
			a:    models.Message{Author: "Alice", Timestamp: now},
			b:    models.Message{Author: "Bob", Timestamp: now.Add(1 * time.Minute)},
			want: false,
		},
		{
			name: "same_author_exactly_5min",
			a:    models.Message{Author: "Alice", Timestamp: now},
			b:    models.Message{Author: "Alice", Timestamp: now.Add(5 * time.Minute)},
			want: true,
		},
		{
			name: "same_author_over_5min",
			a:    models.Message{Author: "Alice", Timestamp: now},
			b:    models.Message{Author: "Alice", Timestamp: now.Add(5*time.Minute + time.Second)},
			want: false,
		},
		{
			name: "same_author_b_has_media",
			a:    models.Message{Author: "Alice", Timestamp: now},
			b: models.Message{
				Author:    "Alice",
				Timestamp: now.Add(1 * time.Minute),
				Media:     []models.MediaFile{{Type: models.MediaPhoto}},
			},
			want: false,
		},
		{
			name: "same_author_a_has_media",
			a: models.Message{
				Author:    "Alice",
				Timestamp: now,
				Media:     []models.MediaFile{{Type: models.MediaVideo}},
			},
			b:    models.Message{Author: "Alice", Timestamp: now.Add(1 * time.Minute)},
			want: false,
		},
		{
			name: "empty_author",
			a:    models.Message{Author: "", Timestamp: now},
			b:    models.Message{Author: "", Timestamp: now.Add(1 * time.Minute)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanGroup(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CanGroup() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- NewWithTimezone tests ---

func TestNewWithTimezone_DifferentZoneProducesDifferentOutput(t *testing.T) {
	utcZone := time.UTC
	mskZone := time.FixedZone("MSK", 3*60*60)

	cUTC := NewWithTimezone(utcZone)
	cMSK := NewWithTimezone(mskZone)

	// 2025-03-15 12:00:00 UTC
	testTime := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)

	gotUTC := cUTC.FormatTimestamp(testTime)
	gotMSK := cMSK.FormatTimestamp(testTime)

	if gotUTC == gotMSK {
		t.Errorf("expected different output for UTC vs MSK, both got %q", gotUTC)
	}

	wantUTC := "15 мар 2025, 12:00 UTC"
	if gotUTC != wantUTC {
		t.Errorf("UTC timestamp = %q, want %q", gotUTC, wantUTC)
	}

	wantMSK := "15 мар 2025, 15:00 MSK"
	if gotMSK != wantMSK {
		t.Errorf("MSK timestamp = %q, want %q", gotMSK, wantMSK)
	}
}

func TestNewWithTimezone_NilSafeCustomZone(t *testing.T) {
	// Verify NewWithTimezone works for arbitrary offsets
	nyZone := time.FixedZone("EST", -5*60*60)
	c := NewWithTimezone(nyZone)
	testTime := time.Date(2025, 6, 15, 20, 0, 0, 0, time.UTC) // 20:00 UTC = 15:00 EST
	got := c.FormatTimestamp(testTime)
	want := "15 июн 2025, 15:00 EST"
	if got != want {
		t.Errorf("EST timestamp = %q, want %q", got, want)
	}
}
