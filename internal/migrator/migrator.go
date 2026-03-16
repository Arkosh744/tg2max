package migrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

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

func (m *Migrator) MigrateAll(ctx context.Context, mappings []models.ChatMapping) error {
	if err := m.cursor.Load(); err != nil {
		m.log.Warn("failed to load cursor, starting from scratch", "error", err)
	}

	for _, mapping := range mappings {
		m.log.Info("starting migration", "chat", mapping.Name)

		if err := m.Migrate(ctx, mapping); err != nil {
			if saveErr := m.cursor.Save(); saveErr != nil {
				m.log.Error("failed to save cursor", "error", saveErr)
			}
			return fmt.Errorf("migrate chat %s: %w", mapping.Name, err)
		}

		m.log.Info("migration complete", "chat", mapping.Name)
	}

	return m.cursor.Save()
}

func (m *Migrator) Migrate(ctx context.Context, mapping models.ChatMapping) error {
	reader := telegram.NewReader(mapping.TGExportPath)

	messages, err := reader.ReadAll(ctx)
	if err != nil {
		return fmt.Errorf("read telegram export: %w", err)
	}

	total := len(messages)
	lastID := m.cursor.GetLastMessageID(mapping.Name)
	sent := 0

	for _, msg := range messages {
		if msg.ID <= lastID {
			sent++
		}
	}

	m.log.Info("messages loaded", "chat", mapping.Name, "total", total, "skipping", sent)

	for _, msg := range messages {
		select {
		case <-ctx.Done():
			m.cursor.Save()
			return ctx.Err()
		default:
		}

		if msg.ID <= lastID {
			continue
		}

		if err := m.sendMessage(ctx, mapping.MaxChatID, msg); err != nil {
			m.cursor.Update(mapping.Name, msg.ID, total, sent)
			m.cursor.Save()
			return fmt.Errorf("send message %d: %w", msg.ID, err)
		}

		sent++
		m.cursor.Update(mapping.Name, msg.ID, total, sent)

		if sent%50 == 0 {
			m.log.Info("progress", "chat", mapping.Name, "sent", sent, "total", total)
			m.cursor.Save()
		}
	}

	return nil
}

func (m *Migrator) sendMessage(ctx context.Context, chatID int64, msg models.Message) error {
	text := m.converter.FormatForMax(msg)

	if len(msg.Media) == 0 {
		return m.sender.SendText(ctx, chatID, text)
	}

	first := msg.Media[0]
	var err error

	switch first.Type {
	case models.MediaPhoto, models.MediaSticker:
		err = m.sender.SendWithPhoto(ctx, chatID, text, first.FilePath)
	default:
		err = m.sender.SendWithMedia(ctx, chatID, text, first.FilePath, first.Type)
	}

	if err != nil {
		m.log.Warn("media upload failed, sending text only", "file", first.FileName, "error", err)
		return m.sender.SendText(ctx, chatID, text)
	}

	for _, media := range msg.Media[1:] {
		caption := media.FileName
		switch media.Type {
		case models.MediaPhoto, models.MediaSticker:
			err = m.sender.SendWithPhoto(ctx, chatID, caption, media.FilePath)
		default:
			err = m.sender.SendWithMedia(ctx, chatID, caption, media.FilePath, media.Type)
		}
		if err != nil {
			m.log.Warn("additional media upload failed", "file", media.FileName, "error", err)
		}
	}

	return nil
}
