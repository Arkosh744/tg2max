package converter

import (
	"fmt"
	"strings"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

var ruMonths = [12]string{"янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

type Converter struct{}

func New() *Converter {
	return &Converter{}
}

func (c *Converter) FormatForMax(msg models.Message) string {
	var b strings.Builder

	// Header: **Author** · timestamp
	b.WriteString(fmt.Sprintf("**%s** · %s\n", msg.Author, c.FormatTimestamp(msg.Timestamp)))

	// Forwarded prefix
	if msg.ForwardedFrom != "" {
		b.WriteString(fmt.Sprintf("↩️ Переслано от %s\n", msg.ForwardedFrom))
	}

	// Body text from parts
	body := c.ConvertParts(msg.RawParts)
	if body != "" {
		b.WriteString(body)
	}

	// Media attachments info
	for _, media := range msg.Media {
		b.WriteString(fmt.Sprintf("\n📎 %s", media.FileName))
	}

	return b.String()
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

func (c *Converter) FormatTimestamp(t time.Time) string {
	return fmt.Sprintf("%d %s %d, %02d:%02d",
		t.Day(), ruMonths[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}
