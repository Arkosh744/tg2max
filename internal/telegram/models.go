package telegram

import "encoding/json"

type tgExport struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	ID       int64       `json:"id"`
	Messages []tgMessage `json:"messages"`
}

type tgMessage struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Date          string `json:"date"`
	DateUnixtime  string `json:"date_unixtime"`
	From          string `json:"from"`
	FromID        string `json:"from_id"`
	Text          tgText `json:"text"`
	Photo         string `json:"photo"`
	File          string `json:"file"`
	MediaType     string `json:"media_type"`
	MimeType      string `json:"mime_type"`
	ReplyToMsgID  *int   `json:"reply_to_message_id"`
	ForwardedFrom string `json:"forwarded_from"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	DurationSec   int    `json:"duration_seconds"`
}

type tgText struct {
	Parts []tgTextPart
}

type tgTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Href string `json:"href,omitempty"`
}

func (t *tgText) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Parts = []tgTextPart{{Type: "plain", Text: s}}
		return nil
	}
	return json.Unmarshal(data, &t.Parts)
}
