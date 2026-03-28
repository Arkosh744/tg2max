package converter

import (
	"fmt"
	"strings"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

// MaxMessageLength is the maximum allowed message length for Max messenger.
const MaxMessageLength = 4096

var ruMonths = [12]string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

type Converter struct {
	tz *time.Location
}

func New() *Converter {
	return &Converter{tz: time.FixedZone("MSK", 3*60*60)}
}

func NewWithTimezone(tz *time.Location) *Converter {
	return &Converter{tz: tz}
}

// FormatForMax formats a single message for Max messenger.
// replyText is the plain-text preview of the replied-to message (may be empty).
func (c *Converter) FormatForMax(msg models.Message, replyText string) string {
	var b strings.Builder

	// Header: Author · timestamp
	b.WriteString(fmt.Sprintf("%s · %s\n", msg.Author, c.FormatTimestamp(msg.Timestamp)))

	// Forwarded prefix
	if msg.ForwardedFrom != "" {
		b.WriteString(fmt.Sprintf("↩️ Переслано от %s\n", msg.ForwardedFrom))
	}

	// Reply context — shown before body
	if replyText != "" {
		if len([]rune(replyText)) > 60 {
			replyText = string([]rune(replyText)[:60]) + "..."
		}
		b.WriteString("> ↪ " + replyText + "\n")
	}

	// Body text from parts
	body := c.ConvertParts(msg.RawParts)
	if body != "" {
		b.WriteString(body)
	}

	// Sticker: emit emoji label instead of media attachment line
	if msg.StickerEmoji != "" {
		b.WriteString("\n" + msg.StickerEmoji + " (стикер)")
		return b.String()
	}

	// Media attachments info
	for _, media := range msg.Media {
		b.WriteString(fmt.Sprintf("\n📎 %s", media.FileName))
	}

	return b.String()
}

// GroupWindow is the max duration between messages to be grouped.
const GroupWindow = 5 * time.Minute

// FormatGroupForMax formats a group of sequential messages from the same author.
// firstReplyText is the reply context for the first message (may be empty).
// Only the first message in the group shows reply context and the shared header.
func (c *Converter) FormatGroupForMax(msgs []models.Message, firstReplyText string) string {
	if len(msgs) == 0 {
		return ""
	}
	if len(msgs) == 1 {
		return c.FormatForMax(msgs[0], firstReplyText)
	}

	var b strings.Builder

	// Header from first message
	b.WriteString(fmt.Sprintf("%s · %s\n", msgs[0].Author, c.FormatTimestamp(msgs[0].Timestamp)))

	// Reply context for first message only
	if firstReplyText != "" {
		if len([]rune(firstReplyText)) > 60 {
			firstReplyText = string([]rune(firstReplyText)[:60]) + "..."
		}
		b.WriteString("> ↪ " + firstReplyText + "\n")
	}

	for i, msg := range msgs {
		if msg.ForwardedFrom != "" {
			b.WriteString(fmt.Sprintf("↩️ Переслано от %s\n", msg.ForwardedFrom))
		}
		body := c.ConvertParts(msg.RawParts)
		if body != "" {
			b.WriteString(body)
		}
		// Sticker emoji handling per message in group
		if msg.StickerEmoji != "" {
			b.WriteString("\n" + msg.StickerEmoji + " (стикер)")
		} else {
			for _, media := range msg.Media {
				b.WriteString(fmt.Sprintf("\n📎 %s", media.FileName))
			}
		}
		if i < len(msgs)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// CanGroup returns true if two messages can be grouped (same author, within GroupWindow, no media).
func CanGroup(a, b models.Message) bool {
	if a.Author != b.Author || a.Author == "" {
		return false
	}
	if len(b.Media) > 0 || len(a.Media) > 0 {
		return false
	}
	return b.Timestamp.Sub(a.Timestamp) <= GroupWindow
}

func (c *Converter) ConvertParts(parts []models.TextPart) string {
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "bold":
			b.WriteString("**")
			b.WriteString(p.Text)
			b.WriteString("**")
		case "italic":
			b.WriteString("*")
			b.WriteString(p.Text)
			b.WriteString("*")
		case "code":
			b.WriteString("`")
			b.WriteString(p.Text)
			b.WriteString("`")
		case "pre":
			b.WriteString("```\n")
			b.WriteString(p.Text)
			b.WriteString("\n```")
		case "text_link", "link":
			if p.Href != "" {
				b.WriteString("[")
				b.WriteString(p.Text)
				b.WriteString("](")
				b.WriteString(p.Href)
				b.WriteString(")")
			} else {
				b.WriteString(p.Text)
			}
		case "mention":
			b.WriteString("**")
			b.WriteString(p.Text)
			b.WriteString("**")
		case "strikethrough":
			b.WriteString("~~")
			b.WriteString(p.Text)
			b.WriteString("~~")
		case "blockquote":
			lines := strings.Split(p.Text, "\n")
			for i, line := range lines {
				b.WriteString("> ")
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
			}
		default:
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// SplitMessage splits text into chunks of at most maxLen runes (characters),
// preferring to break at newline boundaries to avoid splitting mid-word.
func (c *Converter) SplitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = MaxMessageLength
	}

	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string

	for len(runes) > maxLen {
		chunk := string(runes[:maxLen])

		splitAt := strings.LastIndex(chunk, "\n")
		if splitAt <= 0 {
			splitAt = strings.LastIndex(chunk, " ")
		}
		if splitAt <= 0 {
			splitAt = len(chunk)
		} else {
			splitAt++
		}

		chunks = append(chunks, chunk[:splitAt])
		runes = runes[len([]rune(chunk[:splitAt])):]
	}

	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}

	return chunks
}

func (c *Converter) FormatTimestamp(t time.Time) string {
	t = t.In(c.tz)
	return fmt.Sprintf("%d %s %d, %02d:%02d %s",
		t.Day(), ruMonths[t.Month()-1], t.Year(), t.Hour(), t.Minute(), c.tz.String())
}
