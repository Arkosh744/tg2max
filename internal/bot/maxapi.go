package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	maxbotapi "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

type maxChat struct {
	id    int64
	title string
}

func (b *Bot) fetchMaxChats() []maxChat {
	if b.maxToken == "" {
		return nil
	}

	api, err := maxbotapi.New(b.maxToken)
	if err != nil {
		b.log.Debug("failed to create max api for chat list", "error", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var chats []maxChat
	var marker int64

	for {
		list, err := api.Chats.GetChats(ctx, 50, marker)
		if err != nil {
			b.log.Debug("failed to fetch max chats", "error", err)
			break
		}

		for _, c := range list.Chats {
			if c.Status != schemes.ACTIVE {
				continue
			}
			title := c.Title
			if c.Type == schemes.DIALOG {
				title = "Диалог"
			}
			chats = append(chats, maxChat{id: c.ChatId, title: title})
		}

		if list.Marker == nil || len(chats) >= 500 {
			break
		}
		marker = *list.Marker
	}

	return chats
}

func (b *Bot) searchMaxChats(msg *tgbotapi.Message) {
	query := strings.TrimSpace(msg.Text)

	// If user typed a number, treat as direct chat ID input
	if id, err := strconv.ParseInt(query, 10, 64); err == nil {
		sess := b.sessions.Get(msg.From.ID)
		if sess == nil {
			return
		}
		chatName := fmt.Sprintf("Chat %d", id)
		sess.mu.Lock()
		sess.MaxChatID = id
		sess.MaxChatName = chatName
		sess.State = StateAwaitingFilter
		sess.mu.Unlock()
		b.showFilterKeyboard(msg.Chat.ID, chatName, id)
		return
	}

	chats := b.fetchMaxChats()
	if len(chats) == 0 {
		b.reply(msg.Chat.ID, "Не удалось получить список чатов Max.\nОтправь числовой ID чата напрямую.")
		sess := b.sessions.Get(msg.From.ID)
		if sess != nil {
			sess.mu.Lock()
			sess.State = StateAwaitingChatID
			sess.mu.Unlock()
		}
		return
	}

	queryLower := strings.ToLower(query)
	var matched []maxChat
	for _, c := range chats {
		if strings.Contains(strings.ToLower(c.title), queryLower) {
			matched = append(matched, c)
		}
	}

	if len(matched) == 0 {
		b.reply(msg.Chat.ID, fmt.Sprintf("Чатов с названием «%s» не найдено.\nПопробуй другое название или отправь числовой ID.", query))
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, c := range matched {
		label := c.title
		if label == "" {
			label = fmt.Sprintf("Chat %d", c.id)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				label,
				fmt.Sprintf("select_chat:%d", c.id),
			),
		))
	}

	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Найдено чатов: %d. Выбери:", len(matched)))
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.api.Send(reply)
}
