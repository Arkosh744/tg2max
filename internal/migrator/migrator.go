package migrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

// Stats holds migration statistics returned from MigrateAll.
type Stats struct {
	Sent        int
	Skipped     int
	MediaErrors int
	Duration    time.Duration
}

type Sender interface {
	SendText(ctx context.Context, chatID int64, text string) error
	SendWithPhoto(ctx context.Context, chatID int64, text string, photoPath string) error
	SendWithMedia(ctx context.Context, chatID int64, text string, mediaPath string, mediaType models.MediaType) error
}

type Migrator struct {
	sender    Sender
	converter *converter.Converter
	cursor    *CursorManager
	log       *slog.Logger
}

func New(sender Sender, conv *converter.Converter, cursorFile string, log *slog.Logger) *Migrator {
	return &Migrator{
		sender:    sender,
		converter: conv,
		cursor:    NewCursorManager(cursorFile),
		log:       log,
	}
}

func (m *Migrator) MigrateAll(ctx context.Context, mappings []models.ChatMapping) (Stats, error) {
	start := time.Now()
	var stats Stats

	if err := m.cursor.Load(); err != nil {
		m.log.Warn("failed to load cursor, starting from scratch", "error", err)
	}

	for _, mapping := range mappings {
		m.log.Info("starting migration", "chat", mapping.Name)

		chatStats, err := m.migrate(ctx, mapping)
		stats.Sent += chatStats.Sent
		stats.Skipped += chatStats.Skipped
		stats.MediaErrors += chatStats.MediaErrors

		if err != nil {
			if saveErr := m.cursor.Save(); saveErr != nil {
				m.log.Error("failed to save cursor", "error", saveErr)
			}
			stats.Duration = time.Since(start)
			return stats, fmt.Errorf("migrate chat %s: %w", mapping.Name, err)
		}

		m.log.Info("migration complete", "chat", mapping.Name)
	}

	stats.Duration = time.Since(start)
	return stats, m.cursor.Save()
}

func (m *Migrator) migrate(ctx context.Context, mapping models.ChatMapping) (Stats, error) {
	var stats Stats
	reader := telegram.NewReader(mapping.TGExportPath)

	result, err := reader.ReadAll(ctx)
	if err != nil {
		return stats, fmt.Errorf("read telegram export: %w", err)
	}

	if result.Skipped > 0 {
		m.log.Warn("some messages were skipped during parsing",
			"chat", mapping.Name,
			"skipped", result.Skipped,
			"total_in_export", result.Total,
		)
	}

	messages := result.Messages
	total := len(messages)
	lastID := m.cursor.GetLastMessageID(mapping.Name)
	sent := 0

	for _, msg := range messages {
		if msg.ID <= lastID {
			sent++
			stats.Skipped++
		}
	}

	m.log.Info("messages loaded", "chat", mapping.Name, "total", total, "skipping", sent)

	lastSentID := lastID

	for _, msg := range messages {
		select {
		case <-ctx.Done():
			m.cursor.Update(mapping.Name, lastSentID, total, sent)
			m.cursor.Save()
			return stats, ctx.Err()
		default:
		}

		if msg.ID <= lastID {
			continue
		}

		mediaErrs, err := m.sendMessage(ctx, mapping.MaxChatID, msg)
		stats.MediaErrors += mediaErrs
		if err != nil {
			m.cursor.Update(mapping.Name, lastSentID, total, sent)
			m.cursor.Save()
			return stats, fmt.Errorf("send message %d: %w", msg.ID, err)
		}

		sent++
		stats.Sent++
		lastSentID = msg.ID
		m.cursor.Update(mapping.Name, lastSentID, total, sent)

		if sent%50 == 0 {
			m.log.Info("progress", "chat", mapping.Name, "sent", sent, "total", total)
			m.cursor.Save()
		}
	}

	return stats, nil
}

// sendMessage sends a single message and returns the number of media errors encountered.
func (m *Migrator) sendMessage(ctx context.Context, chatID int64, msg models.Message) (int, error) {
	text := m.converter.FormatForMax(msg)
	mediaErrors := 0

	// Split long messages and send text-only parts
	chunks := m.converter.SplitMessage(text, converter.MaxMessageLength)

	if len(msg.Media) == 0 {
		for _, chunk := range chunks {
			if err := m.sender.SendText(ctx, chatID, chunk); err != nil {
				return mediaErrors, err
			}
		}
		return mediaErrors, nil
	}

	// Send first chunk with primary media attachment
	firstChunk := chunks[0]
	first := msg.Media[0]
	var err error

	switch first.Type {
	case models.MediaPhoto, models.MediaSticker:
		err = m.sender.SendWithPhoto(ctx, chatID, firstChunk, first.FilePath)
	default:
		err = m.sender.SendWithMedia(ctx, chatID, firstChunk, first.FilePath, first.Type)
	}

	if err != nil {
		mediaErrors++
		m.log.Warn("media upload failed, sending text only", "file", first.FileName, "error", err)
		if sendErr := m.sender.SendText(ctx, chatID, firstChunk); sendErr != nil {
			return mediaErrors, sendErr
		}
	}

	// Send remaining text chunks
	for _, chunk := range chunks[1:] {
		if err := m.sender.SendText(ctx, chatID, chunk); err != nil {
			return mediaErrors, err
		}
	}

	// Send additional media attachments
	for _, media := range msg.Media[1:] {
		caption := media.FileName
		switch media.Type {
		case models.MediaPhoto, models.MediaSticker:
			err = m.sender.SendWithPhoto(ctx, chatID, caption, media.FilePath)
		default:
			err = m.sender.SendWithMedia(ctx, chatID, caption, media.FilePath, media.Type)
		}
		if err != nil {
			mediaErrors++
			m.log.Warn("additional media upload failed", "file", media.FileName, "error", err)
		}
	}

	return mediaErrors, nil
}
