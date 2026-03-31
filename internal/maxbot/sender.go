package maxbot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	maxbotapi "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"

	"github.com/arkosh/tg2max/pkg/models"
)

// retryBackoff defines delays between retry attempts for 429/5xx errors.
// Max API has an undocumented per-chat cooldown (likely 30-120s after burst).
var retryBackoff = []time.Duration{30 * time.Second, 60 * time.Second, 60 * time.Second, 120 * time.Second, 120 * time.Second}

// RetryNotifyFunc is called when a retry is about to happen.
// attempt is 1-based, waitDur is how long we'll wait before the next try.
type RetryNotifyFunc func(attempt int, err error, waitDur time.Duration)

type Sender struct {
	api     *maxbotapi.Api
	delay   time.Duration // pause between sends to avoid 429
	onRetry RetryNotifyFunc
}

func NewSender(token string, rps float64) (*Sender, error) {
	api, err := maxbotapi.New(token, maxbotapi.WithApiTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("create max bot api: %w", err)
	}

	delay := time.Second // default 1 msg/sec
	if rps > 0 {
		delay = time.Duration(float64(time.Second) / rps)
	}

	return &Sender{
		api:   api,
		delay: delay,
	}, nil
}

// SetOnRetry sets a callback that fires before each retry wait.
func (s *Sender) SetOnRetry(fn RetryNotifyFunc) {
	s.onRetry = fn
}

// Close is a no-op for the bot API sender (no persistent connection).
func (s *Sender) Close() error {
	return nil
}

// wait pauses between sends to stay under Max API rate limits.
func (s *Sender) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
		return nil
	}
}

// withRetry retries op on network errors and HTTP 429/5xx with exponential backoff.
func (s *Sender) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for i := 0; i <= len(retryBackoff); i++ {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
		if i < len(retryBackoff) {
			if s.onRetry != nil {
				s.onRetry(i+1, lastErr, retryBackoff[i])
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryBackoff[i] + time.Duration(rand.Int63n(int64(retryBackoff[i]/5))) - retryBackoff[i]/10):
			}
		}
	}
	return lastErr
}

// isRetryable returns true for HTTP 429/5xx responses and network timeout errors.
// Unknown errors default to not retryable to avoid silent retry loops.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *maxbotapi.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}

	// Check if it's an upload error (HTTP 500 from upload CDN) — not retryable
	errStr := err.Error()
	if strings.Contains(errStr, "upload:") && strings.Contains(errStr, "500") {
		return false
	}

	// Network errors (timeout, connection refused) are retryable
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return true
	}

	// Default: not retryable (unknown errors should not be silently retried)
	return false
}

func (s *Sender) SendText(ctx context.Context, chatID int64, text string) error {
	if err := s.wait(ctx); err != nil {
		return err
	}

	return s.withRetry(ctx, func() error {
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
	if err := s.wait(ctx); err != nil {
		return err
	}

	// Upload once, outside retry loop
	photo, err := s.api.Uploads.UploadPhotoFromFile(ctx, photoPath)
	if err != nil {
		return fmt.Errorf("upload photo %s: %w", photoPath, err)
	}

	return s.withRetry(ctx, func() error {
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
	if err := s.wait(ctx); err != nil {
		return err
	}

	// Upload once, outside retry loop
	uploadType := resolveUploadType(mediaType)
	uploaded, err := s.api.Uploads.UploadMediaFromFile(ctx, uploadType, mediaPath)
	if err != nil && uploadType == schemes.VIDEO {
		uploaded, err = s.api.Uploads.UploadMediaFromFile(ctx, schemes.FILE, mediaPath)
		if err == nil {
			uploadType = schemes.FILE
		}
	}
	if err != nil {
		return fmt.Errorf("upload media %s: %w", mediaPath, err)
	}

	return s.withRetry(ctx, func() error {
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
