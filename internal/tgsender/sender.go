package tgsender

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/arkosh/tg2max/pkg/models"
)

// Sender implements migrator.Sender for sending messages to a Telegram channel via MTProto.
type Sender struct {
	api       *tg.Client
	channelID int64
	accessHash int64
	log       *slog.Logger
	delay     time.Duration // delay between sends for rate limiting
}

// New creates a new TG sender for the specified channel.
// rps controls the rate limit (messages per second).
func New(api *tg.Client, channelID, accessHash int64, rps float64, log *slog.Logger) *Sender {
	delay := time.Second
	if rps > 0 {
		delay = time.Duration(float64(time.Second) / rps)
	}
	// Telegram userbot rate limit: max ~20 msg/s, be conservative
	if delay < 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	return &Sender{
		api:        api,
		channelID:  channelID,
		accessHash: accessHash,
		log:        log,
		delay:      delay,
	}
}

func (s *Sender) inputPeer() *tg.InputPeerChannel {
	return &tg.InputPeerChannel{
		ChannelID:  s.channelID,
		AccessHash: s.accessHash,
	}
}

// SendText sends a text message to the channel.
func (s *Sender) SendText(ctx context.Context, _ int64, text string) error {
	time.Sleep(s.delay)

	return s.withFloodWait(ctx, func() error {
		_, err := s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:    s.inputPeer(),
			Message: text,
			RandomID: time.Now().UnixNano(),
		})
		return err
	})
}

// SendWithPhoto uploads a photo and sends it with a caption.
func (s *Sender) SendWithPhoto(ctx context.Context, _ int64, text string, photoPath string) error {
	time.Sleep(s.delay)

	file, err := s.uploadFile(ctx, photoPath)
	if err != nil {
		s.log.Warn("photo upload failed, sending text only", "path", photoPath, "error", err)
		return s.SendText(ctx, 0, text)
	}

	return s.withFloodWait(ctx, func() error {
		_, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer: s.inputPeer(),
			Media: &tg.InputMediaUploadedPhoto{
				File: file,
			},
			Message:  text,
			RandomID: time.Now().UnixNano(),
		})
		return err
	})
}

// SendWithMedia uploads a file and sends it as a document with a caption.
func (s *Sender) SendWithMedia(ctx context.Context, _ int64, text string, mediaPath string, mediaType models.MediaType) error {
	time.Sleep(s.delay)

	file, err := s.uploadFile(ctx, mediaPath)
	if err != nil {
		s.log.Warn("media upload failed, sending text only", "path", mediaPath, "error", err)
		return s.SendText(ctx, 0, text)
	}

	var attrs []tg.DocumentAttributeClass

	// Add filename attribute
	parts := strings.Split(mediaPath, "/")
	filename := parts[len(parts)-1]
	attrs = append(attrs, &tg.DocumentAttributeFilename{FileName: filename})

	// Add type-specific attributes
	switch mediaType {
	case models.MediaVideo, models.MediaAnimation:
		attrs = append(attrs, &tg.DocumentAttributeVideo{})
	case models.MediaAudio, models.MediaVoice:
		attrs = append(attrs, &tg.DocumentAttributeAudio{
			Voice: mediaType == models.MediaVoice,
		})
	}

	mimeType := guessMimeType(mediaPath, mediaType)

	return s.withFloodWait(ctx, func() error {
		_, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer: s.inputPeer(),
			Media: &tg.InputMediaUploadedDocument{
				File:       file,
				MimeType:   mimeType,
				Attributes: attrs,
			},
			Message:  text,
			RandomID: time.Now().UnixNano(),
		})
		return err
	})
}

// Close is a no-op for TG sender (client lifecycle managed externally).
func (s *Sender) Close() error {
	return nil
}

// CreateChannel creates a new Telegram channel and returns its ID and access hash.
func CreateChannel(ctx context.Context, api *tg.Client, title, about string) (int64, int64, error) {
	updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Title:     title,
		About:     about,
		Broadcast: true,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create channel: %w", err)
	}

	// Extract channel from updates
	switch u := updates.(type) {
	case *tg.Updates:
		for _, chat := range u.Chats {
			if ch, ok := chat.(*tg.Channel); ok {
				return ch.ID, ch.AccessHash, nil
			}
		}
	case *tg.UpdatesCombined:
		for _, chat := range u.Chats {
			if ch, ok := chat.(*tg.Channel); ok {
				return ch.ID, ch.AccessHash, nil
			}
		}
	}

	return 0, 0, fmt.Errorf("channel not found in response")
}

// uploadFile uploads a local file to Telegram using SaveFilePart.
func (s *Sender) uploadFile(ctx context.Context, path string) (tg.InputFileClass, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	fileID := time.Now().UnixNano()
	const partSize = 512 * 1024 // 512 KB parts

	totalParts := (len(data) + partSize - 1) / partSize

	for i := 0; i < totalParts; i++ {
		start := i * partSize
		end := start + partSize
		if end > len(data) {
			end = len(data)
		}

		ok, err := s.api.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
			FileID:   fileID,
			FilePart: i,
			Bytes:    data[start:end],
		})
		if err != nil {
			return nil, fmt.Errorf("upload part %d: %w", i, err)
		}
		if !ok {
			return nil, fmt.Errorf("upload part %d rejected", i)
		}
	}

	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]

	return &tg.InputFile{
		ID:          fileID,
		Parts:       totalParts,
		Name:        name,
	}, nil
}

// withFloodWait retries on FLOOD_WAIT errors from Telegram.
func (s *Sender) withFloodWait(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt < 5; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		// Check for FLOOD_WAIT
		errStr := err.Error()
		if !strings.Contains(errStr, "FLOOD_WAIT") && !strings.Contains(errStr, "FLOOD_PREMIUM_WAIT") {
			return err
		}

		// Extract wait time, default to 30s
		waitSec := 30
		if _, scanErr := fmt.Sscanf(errStr, "rpc error: FLOOD_WAIT_%d", &waitSec); scanErr != nil {
			waitSec = 30
		}

		s.log.Warn("flood wait", "seconds", waitSec, "attempt", attempt+1)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(waitSec) * time.Second):
		}
	}
	return fmt.Errorf("flood wait retries exhausted")
}

func guessMimeType(path string, mediaType models.MediaType) string {
	switch mediaType {
	case models.MediaPhoto:
		return "image/jpeg"
	case models.MediaVideo, models.MediaAnimation:
		return "video/mp4"
	case models.MediaAudio:
		return "audio/mpeg"
	case models.MediaVoice:
		return "audio/ogg"
	case models.MediaSticker:
		return "image/webp"
	}

	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	}
	return "application/octet-stream"
}
