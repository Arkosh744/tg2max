package telegram

import "encoding/json"

type tgExport struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	ID       int64       `json:"id"`
	Messages []tgMessage `json:"messages"`
}

type tgMessage struct {
	ID            int         `json:"id"`
	Type          string      `json:"type"`
	Date          string      `json:"date"`
	DateUnixtime  string      `json:"date_unixtime"`
	From          string      `json:"from"`
	FromID        string      `json:"from_id"`
	Text          tgText      `json:"text"`
	Photo         string      `json:"photo"`
	File          string      `json:"file"`
	MediaType     string      `json:"media_type"`
	MimeType      string      `json:"mime_type"`
	ReplyToMsgID  *int        `json:"reply_to_message_id"`
	ForwardedFrom string      `json:"forwarded_from"`
	StickerEmoji  string      `json:"sticker_emoji"`
	Width         int         `json:"width"`
	Height        int         `json:"height"`
	DurationSec   int         `json:"duration_seconds"`
	Poll          *tgPoll     `json:"poll"`
	Contact       *tgContact  `json:"contact_information"`
	Location      *tgLocation `json:"location_information"`
}

type tgPoll struct {
	Question    string          `json:"question"`
	Answers     []tgPollAnswer  `json:"answers"`
	TotalVoters int             `json:"total_voters"`
}

type tgPollAnswer struct {
	Text   string `json:"text"`
	Voters int    `json:"voters"`
}

type tgContact struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	PhoneNumber string `json:"phone_number"`
}

type tgLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
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
	// Case 1: plain string — "hello"
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Parts = []tgTextPart{{Type: "plain", Text: s}}
		return nil
	}

	// Case 2: array of mixed strings and objects — ["hello", {"type":"bold","text":"world"}]
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	t.Parts = make([]tgTextPart, 0, len(raw))
	for _, elem := range raw {
		var str string
		if err := json.Unmarshal(elem, &str); err == nil {
			t.Parts = append(t.Parts, tgTextPart{Type: "plain", Text: str})
			continue
		}
		var part tgTextPart
		if err := json.Unmarshal(elem, &part); err != nil {
			return err
		}
		t.Parts = append(t.Parts, part)
	}
	return nil
}
