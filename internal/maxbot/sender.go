package maxbot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	maxbotapi "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
	"golang.org/x/time/rate"

	"github.com/arkosh/tg2max/pkg/models"
)

// retryBackoff defines delays between retry attempts.
var retryBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

type Sender struct {
	api     *maxbotapi.Api
	limiter *rate.Limiter
}

func NewSender(token string, rps int) (*Sender, error) {
	api, err := maxbotapi.New(token, maxbotapi.WithApiTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("create max bot api: %w", err)
	}

	return &Sender{
		api:     api,
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
	}, nil
}

// withRetry retries op on network errors and HTTP 429/5xx with exponential backoff.
func (s *Sender) withRetry(ctx context.Context, op func() error) error {
	backoff := retryBackoff
	var lastErr error
	for i := 0; i <= len(backoff); i++ {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
		if i < len(backoff) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff[i]):
			}
		}
	}
	return lastErr
}

// isRetryable returns true for network errors and HTTP 429/5xx responses.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for max-bot-api-client-go API error with status code
	var apiErr *maxbotapi.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= http.StatusInternalServerError
	}

	// Network errors (timeout, connection refused, etc.) are retryable
	// If it's not an API error, treat as network error
	return true
}

func (s *Sender) SendText(ctx context.Context, chatID int64, text string) error {
	return s.withRetry(ctx, func() error {
		if err := s.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		msg := maxbotapi.NewMessage().
			SetChat(chatID).
			SetText(text).
			SetFormat(schemes.Markdown)

		if err := s.api.Messages.Send(ctx, msg); err != nil {
			return fmt.Errorf("send text to chat %d: %w", chatID, err)
		}

		return nil
	})
}

func (s *Sender) SendWithPhoto(ctx context.Context, chatID int64, text string, photoPath string) error {
	return s.withRetry(ctx, func() error {
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

		msg := maxbotapi.NewMessage().
			SetChat(chatID).
			SetText(text).
			SetFormat(schemes.Markdown).
			AddPhoto(photo)

		if err := s.api.Messages.Send(ctx, msg); err != nil {
			return fmt.Errorf("send photo to chat %d: %w", chatID, err)
		}

		return nil
	})
}

func (s *Sender) SendWithMedia(ctx context.Context, chatID int64, text string, mediaPath string, mediaType models.MediaType) error {
	return s.withRetry(ctx, func() error {
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

		msg := maxbotapi.NewMessage().
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
	})
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
