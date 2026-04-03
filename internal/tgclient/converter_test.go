package tgclient

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/assert"

	"github.com/arkosh/tg2max/pkg/models"
)

func makeUser(id int64, first, last, username string) *tg.User {
	u := &tg.User{}
	u.ID = id
	u.FirstName = first
	u.LastName = last
	u.Username = username
	return u
}

// TestConvertMessage_BasicText covers ID, Timestamp and plain text → RawParts.
func TestConvertMessage_BasicText(t *testing.T) {
	msg := &tg.Message{
		ID:      42,
		Date:    1700000000,
		Message: "Hello world",
	}

	got := ConvertMessage(msg, nil, "")

	assert.Equal(t, 42, got.ID)
	assert.Equal(t, time.Unix(1700000000, 0), got.Timestamp)
	assert.Equal(t, []models.TextPart{{Type: "plain", Text: "Hello world"}}, got.RawParts)
	assert.Empty(t, got.Author)
	assert.Empty(t, got.AuthorID)
}

// TestConvertMessage_EmptyMessage tests a message with no text and no media.
func TestConvertMessage_EmptyMessage(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}

	got := ConvertMessage(msg, nil, "")

	assert.Equal(t, 1, got.ID)
	assert.Nil(t, got.RawParts)
	assert.Nil(t, got.Media)
	assert.Empty(t, got.Author)
}

// TestConvertMessage_AuthorFromUsersMap tests author extraction via FromID PeerUser.
func TestConvertMessage_AuthorFromUsersMap(t *testing.T) {
	user := makeUser(100, "Ivan", "Petrov", "ivanp")
	users := map[int64]*tg.User{100: user}

	msg := &tg.Message{ID: 1, Date: 1700000000, Message: "hi"}
	msg.SetFromID(&tg.PeerUser{UserID: 100})

	got := ConvertMessage(msg, users, "")

	assert.Equal(t, "Ivan Petrov", got.Author)
	assert.Equal(t, "100", got.AuthorID)
}

// TestConvertMessage_AuthorNoLastName tests author with only first name.
func TestConvertMessage_AuthorNoLastName(t *testing.T) {
	user := makeUser(200, "Anna", "", "anna_x")
	users := map[int64]*tg.User{200: user}

	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetFromID(&tg.PeerUser{UserID: 200})

	got := ConvertMessage(msg, users, "")

	assert.Equal(t, "Anna", got.Author)
}

// TestConvertMessage_AuthorFallbackToUsername tests fallback to Username when names are empty.
func TestConvertMessage_AuthorFallbackToUsername(t *testing.T) {
	user := makeUser(300, "", "", "botuser")
	users := map[int64]*tg.User{300: user}

	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetFromID(&tg.PeerUser{UserID: 300})

	got := ConvertMessage(msg, users, "")

	assert.Equal(t, "botuser", got.Author)
}

// TestConvertMessage_NoFromID tests that author is empty when FromID is not set.
func TestConvertMessage_NoFromID(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000, Message: "text"}

	got := ConvertMessage(msg, nil, "")

	assert.Empty(t, got.Author)
	assert.Empty(t, got.AuthorID)
}

// TestConvertMessage_FromIDUserNotInMap tests that author is empty when user not in map.
func TestConvertMessage_FromIDUserNotInMap(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetFromID(&tg.PeerUser{UserID: 999})

	got := ConvertMessage(msg, map[int64]*tg.User{}, "")

	assert.Empty(t, got.Author)
	assert.Empty(t, got.AuthorID)
}

// TestConvertMessage_ReplyTo tests ReplyToID extraction from MessageReplyHeader.
func TestConvertMessage_ReplyTo(t *testing.T) {
	msg := &tg.Message{ID: 5, Date: 1700000000, Message: "reply"}
	header := &tg.MessageReplyHeader{}
	header.ReplyToMsgID = 3
	msg.SetReplyTo(header)

	got := ConvertMessage(msg, nil, "")

	assert.NotNil(t, got.ReplyToID)
	assert.Equal(t, 3, *got.ReplyToID)
}

// TestConvertMessage_NoReply tests that ReplyToID is nil when no reply set.
func TestConvertMessage_NoReply(t *testing.T) {
	msg := &tg.Message{ID: 5, Date: 1700000000}

	got := ConvertMessage(msg, nil, "")

	assert.Nil(t, got.ReplyToID)
}

// TestConvertMessage_ForwardFromName tests ForwardedFrom set from FwdFrom.FromName.
func TestConvertMessage_ForwardFromName(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	fwd := tg.MessageFwdHeader{}
	fwd.SetFromName("Channel News")
	msg.SetFwdFrom(fwd)

	got := ConvertMessage(msg, nil, "")

	assert.Equal(t, "Channel News", got.ForwardedFrom)
}

// TestConvertMessage_ForwardFromUser tests ForwardedFrom set from user in map.
func TestConvertMessage_ForwardFromUser(t *testing.T) {
	user := makeUser(50, "Boris", "Sidorov", "boris")
	users := map[int64]*tg.User{50: user}

	msg := &tg.Message{ID: 1, Date: 1700000000}
	fwd := tg.MessageFwdHeader{}
	fwd.SetFromID(&tg.PeerUser{UserID: 50})
	msg.SetFwdFrom(fwd)

	got := ConvertMessage(msg, users, "")

	assert.Equal(t, "Boris Sidorov", got.ForwardedFrom)
}

// TestConvertMessage_ForwardUnknown tests ForwardedFrom = "Unknown" when user not found.
func TestConvertMessage_ForwardUnknown(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	fwd := tg.MessageFwdHeader{}
	fwd.SetFromID(&tg.PeerUser{UserID: 777})
	msg.SetFwdFrom(fwd)

	got := ConvertMessage(msg, map[int64]*tg.User{}, "")

	assert.Equal(t, "Unknown", got.ForwardedFrom)
}

// TestConvertMessage_Entities covers table-driven entity type conversion.
func TestConvertMessage_Entities(t *testing.T) {
	type entityCase struct {
		name     string
		text     string
		entity   tg.MessageEntityClass
		wantType string
		wantHref string
	}

	cases := []entityCase{
		{
			name:     "bold",
			text:     "bold text",
			entity:   &tg.MessageEntityBold{Offset: 0, Length: 9},
			wantType: "bold",
		},
		{
			name:     "italic",
			text:     "italic text",
			entity:   &tg.MessageEntityItalic{Offset: 0, Length: 11},
			wantType: "italic",
		},
		{
			name:     "code",
			text:     "code",
			entity:   &tg.MessageEntityCode{Offset: 0, Length: 4},
			wantType: "code",
		},
		{
			name:     "pre",
			text:     "preformatted",
			entity:   &tg.MessageEntityPre{Offset: 0, Length: 12},
			wantType: "pre",
		},
		{
			name:     "text_link",
			text:     "click here",
			entity:   &tg.MessageEntityTextURL{Offset: 0, Length: 10, URL: "https://example.com"},
			wantType: "text_link",
			wantHref: "https://example.com",
		},
		{
			name:     "mention",
			text:     "@username",
			entity:   &tg.MessageEntityMention{Offset: 0, Length: 9},
			wantType: "mention",
		},
		{
			name:     "strikethrough",
			text:     "striked",
			entity:   &tg.MessageEntityStrike{Offset: 0, Length: 7},
			wantType: "strikethrough",
		},
		{
			name:     "blockquote",
			text:     "quoted text",
			entity:   &tg.MessageEntityBlockquote{Offset: 0, Length: 11},
			wantType: "blockquote",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &tg.Message{
				ID:      1,
				Date:    1700000000,
				Message: tc.text,
			}
			msg.SetEntities([]tg.MessageEntityClass{tc.entity})

			got := ConvertMessage(msg, nil, "")

			assert.Len(t, got.RawParts, 1)
			assert.Equal(t, tc.wantType, got.RawParts[0].Type)
			assert.Equal(t, tc.text, got.RawParts[0].Text)
			if tc.wantHref != "" {
				assert.Equal(t, tc.wantHref, got.RawParts[0].Href)
			}
		})
	}
}

// TestConvertMessage_MixedEntitiesWithGaps tests plain text gaps between entities.
func TestConvertMessage_MixedEntitiesWithGaps(t *testing.T) {
	// "Hello **world** end" — bold covers "world" (offset=6, len=5)
	text := "Hello world end"
	msg := &tg.Message{
		ID:      1,
		Date:    1700000000,
		Message: text,
	}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 6, Length: 5},
	})

	got := ConvertMessage(msg, nil, "")

	assert.Equal(t, []models.TextPart{
		{Type: "plain", Text: "Hello "},
		{Type: "bold", Text: "world"},
		{Type: "plain", Text: " end"},
	}, got.RawParts)
}

// TestConvertMessage_MultipleEntities tests multiple sequential entities.
func TestConvertMessage_MultipleEntities(t *testing.T) {
	// "bold italic" — bold=0..4, italic=5..11
	text := "bold italic"
	msg := &tg.Message{
		ID:      1,
		Date:    1700000000,
		Message: text,
	}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 4},
		&tg.MessageEntityItalic{Offset: 5, Length: 6},
	})

	got := ConvertMessage(msg, nil, "")

	assert.Equal(t, []models.TextPart{
		{Type: "bold", Text: "bold"},
		{Type: "plain", Text: " "},
		{Type: "italic", Text: "italic"},
	}, got.RawParts)
}

// TestConvertMessage_MediaPhoto tests photo media conversion.
func TestConvertMessage_MediaPhoto(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	photo := &tg.Photo{}
	msg.SetMedia(&tg.MessageMediaPhoto{Photo: photo})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaPhoto, got.Media[0].Type)
	assert.Equal(t, "photo.jpg", got.Media[0].FileName)
}

// TestConvertMessage_MediaPhotoNil tests that nil photo produces no media.
func TestConvertMessage_MediaPhotoNil(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetMedia(&tg.MessageMediaPhoto{})

	got := ConvertMessage(msg, nil, "")

	assert.Nil(t, got.Media)
}

// TestConvertMessage_MediaVideo tests document with video attribute.
func TestConvertMessage_MediaVideo(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	doc := &tg.Document{
		MimeType: "video/mp4",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "clip.mp4"},
			&tg.DocumentAttributeVideo{},
		},
	}
	msg.SetMedia(&tg.MessageMediaDocument{Document: doc})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaVideo, got.Media[0].Type)
	assert.Equal(t, "clip.mp4", got.Media[0].FileName)
	assert.Equal(t, "video/mp4", got.Media[0].MimeType)
}

// TestConvertMessage_MediaAudio tests document with audio attribute.
func TestConvertMessage_MediaAudio(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	doc := &tg.Document{
		MimeType: "audio/mpeg",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "track.mp3"},
			&tg.DocumentAttributeAudio{},
		},
	}
	msg.SetMedia(&tg.MessageMediaDocument{Document: doc})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaAudio, got.Media[0].Type)
	assert.Equal(t, "track.mp3", got.Media[0].FileName)
}

// TestConvertMessage_MediaSticker tests document with sticker attribute.
func TestConvertMessage_MediaSticker(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	doc := &tg.Document{
		MimeType: "image/webp",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeSticker{},
		},
	}
	msg.SetMedia(&tg.MessageMediaDocument{Document: doc})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaSticker, got.Media[0].Type)
}

// TestConvertMessage_MediaDocument tests plain document (no video/audio/sticker attrs).
func TestConvertMessage_MediaDocument(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	doc := &tg.Document{
		MimeType: "application/pdf",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "report.pdf"},
		},
	}
	msg.SetMedia(&tg.MessageMediaDocument{Document: doc})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaDocument, got.Media[0].Type)
	assert.Equal(t, "report.pdf", got.Media[0].FileName)
	assert.Equal(t, "application/pdf", got.Media[0].MimeType)
}

// TestConvertMessage_MediaDocumentNil tests that nil document produces no media.
func TestConvertMessage_MediaDocumentNil(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetMedia(&tg.MessageMediaDocument{})

	got := ConvertMessage(msg, nil, "")

	assert.Nil(t, got.Media)
}

// TestConvertMessage_MediaAnimation tests document with animated attribute.
func TestConvertMessage_MediaAnimation(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	doc := &tg.Document{
		MimeType: "video/mp4",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAnimated{},
		},
	}
	msg.SetMedia(&tg.MessageMediaDocument{Document: doc})

	got := ConvertMessage(msg, nil, "")

	assert.Len(t, got.Media, 1)
	assert.Equal(t, models.MediaAnimation, got.Media[0].Type)
}

// TestConvertMessage_NoMedia tests message with nil media.
func TestConvertMessage_NoMedia(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000, Message: "text only"}

	got := ConvertMessage(msg, nil, "")

	assert.Nil(t, got.Media)
}

// TestConvertMessage_UnknownMediaType tests unknown media class returns no media.
func TestConvertMessage_UnknownMediaType(t *testing.T) {
	msg := &tg.Message{ID: 1, Date: 1700000000}
	msg.SetMedia(&tg.MessageMediaEmpty{})

	got := ConvertMessage(msg, nil, "")

	assert.Nil(t, got.Media)
}
