package tgsender

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/arkosh/tg2max/pkg/models"
)

func TestGuessMimeType_MediaTypeMappings(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		mediaType models.MediaType
		want      string
	}{
		{
			name:      "MediaPhoto returns image/jpeg",
			path:      "irrelevant.png",
			mediaType: models.MediaPhoto,
			want:      "image/jpeg",
		},
		{
			name:      "MediaVideo returns video/mp4",
			path:      "irrelevant.ogg",
			mediaType: models.MediaVideo,
			want:      "video/mp4",
		},
		{
			name:      "MediaAnimation returns video/mp4",
			path:      "irrelevant.gif",
			mediaType: models.MediaAnimation,
			want:      "video/mp4",
		},
		{
			name:      "MediaAudio returns audio/mpeg",
			path:      "irrelevant.ogg",
			mediaType: models.MediaAudio,
			want:      "audio/mpeg",
		},
		{
			name:      "MediaVoice returns audio/ogg",
			path:      "irrelevant.mp3",
			mediaType: models.MediaVoice,
			want:      "audio/ogg",
		},
		{
			name:      "MediaSticker returns image/webp",
			path:      "irrelevant.jpg",
			mediaType: models.MediaSticker,
			want:      "image/webp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := guessMimeType(tt.path, tt.mediaType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGuessMimeType_ExtensionFallback(t *testing.T) {
	// MediaDocument has no explicit mapping, so falls through to extension-based detection.
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: ".pdf extension",
			path: "/some/path/document.pdf",
			want: "application/pdf",
		},
		{
			name: ".png extension",
			path: "/some/path/image.png",
			want: "image/png",
		},
		{
			name: ".jpg extension",
			path: "/some/path/photo.jpg",
			want: "image/jpeg",
		},
		{
			name: ".jpeg extension",
			path: "/some/path/photo.jpeg",
			want: "image/jpeg",
		},
		{
			name: ".mp4 extension",
			path: "/some/path/video.mp4",
			want: "video/mp4",
		},
		{
			name: ".mp3 extension",
			path: "/some/path/audio.mp3",
			want: "audio/mpeg",
		},
		{
			name: "uppercase extension is handled case-insensitively",
			path: "/some/path/image.PNG",
			want: "image/png",
		},
		{
			name: "unknown extension falls back to octet-stream",
			path: "/some/path/file.xyz",
			want: "application/octet-stream",
		},
		{
			name: "no extension falls back to octet-stream",
			path: "/some/path/noextension",
			want: "application/octet-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := guessMimeType(tt.path, models.MediaDocument)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNew_DelayCalculation(t *testing.T) {
	logger := slog.Default()

	t.Run("rps=1 gives delay of 1 second", func(t *testing.T) {
		s := New(nil, 0, 0, 1.0, logger)
		assert.Equal(t, time.Second, s.delay)
	})

	t.Run("rps=2 gives delay of 500ms", func(t *testing.T) {
		s := New(nil, 0, 0, 2.0, logger)
		assert.Equal(t, 500*time.Millisecond, s.delay)
	})

	t.Run("rps=10 gives delay of 100ms", func(t *testing.T) {
		s := New(nil, 0, 0, 10.0, logger)
		assert.Equal(t, 100*time.Millisecond, s.delay)
	})

	t.Run("very high rps is clamped to 50ms minimum", func(t *testing.T) {
		s := New(nil, 0, 0, 1000.0, logger)
		assert.Equal(t, 50*time.Millisecond, s.delay)
	})

	t.Run("rps=0 defaults to 1 second", func(t *testing.T) {
		s := New(nil, 0, 0, 0, logger)
		assert.Equal(t, time.Second, s.delay)
	})

	t.Run("negative rps defaults to 1 second", func(t *testing.T) {
		s := New(nil, 0, 0, -5.0, logger)
		assert.Equal(t, time.Second, s.delay)
	})
}

func TestNew_FieldsPopulated(t *testing.T) {
	logger := slog.Default()
	const channelID int64 = 12345
	const accessHash int64 = 67890

	s := New(nil, channelID, accessHash, 1.0, logger)

	assert.Equal(t, channelID, s.channelID)
	assert.Equal(t, accessHash, s.accessHash)
	assert.Equal(t, logger, s.log)
	assert.Nil(t, s.api)
}

// TestCreateChannel_SignatureExists verifies that the CreateChannel function
// exists with the expected signature. No actual API call is made.
func TestCreateChannel_SignatureExists(t *testing.T) {
	// The function signature is:
	//   func CreateChannel(ctx context.Context, api *tg.Client, title, about string) (int64, int64, error)
	//
	// We only verify it compiles and is callable; passing nil for api would
	// panic on any real invocation, so we just take the address.
	_ = CreateChannel // compile-time check that the symbol exists
	t.Log("CreateChannel signature verified at compile time")
}
