package tgclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
)

// ChannelInfo holds metadata about a Telegram channel or supergroup.
type ChannelInfo struct {
	ID           int64
	AccessHash   int64
	Title        string
	Username     string
	MembersCount int
	IsChannel    bool // true = broadcast channel, false = supergroup
}

// InputChannel returns the tg.InputChannel for API calls.
func (c *ChannelInfo) InputChannel() *tg.InputChannel {
	return &tg.InputChannel{
		ChannelID:  c.ID,
		AccessHash: c.AccessHash,
	}
}

// ListChannels returns all channels and supergroups the user has access to.
func (c *Client) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	if c.api == nil {
		return nil, fmt.Errorf("client not running")
	}

	var channels []ChannelInfo
	var offsetDate int
	var offsetID int
	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}

	for {
		result, err := c.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      100,
		})
		if err != nil {
			return nil, fmt.Errorf("get dialogs: %w", err)
		}

		var dialogs []tg.DialogClass
		var chats []tg.ChatClass

		switch r := result.(type) {
		case *tg.MessagesDialogs:
			dialogs = r.Dialogs
			chats = r.Chats
		case *tg.MessagesDialogsSlice:
			dialogs = r.Dialogs
			chats = r.Chats
		default:
			return channels, nil
		}

		if len(dialogs) == 0 {
			break
		}

		// Build a map of chats for quick lookup
		chatMap := make(map[int64]tg.ChatClass, len(chats))
		for _, chat := range chats {
			switch ch := chat.(type) {
			case *tg.Channel:
				chatMap[ch.ID] = chat
			}
		}

		for _, d := range dialogs {
			dialog, ok := d.(*tg.Dialog)
			if !ok {
				continue
			}
			peerChannel, ok := dialog.Peer.(*tg.PeerChannel)
			if !ok {
				continue
			}
			chat, ok := chatMap[peerChannel.ChannelID]
			if !ok {
				continue
			}
			ch := chat.(*tg.Channel)
			info := ChannelInfo{
				ID:         ch.ID,
				AccessHash: ch.AccessHash,
				Title:      ch.Title,
				Username:   ch.Username,
				IsChannel:  ch.Broadcast,
			}
			if ch.ParticipantsCount > 0 {
				info.MembersCount = ch.ParticipantsCount
			}
			channels = append(channels, info)
		}

		// Hard limit: 500 channels
		if len(channels) >= 500 {
			break
		}

		// Non-slice response means all dialogs fit in one page
		if _, ok := result.(*tg.MessagesDialogs); ok {
			break
		}
		if len(dialogs) < 100 {
			break
		}

		// Build message map for offset lookup
		var msgs []tg.MessageClass
		switch r := result.(type) {
		case *tg.MessagesDialogsSlice:
			msgs = r.Messages
		}
		msgMap := make(map[int]int) // msg ID -> date
		for _, m := range msgs {
			if msg, ok := m.(*tg.Message); ok {
				msgMap[msg.ID] = msg.Date
			}
		}

		// Update offset from last dialog's top message
		lastDialog, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
		if !ok {
			break
		}
		offsetID = lastDialog.TopMessage
		if date, exists := msgMap[lastDialog.TopMessage]; exists {
			offsetDate = date
		}
		offsetPeer = peerToInputPeer(lastDialog.Peer)
	}

	return channels, nil
}

// SearchChannels searches for channels matching the query string (case-insensitive substring).
func (c *Client) SearchChannels(ctx context.Context, query string) ([]ChannelInfo, error) {
	all, err := c.ListChannels(ctx)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []ChannelInfo
	for _, ch := range all {
		if strings.Contains(strings.ToLower(ch.Title), query) {
			results = append(results, ch)
		}
	}
	return results, nil
}

const maxCloneMessages = 50000 // hard limit to prevent OOM on huge channels

// ReadHistory reads all messages from a channel in chronological order.
// Downloads media to mediaDir. Returns up to maxCloneMessages results.
func (c *Client) ReadHistory(ctx context.Context, channel *ChannelInfo, mediaDir string) ([]ReadHistoryResult, error) {
	if c.api == nil {
		return nil, fmt.Errorf("client not running")
	}

	var allMessages []ReadHistoryResult
	var offsetID int

	inputChannel := channel.InputChannel()
	inputPeer := &tg.InputPeerChannel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
	}

	for {
		select {
		case <-ctx.Done():
			return allMessages, ctx.Err()
		default:
		}

		result, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     inputPeer,
			OffsetID: offsetID,
			Limit:    100,
		})
		if err != nil {
			return allMessages, fmt.Errorf("get history offset=%d: %w", offsetID, err)
		}

		var messages []tg.MessageClass
		var users []tg.UserClass
		var chats []tg.ChatClass

		switch r := result.(type) {
		case *tg.MessagesMessages:
			messages = r.Messages
			users = r.Users
			chats = r.Chats
		case *tg.MessagesMessagesSlice:
			messages = r.Messages
			users = r.Users
			chats = r.Chats
		case *tg.MessagesChannelMessages:
			messages = r.Messages
			users = r.Users
			chats = r.Chats
		default:
			break
		}

		if len(messages) == 0 {
			break
		}

		_ = chats
		_ = inputChannel

		// Build user map
		userMap := make(map[int64]*tg.User, len(users))
		for _, u := range users {
			if user, ok := u.(*tg.User); ok {
				userMap[user.ID] = user
			}
		}

		for _, m := range messages {
			msg, ok := m.(*tg.Message)
			if !ok {
				continue // skip service messages
			}

			// Download media if present and mediaDir is set
			var mediaPath string
			if mediaDir != "" && msg.Media != nil {
				path, dlErr := c.DownloadMedia(ctx, msg.Media, msg.ID, mediaDir)
				if dlErr != nil {
					c.log.Warn("media download failed, skipping", "msg_id", msg.ID, "error", dlErr)
				} else {
					mediaPath = path
				}
			}

			allMessages = append(allMessages, ReadHistoryResult{
				Raw:       msg,
				UserMap:   userMap,
				MediaPath: mediaPath,
			})

			if offsetID == 0 || msg.ID < offsetID {
				offsetID = msg.ID
			}
		}

		if len(messages) < 100 {
			break
		}
		if len(allMessages) >= maxCloneMessages {
			c.log.Warn("clone message limit reached", "limit", maxCloneMessages, "channel", channel.Title)
			break
		}
	}

	// Reverse to chronological order (oldest first)
	for i, j := 0, len(allMessages)-1; i < j; i, j = i+1, j-1 {
		allMessages[i], allMessages[j] = allMessages[j], allMessages[i]
	}

	return allMessages, nil
}

// ReadHistoryResult holds a raw MTProto message, user context, and downloaded media path.
type ReadHistoryResult struct {
	Raw       *tg.Message
	UserMap   map[int64]*tg.User
	MediaPath string // local path to downloaded media file (empty if no media or download failed)
}

// GetChannelMessagesCount returns the approximate number of messages in a channel.
func (c *Client) GetChannelMessagesCount(ctx context.Context, channel *ChannelInfo) (int, error) {
	if c.api == nil {
		return 0, fmt.Errorf("client not running")
	}

	inputPeer := &tg.InputPeerChannel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
	}

	result, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  inputPeer,
		Limit: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("get channel message count: %w", err)
	}

	switch r := result.(type) {
	case *tg.MessagesMessages:
		return len(r.Messages), nil
	case *tg.MessagesMessagesSlice:
		return r.Count, nil
	case *tg.MessagesChannelMessages:
		return r.Count, nil
	}
	return 0, nil
}

// peerToInputPeer converts a tg.PeerClass to tg.InputPeerClass for dialog pagination offsets.
func peerToInputPeer(peer tg.PeerClass) tg.InputPeerClass {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return &tg.InputPeerUser{UserID: p.UserID}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerChannel:
		return &tg.InputPeerChannel{ChannelID: p.ChannelID}
	default:
		return &tg.InputPeerEmpty{}
	}
}
