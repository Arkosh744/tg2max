package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

const userbotSessionMaxAge = 30 * 24 * time.Hour // 30 days

// startUserbotSessionCleanup periodically removes expired MTProto sessions from the database.
func (b *Bot) startUserbotSessionCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed, err := b.storage.CleanExpiredUserbotSessions(ctx, userbotSessionMaxAge)
				if err != nil {
					b.log.Warn("failed to clean expired userbot sessions", "error", err)
				} else if removed > 0 {
					b.log.Info("cleaned expired userbot sessions", "removed", removed)
				}
			}
		}
	}()
}

// deleteUserMessage attempts to delete a user's message from the chat (for sensitive data like auth codes).
func (b *Bot) deleteUserMessage(msg *tgbotapi.Message) {
	del := tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID)
	if _, err := b.api.Request(del); err != nil {
		b.log.Warn("failed to delete user message (bot may not be admin)", "error", err, "chat", msg.Chat.ID)
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
	b.waitingMu.Lock()
	defer b.waitingMu.Unlock()
	for _, id := range b.waitingUsers {
		if id == chatID {
			return
		}
	}
	b.waitingUsers = append(b.waitingUsers, chatID)
}

// notifyWaiting sends a notification to all waiting users and clears the queue.
func (b *Bot) notifyWaiting() {
	b.waitingMu.Lock()
	users := b.waitingUsers
	b.waitingUsers = nil
	b.waitingMu.Unlock()

	for _, chatID := range users {
		b.reply(chatID, "🔔 Бот свободен! Можешь отправить ZIP для миграции.")
	}
}

// handleResumeExport continues migration from the saved cursor position.
func (b *Bot) handleResumeExport(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(chatID, "Нет загруженного экспорта.")
		return
	}
	sess.mu.Lock()
	sess.State = StateAwaitingChatSearch
	sess.mu.Unlock()
	b.replyWithKeyboard(chatID, "Продолжаем с сохранённым прогрессом.\nВведи название чата в Max для поиска.", keyboardMain())
}

// handleResetCursor removes the cursor file and restarts migration from scratch.
func (b *Bot) handleResetCursor(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(chatID, "Нет загруженного экспорта.")
		return
	}
	sess.mu.Lock()
	// Remove cursor to start from scratch
	if sess.CursorFile != "" {
		os.Remove(sess.CursorFile)
		sess.CursorFile = ""
		sess.CursorName = ""
	}
	// Also check for cursor.json next to the export
	dir := filepath.Dir(sess.ExportPath)
	os.Remove(filepath.Join(dir, "cursor.json"))
	sess.State = StateAwaitingChatSearch
	sess.mu.Unlock()
	b.replyWithKeyboard(chatID, "Прогресс сброшен. Миграция начнётся с начала.\nВведи название чата в Max для поиска.", keyboardMain())
}

