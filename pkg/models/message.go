package models

import "time"

// Message represents a unified message from any source
type Message struct {
	ID            int
	Timestamp     time.Time
	Author        string
	AuthorID      string
	Text          string
	RawParts      []TextPart
	Media         []MediaFile
	ReplyToID     *int
	ForwardedFrom string
}

// TextPart represents a piece of text with formatting (from TG export)
type TextPart struct {
	Type string
	Text string
	Href string
}

// MediaFile represents an attached media file
type MediaFile struct {
	Type     MediaType
	FilePath string
	FileName string
	MimeType string
}

type MediaType string

const (
	MediaPhoto     MediaType = "photo"
	MediaVideo     MediaType = "video"
	MediaDocument  MediaType = "document"
	MediaAudio     MediaType = "audio"
	MediaSticker   MediaType = "sticker"
	MediaAnimation MediaType = "animation"
	MediaVoice     MediaType = "voice_message"
)

// ChatMapping represents a single TG chat -> Max channel mapping
type ChatMapping struct {
	Name         string
	TGExportPath string
	MaxChatID    int64
}

// MigrationCursor tracks progress for resume capability
type MigrationCursor struct {
	ChatName      string    `json:"chat_name"`
	LastMessageID int       `json:"last_message_id"`
	TotalMessages int       `json:"total_messages"`
	SentMessages  int       `json:"sent_messages"`
	UpdatedAt     time.Time `json:"updated_at"`
}
