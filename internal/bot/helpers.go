package bot

import (
	"context"
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/arkosh/tg2max/internal/storage"
)

func (b *Bot) reply(chatID int64, text string) {
	b.log.Debug("sending reply", "chatID", chatID, "len", len(text))
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		b.log.Error("failed to send reply", "error", err, "chatID", chatID)
	}
}

func (b *Bot) replyWithKeyboard(chatID int64, text string, keyboard tgbotapi.ReplyKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	keyboard.ResizeKeyboard = true
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		b.log.Error("failed to send reply", "error", err)
	}
}

func (b *Bot) editMessage(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	if _, err := b.api.Send(edit); err != nil {
		b.log.Debug("failed to edit message", "error", err)
	}
}

// trackUser logs the user in persistent storage (fire-and-forget).
func (b *Bot) trackUser(ctx context.Context, from *tgbotapi.User) {
	if from == nil {
		return
	}
	if err := b.storage.UpsertUser(ctx, storage.User{
		TelegramID: from.ID,
		Username:   from.UserName,
		FirstName:  from.FirstName,
		LastName:   from.LastName,
	}); err != nil {
		b.log.Warn("track user failed", "error", err, "user_id", from.ID)
	}
}

// estimateETA returns a human-readable remaining time string based on cursor progress.
func (b *Bot) estimateETA(sess *Session) string {
	if sess == nil {
		return ""
	}
	sess.mu.Lock()
	cursorFile := sess.CursorFile
	cursorName := sess.CursorName
	startedAt := sess.MigrationStart
	sess.mu.Unlock()

	if cursorFile == "" || startedAt.IsZero() {
		return ""
	}

	sent, total := readCursorProgress(cursorFile, cursorName)
	if total <= 0 || sent <= 0 {
		return ""
	}

	elapsed := time.Since(startedAt).Seconds()
	speed := float64(sent) / elapsed
	if speed <= 0 {
		return ""
	}
	remaining := time.Duration(float64(total-sent) / speed * float64(time.Second))
	remaining = remaining.Round(time.Minute)

	if remaining < time.Minute {
		return "~1 мин"
	}
	hours := int(remaining.Hours())
	minutes := int(remaining.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("~%d ч %d мин", hours, minutes)
	}
	return fmt.Sprintf("~%d мин", minutes)
}

// checkBusy checks if another user is running a migration.
// If busy, sends a message to chatID with ETA, adds to waiting queue, and returns true.
func (b *Bot) checkBusy(chatID int64, userID int64) bool {
	active := b.sessions.GetActiveMigration()
	if active == nil || active.UserID == userID {
		return false
	}

	eta := b.estimateETA(active)

	msg := "⏳ Бот занят миграцией другого пользователя."
	if eta != "" {
		msg += fmt.Sprintf(" Попробуйте через %s.", eta)
	} else {
		msg += " Попробуйте позже."
	}
	msg += "\nВы получите уведомление когда бот освободится."
	b.reply(chatID, msg)

	// Add to waiting queue (deduplicate)
	b.addWaiting(chatID)
	return true
}

// addWaiting adds a chat ID to the notification queue (deduplicates).
func (b *Bot) addWaiting(chatID int64) {
	for _, id := range b.waitingUsers {
		if id == chatID {
			return
		}
	}
	b.waitingUsers = append(b.waitingUsers, chatID)
}

// notifyWaiting sends a notification to all waiting users and clears the queue.
func (b *Bot) notifyWaiting() {
	if len(b.waitingUsers) == 0 {
		return
	}
	for _, chatID := range b.waitingUsers {
		b.reply(chatID, "🔔 Бот свободен! Можешь отправить ZIP для миграции.")
	}
	b.waitingUsers = nil
}

