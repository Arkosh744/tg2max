package migrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

// Stats holds migration statistics returned from MigrateAll.
type Stats struct {
	Sent           int
	Skipped        int
	MediaErrors    int
	Duration       time.Duration
	AuthorCounts   map[string]int // author name -> message count
	ForwardedCount int
	FailedFiles    []string // file names where media upload failed (sent as text instead)
}

type Sender interface {
	SendText(ctx context.Context, chatID int64, text string) error
	SendWithPhoto(ctx context.Context, chatID int64, text string, photoPath string) error
	SendWithMedia(ctx context.Context, chatID int64, text string, mediaPath string, mediaType models.MediaType) error
	Close() error
}

type Migrator struct {
	sender    Sender
	converter *converter.Converter
	cursor    *CursorManager
	log       *slog.Logger
	pauseCh   <-chan struct{} // if set, receives pause/resume signals
}

func New(sender Sender, conv *converter.Converter, cursorFile string, log *slog.Logger) *Migrator {
	return &Migrator{
		sender:    sender,
		converter: conv,
		cursor:    NewCursorManager(cursorFile),
		log:       log,
	}
}

// SetPauseCh sets the channel used to receive pause/resume signals.
// The channel must be buffered (cap >= 1). Send one value to pause, another to resume.
func (m *Migrator) SetPauseCh(ch <-chan struct{}) {
	m.pauseCh = ch
}

func (m *Migrator) MigrateAll(ctx context.Context, mappings []models.ChatMapping) (Stats, error) {
	defer func() {
		if err := m.sender.Close(); err != nil {
			m.log.Warn("failed to close sender", "error", err)
		}
	}()

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
		stats.ForwardedCount += chatStats.ForwardedCount
		stats.FailedFiles = append(stats.FailedFiles, chatStats.FailedFiles...)
		if stats.AuthorCounts == nil {
			stats.AuthorCounts = make(map[string]int)
		}
		for author, count := range chatStats.AuthorCounts {
			stats.AuthorCounts[author] += count
		}

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
	stats := Stats{AuthorCounts: make(map[string]int)}
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

	// Build reply index: message ID -> plain-text body (first 60 runes) for reply previews.
	replyIndex := make(map[int]string, len(messages))
	for _, msg := range messages {
		if len(msg.RawParts) == 0 {
			continue
		}
		var sb strings.Builder
		for _, p := range msg.RawParts {
			sb.WriteString(p.Text)
		}
		text := sb.String()
		runes := []rune(text)
		if len(runes) > 60 {
			text = string(runes[:60])
		}
		replyIndex[msg.ID] = text
	}

	lastSentID := lastID

	// Build list of messages to process (skip already sent), then apply content filters.
	pending := make([]models.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.ID > lastID {
			pending = append(pending, msg)
		}
	}
	if mapping.FilterType != "" || mapping.FilterMonths > 0 {
		before := len(pending)
		pending = filterMessages(pending, mapping.FilterType, mapping.FilterMonths)
		m.log.Info("filter applied",
			"chat", mapping.Name,
			"filter_type", mapping.FilterType,
			"filter_months", mapping.FilterMonths,
			"before", before,
			"after", len(pending),
		)
	}

	i := 0
	for i < len(pending) {
		select {
		case <-ctx.Done():
			m.cursor.Update(mapping.Name, lastSentID, total, sent)
			if err := m.cursor.Save(); err != nil {
				m.log.Error("failed to save cursor", "error", err)
			}
			return stats, ctx.Err()
		default:
		}

		// Collect a group of messages that can be merged
		group := []models.Message{pending[i]}
		for j := i + 1; j < len(pending); j++ {
			if !converter.CanGroup(group[len(group)-1], pending[j]) {
				break
			}
			group = append(group, pending[j])
		}

		var mediaErrs int
		var failedFile string
		var err error
		if len(group) > 1 {
			// Send grouped text as one message; only first message carries reply context.
			firstReplyText := replyTextFor(group[0], replyIndex)
			text := m.converter.FormatGroupForMax(group, firstReplyText)
			chunks := m.converter.SplitMessage(text, converter.MaxMessageLength)
			for _, chunk := range chunks {
				if err = m.sender.SendText(ctx, mapping.MaxChatID, chunk); err != nil {
					break
				}
			}
		} else {
			mediaErrs, failedFile, err = m.sendMessage(ctx, mapping.MaxChatID, group[0], replyIndex)
		}

		stats.MediaErrors += mediaErrs
		if failedFile != "" {
			stats.FailedFiles = append(stats.FailedFiles, failedFile)
		}
		if err != nil {
			m.cursor.Update(mapping.Name, lastSentID, total, sent)
			if err := m.cursor.Save(); err != nil {
				m.log.Error("failed to save cursor", "error", err)
			}
			return stats, fmt.Errorf("send message %d: %w", group[0].ID, err)
		}

		for _, msg := range group {
			sent++
			stats.Sent++
			lastSentID = msg.ID
			if msg.Author != "" {
				stats.AuthorCounts[msg.Author]++
			}
			if msg.ForwardedFrom != "" {
				stats.ForwardedCount++
			}
		}
		m.cursor.Update(mapping.Name, lastSentID, total, sent)

		if sent%10 == 0 {
			m.log.Info("progress", "chat", mapping.Name, "sent", sent, "total", total)
			if err := m.cursor.Save(); err != nil {
				m.log.Error("failed to save cursor", "error", err)
			}
		}

		i += len(group)

		// Check for pause signal (non-blocking check first)
		if m.pauseCh != nil {
			select {
			case <-m.pauseCh:
				m.log.Info("migration paused")
				m.cursor.Save()
				// Block until resume signal arrives
				<-m.pauseCh
				m.log.Info("migration resumed")
			default:
			}
		}
	}

	return stats, nil
}

// filterMessages returns messages matching the type and date filters from the mapping.
// filterType "text" keeps only messages without media; "media" keeps only those with media.
// filterMonths > 0 keeps only messages within the last N months.
func filterMessages(msgs []models.Message, filterType string, filterMonths int) []models.Message {
	var cutoff time.Time
	if filterMonths > 0 {
		cutoff = time.Now().AddDate(0, -filterMonths, 0)
	}

	result := make([]models.Message, 0, len(msgs))
	for _, m := range msgs {
		if filterMonths > 0 && m.Timestamp.Before(cutoff) {
			continue
		}
		switch filterType {
		case "text":
			if len(m.Media) > 0 {
				continue
			}
		case "media":
			if len(m.Media) == 0 {
				continue
			}
		}
		result = append(result, m)
	}
	return result
}

// replyTextFor returns the reply preview text for a message from the index.
func replyTextFor(msg models.Message, index map[int]string) string {
	if msg.ReplyToID == nil {
		return ""
	}
	return index[*msg.ReplyToID]
}

// sendMessage sends a single message and returns the number of media errors, the first failed
// file name (empty string if none), and any fatal send error.
// replyIndex maps message IDs to their plain-text body previews for reply context.
func (m *Migrator) sendMessage(ctx context.Context, chatID int64, msg models.Message, replyIndex map[int]string) (mediaErrors int, failedFile string, err error) {
	replyText := replyTextFor(msg, replyIndex)
	text := m.converter.FormatForMax(msg, replyText)

	// Sticker with emoji: skip media upload, send text-only (converter includes emoji label).
	if msg.StickerEmoji != "" {
		chunks := m.converter.SplitMessage(text, converter.MaxMessageLength)
		for _, chunk := range chunks {
			if err = m.sender.SendText(ctx, chatID, chunk); err != nil {
				return mediaErrors, failedFile, err
			}
		}
		return mediaErrors, failedFile, nil
	}

	// Split long messages and send text-only parts
	chunks := m.converter.SplitMessage(text, converter.MaxMessageLength)

	if len(msg.Media) == 0 {
		for _, chunk := range chunks {
			if err = m.sender.SendText(ctx, chatID, chunk); err != nil {
				return mediaErrors, failedFile, err
			}
		}
		return mediaErrors, failedFile, nil
	}

	// Send first chunk with primary media attachment
	firstChunk := chunks[0]
	first := msg.Media[0]

	switch first.Type {
	case models.MediaPhoto:
		err = m.sender.SendWithPhoto(ctx, chatID, firstChunk, first.FilePath)
	default:
		err = m.sender.SendWithMedia(ctx, chatID, firstChunk, first.FilePath, first.Type)
	}

	if err != nil {
		mediaErrors++
		failedFile = first.FileName
		m.log.Warn("media upload failed, sending text only", "file", first.FileName, "error", err)
		if sendErr := m.sender.SendText(ctx, chatID, firstChunk); sendErr != nil {
			return mediaErrors, failedFile, sendErr
		}
	}

	// Send remaining text chunks
	for _, chunk := range chunks[1:] {
		if err = m.sender.SendText(ctx, chatID, chunk); err != nil {
			return mediaErrors, failedFile, err
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

	return mediaErrors, failedFile, nil
}
