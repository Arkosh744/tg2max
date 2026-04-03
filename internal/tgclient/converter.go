package tgclient

import (
	"strconv"
	"time"

	"github.com/gotd/td/tg"

	"github.com/arkosh/tg2max/pkg/models"
)

// ConvertMessage converts a gotd/td tg.Message to the unified models.Message format.
// mediaPath is the local path to the downloaded media file (empty if not downloaded).
func ConvertMessage(msg *tg.Message, users map[int64]*tg.User, mediaPath string) models.Message {
	result := models.Message{
		ID:        msg.ID,
		Timestamp: time.Unix(int64(msg.Date), 0),
	}

	// Author
	if fromID, ok := msg.GetFromID(); ok {
		if peer, ok := fromID.(*tg.PeerUser); ok {
			if user, exists := users[peer.UserID]; exists {
				result.Author = formatUserName(user)
				result.AuthorID = strconv.FormatInt(user.ID, 10)
			}
		}
	}

	// Text and entities
	if msg.Message != "" {
		result.RawParts = convertEntities(msg.Message, msg.Entities)
		if len(result.RawParts) == 0 {
			result.RawParts = []models.TextPart{{Type: "plain", Text: msg.Message}}
		}
	}

	// Reply
	if replyTo, ok := msg.GetReplyTo(); ok {
		if header, ok := replyTo.(*tg.MessageReplyHeader); ok {
			replyID := header.ReplyToMsgID
			result.ReplyToID = &replyID
		}
	}

	// Forward
	if fwd, ok := msg.GetFwdFrom(); ok {
		result.ForwardedFrom = formatForwardFrom(&fwd, users)
	}

	// Media
	if msg.Media != nil {
		result.Media = convertMedia(msg.Media)
		// Set downloaded file path on the first media item
		if mediaPath != "" && len(result.Media) > 0 {
			result.Media[0].FilePath = mediaPath
		}
	}

	return result
}

func formatUserName(user *tg.User) string {
	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}
	if name == "" {
		name = user.Username
	}
	return name
}

func formatForwardFrom(fwd *tg.MessageFwdHeader, users map[int64]*tg.User) string {
	if fwd.FromName != "" {
		return fwd.FromName
	}
	if peer, ok := fwd.FromID.(*tg.PeerUser); ok {
		if user, exists := users[peer.UserID]; exists {
			return formatUserName(user)
		}
	}
	return "Unknown"
}

// convertEntities maps TG message entities to models.TextPart slices.
func convertEntities(text string, entities []tg.MessageEntityClass) []models.TextPart {
	if len(entities) == 0 {
		return []models.TextPart{{Type: "plain", Text: text}}
	}

	runes := []rune(text)
	var parts []models.TextPart
	pos := 0

	for _, entity := range entities {
		offset, length := entityOffsetLength(entity)
		if offset < 0 || length <= 0 {
			continue
		}

		// Add plain text before this entity
		if offset > pos {
			end := offset
			if end > len(runes) {
				end = len(runes)
			}
			parts = append(parts, models.TextPart{
				Type: "plain",
				Text: string(runes[pos:end]),
			})
		}

		// Entity text
		end := offset + length
		if end > len(runes) {
			end = len(runes)
		}
		entityText := string(runes[offset:end])

		part := models.TextPart{Text: entityText}
		switch e := entity.(type) {
		case *tg.MessageEntityBold:
			part.Type = "bold"
		case *tg.MessageEntityItalic:
			part.Type = "italic"
		case *tg.MessageEntityCode:
			part.Type = "code"
		case *tg.MessageEntityPre:
			part.Type = "pre"
		case *tg.MessageEntityTextURL:
			part.Type = "text_link"
			part.Href = e.URL
		case *tg.MessageEntityURL:
			part.Type = "link"
			part.Href = entityText
		case *tg.MessageEntityMention:
			part.Type = "mention"
		case *tg.MessageEntityStrike:
			part.Type = "strikethrough"
		case *tg.MessageEntityBlockquote:
			part.Type = "blockquote"
		default:
			part.Type = "plain"
		}
		parts = append(parts, part)
		pos = end
	}

	// Remaining text
	if pos < len(runes) {
		parts = append(parts, models.TextPart{
			Type: "plain",
			Text: string(runes[pos:]),
		})
	}

	return parts
}

func entityOffsetLength(e tg.MessageEntityClass) (int, int) {
	switch entity := e.(type) {
	case *tg.MessageEntityBold:
		return entity.Offset, entity.Length
	case *tg.MessageEntityItalic:
		return entity.Offset, entity.Length
	case *tg.MessageEntityCode:
		return entity.Offset, entity.Length
	case *tg.MessageEntityPre:
		return entity.Offset, entity.Length
	case *tg.MessageEntityTextURL:
		return entity.Offset, entity.Length
	case *tg.MessageEntityURL:
		return entity.Offset, entity.Length
	case *tg.MessageEntityMention:
		return entity.Offset, entity.Length
	case *tg.MessageEntityStrike:
		return entity.Offset, entity.Length
	case *tg.MessageEntityBlockquote:
		return entity.Offset, entity.Length
	case *tg.MessageEntityUnderline:
		return entity.Offset, entity.Length
	case *tg.MessageEntityHashtag:
		return entity.Offset, entity.Length
	default:
		return -1, 0
	}
}

// convertMedia extracts media info from a tg.MessageMediaClass.
// Note: actual file download happens separately in ReadHistory.
func convertMedia(media tg.MessageMediaClass) []models.MediaFile {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		if m.Photo == nil {
			return nil
		}
		return []models.MediaFile{{
			Type:     models.MediaPhoto,
			FileName: "photo.jpg",
		}}
	case *tg.MessageMediaDocument:
		if m.Document == nil {
			return nil
		}
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return nil
		}
		mf := models.MediaFile{
			FileName: "document",
			MimeType: doc.MimeType,
		}
		// Determine type from attributes
		for _, attr := range doc.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeFilename:
				mf.FileName = a.FileName
			case *tg.DocumentAttributeVideo:
				mf.Type = models.MediaVideo
				_ = a
			case *tg.DocumentAttributeAudio:
				mf.Type = models.MediaAudio
				_ = a
			case *tg.DocumentAttributeSticker:
				mf.Type = models.MediaSticker
			case *tg.DocumentAttributeAnimated:
				mf.Type = models.MediaAnimation
			}
		}
		if mf.Type == "" {
			mf.Type = models.MediaDocument
		}
		return []models.MediaFile{mf}
	default:
		return nil
	}
}
