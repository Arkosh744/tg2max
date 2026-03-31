package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"time"

	"github.com/arkosh/tg2max/pkg/models"
)

// ReadResult contains the outcome of reading a Telegram export file.
type ReadResult struct {
	Messages []models.Message
	Skipped  int
	Total    int
}

type Reader struct {
	exportPath string
	baseDir    string
}

func NewReader(exportPath string) *Reader {
	return &Reader{
		exportPath: exportPath,
		baseDir:    filepath.Dir(exportPath),
	}
}

func (r *Reader) BasePath() string {
	return r.baseDir
}

func (r *Reader) ReadAll(ctx context.Context) (ReadResult, error) {
	f, err := os.Open(r.exportPath)
	if err != nil {
		return ReadResult{}, fmt.Errorf("read export file %s: %w", r.exportPath, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)

	// Expect opening '{' of the root object.
	if _, err := dec.Token(); err != nil {
		return ReadResult{}, fmt.Errorf("parse export json: %w", err)
	}

	// Scan top-level keys until we find "messages".
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return ReadResult{}, fmt.Errorf("parse export json: %w", err)
		}
		if key.(string) == "messages" {
			break
		}
		// Skip the value for any other top-level key.
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return ReadResult{}, fmt.Errorf("parse export json: %w", err)
		}
	}

	// Expect opening '[' of the messages array.
	if _, err := dec.Token(); err != nil {
		return ReadResult{}, fmt.Errorf("parse export json: expected messages array: %w", err)
	}

	var result ReadResult
	for dec.More() {
		select {
		case <-ctx.Done():
			return ReadResult{}, ctx.Err()
		default:
		}

		var msg tgMessage
		if err := dec.Decode(&msg); err != nil {
			return ReadResult{}, fmt.Errorf("parse export json: %w", err)
		}

		result.Total++

		if msg.Type != "message" {
			continue
		}

		m, err := r.convertMessage(msg)
		if err != nil {
			result.Skipped++
			slog.Warn("skipped message during conversion",
				"message_id", msg.ID,
				"error", err,
			)
			continue
		}
		result.Messages = append(result.Messages, m)
	}

	return result, nil
}

func (r *Reader) convertMessage(msg tgMessage) (models.Message, error) {
	ts, err := r.parseTimestamp(msg)
	if err != nil {
		return models.Message{}, err
	}

	m := models.Message{
		ID:            msg.ID,
		Timestamp:     ts,
		Author:        msg.From,
		AuthorID:      msg.FromID,
		ForwardedFrom: msg.ForwardedFrom,
		ReplyToID:     msg.ReplyToMsgID,
		StickerEmoji:  msg.StickerEmoji,
	}

	for _, part := range msg.Text.Parts {
		m.RawParts = append(m.RawParts, models.TextPart{
			Type: part.Type,
			Text: part.Text,
			Href: part.Href,
		})
	}

	if msg.Poll != nil {
		var sb strings.Builder
		fmt.Fprintf(&sb, "📊 Опрос: %s", msg.Poll.Question)
		for _, answer := range msg.Poll.Answers {
			fmt.Fprintf(&sb, "\n  • %s (%d)", answer.Text, answer.Voters)
		}
		m.RawParts = append(m.RawParts, models.TextPart{Text: sb.String()})
	}

	if msg.Contact != nil {
		c := msg.Contact
		name := strings.TrimSpace(c.FirstName + " " + c.LastName)
		m.RawParts = append(m.RawParts, models.TextPart{
			Text: fmt.Sprintf("👤 Контакт: %s, %s", name, c.PhoneNumber),
		})
	}

	if msg.Location != nil {
		m.RawParts = append(m.RawParts, models.TextPart{
			Text: fmt.Sprintf("📍 Геолокация: %g, %g", msg.Location.Latitude, msg.Location.Longitude),
		})
	}

	r.attachMedia(&m, msg)

	return m, nil
}

func (r *Reader) parseTimestamp(msg tgMessage) (time.Time, error) {
	if msg.DateUnixtime != "" {
		unix, err := strconv.ParseInt(msg.DateUnixtime, 10, 64)
		if err == nil {
			return time.Unix(unix, 0), nil
		}
	}
	if msg.Date != "" {
		t, err := time.Parse("2006-01-02T15:04:05", msg.Date)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no valid timestamp for message %d", msg.ID)
}

const notIncludedPlaceholder = "(File not included. Change data exporting settings to download.)"

func (r *Reader) attachMedia(m *models.Message, msg tgMessage) {
	if msg.Photo != "" && msg.Photo != notIncludedPlaceholder {
		if path, err := r.safeMediaPath(msg.Photo); err == nil {
			m.Media = append(m.Media, models.MediaFile{
				Type:     models.MediaPhoto,
				FilePath: path,
				FileName: filepath.Base(msg.Photo),
				MimeType: msg.MimeType,
			})
		}
	}

	if msg.File != "" && msg.File != notIncludedPlaceholder && msg.Photo == "" {
		if path, err := r.safeMediaPath(msg.File); err == nil {
			mediaType := r.resolveMediaType(msg.MediaType)
			m.Media = append(m.Media, models.MediaFile{
				Type:     mediaType,
				FilePath: path,
				FileName: filepath.Base(msg.File),
				MimeType: msg.MimeType,
			})
		}
	}
}

// safeMediaPath validates that the media path stays within the export directory.
func (r *Reader) safeMediaPath(rawPath string) (string, error) {
	if filepath.IsAbs(rawPath) {
		return "", fmt.Errorf("absolute media path rejected: %s", rawPath)
	}
	joined := filepath.Join(r.baseDir, rawPath)
	cleaned := filepath.Clean(joined)
	if !strings.HasPrefix(cleaned, filepath.Clean(r.baseDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("media path escapes export dir: %s", rawPath)
	}
	return cleaned, nil
}

func (r *Reader) resolveMediaType(tgType string) models.MediaType {
	switch tgType {
	case "video_file":
		return models.MediaVideo
	case "audio_file":
		return models.MediaAudio
	case "voice_message":
		return models.MediaVoice
	case "video_message":
		return models.MediaVideo
	case "sticker":
		return models.MediaSticker
	case "animation":
		return models.MediaAnimation
	default:
		return models.MediaDocument
	}
}
