package maxbot

import (
	"context"
	"fmt"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
	"golang.org/x/time/rate"

	"github.com/arkosh/tg2max/pkg/models"
)

type Sender struct {
	api     *maxbot.Api
	limiter *rate.Limiter
}

func NewSender(token string, rps int) (*Sender, error) {
	api, err := maxbot.New(token, maxbot.WithApiTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("create max bot api: %w", err)
	}

	return &Sender{
		api:     api,
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
	}, nil
}

func (s *Sender) SendText(ctx context.Context, chatID int64, text string) error {
	if err := s.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	msg := maxbot.NewMessage().
		SetChat(chatID).
		SetText(text).
		SetFormat(schemes.Markdown)

	if err := s.api.Messages.Send(ctx, msg); err != nil {
		return fmt.Errorf("send text to chat %d: %w", chatID, err)
	}

	return nil
}

func (s *Sender) SendWithPhoto(ctx context.Context, chatID int64, text string, photoPath string) error {
	if err := s.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	photo, err := s.api.Uploads.UploadPhotoFromFile(ctx, photoPath)
	if err != nil {
		return fmt.Errorf("upload photo %s: %w", photoPath, err)
	}

	if err := s.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	msg := maxbot.NewMessage().
		SetChat(chatID).
		SetText(text).
		SetFormat(schemes.Markdown).
		AddPhoto(photo)

	if err := s.api.Messages.Send(ctx, msg); err != nil {
		return fmt.Errorf("send photo to chat %d: %w", chatID, err)
	}

	return nil
}

func (s *Sender) SendWithMedia(ctx context.Context, chatID int64, text string, mediaPath string, mediaType models.MediaType) error {
	if err := s.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	uploadType := resolveUploadType(mediaType)

	uploaded, err := s.api.Uploads.UploadMediaFromFile(ctx, uploadType, mediaPath)
	if err != nil {
		return fmt.Errorf("upload media %s: %w", mediaPath, err)
	}

	if err := s.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait: %w", err)
	}

	msg := maxbot.NewMessage().
		SetChat(chatID).
		SetText(text).
		SetFormat(schemes.Markdown)

	switch uploadType {
	case schemes.VIDEO:
		msg.AddVideo(uploaded)
	case schemes.AUDIO:
		msg.AddAudio(uploaded)
	default:
		msg.AddFile(uploaded)
	}

	if err := s.api.Messages.Send(ctx, msg); err != nil {
		return fmt.Errorf("send media to chat %d: %w", chatID, err)
	}

	return nil
}

func resolveUploadType(mt models.MediaType) schemes.UploadType {
	switch mt {
	case models.MediaVideo, models.MediaAnimation:
		return schemes.VIDEO
	case models.MediaAudio, models.MediaVoice:
		return schemes.AUDIO
	default:
		return schemes.FILE
	}
}
