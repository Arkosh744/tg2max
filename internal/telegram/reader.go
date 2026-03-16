package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	data, err := os.ReadFile(r.exportPath)
	if err != nil {
		return ReadResult{}, fmt.Errorf("read export file %s: %w", r.exportPath, err)
	}

	var export tgExport
	if err := json.Unmarshal(data, &export); err != nil {
		return ReadResult{}, fmt.Errorf("parse export json: %w", err)
	}

	// Free raw bytes after parsing; keep only the decoded structs.
	data = nil

	result := ReadResult{
		Total: len(export.Messages),
	}

	for _, msg := range export.Messages {
		select {
		case <-ctx.Done():
			return ReadResult{}, ctx.Err()
		default:
		}

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
	}

	for _, part := range msg.Text.Parts {
		m.RawParts = append(m.RawParts, models.TextPart{
			Type: part.Type,
			Text: part.Text,
			Href: part.Href,
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

func (r *Reader) attachMedia(m *models.Message, msg tgMessage) {
	if msg.Photo != "" {
		m.Media = append(m.Media, models.MediaFile{
			Type:     models.MediaPhoto,
			FilePath: filepath.Join(r.baseDir, msg.Photo),
			FileName: filepath.Base(msg.Photo),
			MimeType: msg.MimeType,
		})
	}

	if msg.File != "" && msg.Photo == "" {
		mediaType := r.resolveMediaType(msg.MediaType)
		m.Media = append(m.Media, models.MediaFile{
			Type:     mediaType,
			FilePath: filepath.Join(r.baseDir, msg.File),
			FileName: filepath.Base(msg.File),
			MimeType: msg.MimeType,
		})
	}
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
