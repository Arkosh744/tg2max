package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/maxbot"
	"github.com/arkosh/tg2max/internal/migrator"
	"github.com/arkosh/tg2max/internal/storage"
	"github.com/arkosh/tg2max/internal/tgclient"
	"github.com/arkosh/tg2max/internal/tgsender"
	"github.com/arkosh/tg2max/pkg/models"
)

// --- Step 0: /clone entry point ---

func (b *Bot) handleClone(msg *tgbotapi.Message) {
	if b.tgAppID == 0 || b.tgAppHash == "" {
		b.reply(msg.Chat.ID, "Функция клонирования не настроена. Администратор должен указать TG_APP_ID и TG_APP_HASH.")
		return
	}

	sess := b.sessions.GetOrCreate(msg.From.ID)
	sess.mu.Lock()
	state := sess.State

	// Rate limit: max 3 /clone per hour
	now := time.Now()
	if now.Sub(sess.CloneWindowStart) > time.Hour {
		sess.CloneAttempts = 0
		sess.CloneWindowStart = now
	}
	sess.CloneAttempts++
	if sess.CloneAttempts > 3 {
		sess.mu.Unlock()
		b.reply(msg.Chat.ID, "Слишком много попыток /clone. Попробуй через час.")
		return
	}
	sess.mu.Unlock()

	if state == StateMigrating || state == StatePaused ||
		state == StateCloneMigrating || state == StateClonePaused {
		b.reply(msg.Chat.ID, "Миграция уже идёт. Дождись завершения или /cancel.")
		return
	}

	// Cancel previous MTProto client if exists (Issue #11: prevent goroutine leaks)
	sess.mu.Lock()
	if sess.TGRunCancel != nil {
		sess.TGRunCancel()
		sess.TGRunCancel = nil
	}
	sess.TGClient = nil
	sess.TGAuth = nil
	sess.SourceChannel = nil
	sess.DestType = ""
	sess.CloneChannelID = 0
	sess.CloneChannelName = ""
	sess.mu.Unlock()

	// Try to reuse existing saved session
	if b.userbotSessionKey != nil {
		b.reply(msg.Chat.ID, "🔄 Проверяю сохранённую сессию...")
		if b.tryReuseSavedSession(msg.Chat.ID, msg.From.ID, sess) {
			return
		}
	}

	sess.mu.Lock()
	sess.State = StateAwaitingPhone
	sess.mu.Unlock()

	b.replyWithKeyboard(msg.Chat.ID,
		"⚠️ Для клонирования канала нужен доступ к вашему Telegram-аккаунту.\n\n"+
			"Сессия используется только для чтения канала и удаляется после завершения.\n\n"+
			"Введите номер телефона (формат: +7XXXXXXXXXX):",
		keyboardCloneAuth())
}

// tryReuseSavedSession attempts to connect using a previously saved MTProto session.
// Returns true if successful (user is already authenticated), false otherwise.
func (b *Bot) tryReuseSavedSession(chatID int64, userID int64, sess *Session) bool {
	sessStore, err := tgclient.NewEncryptedSessionStorage(
		userID,
		b.userbotSessionKey,
		b.storage.SaveUserbotSession,
		b.storage.LoadUserbotSession,
	)
	if err != nil {
		return false
	}

	// Check if there's a saved session
	saved, err := b.storage.LoadUserbotSession(context.Background(), userID)
	if err != nil || len(saved) == 0 {
		return false
	}

	client := tgclient.New(b.tgAppID, b.tgAppHash, sessStore, b.log)

	runCtx, runCancel := context.WithTimeout(context.Background(), 4*time.Hour)
	authOK := make(chan bool, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in MTProto session reuse", "recover", r)
				select {
				case authOK <- false:
				default:
				}
				runCancel()
			}
		}()
		err := client.Run(runCtx, func(ctx context.Context) error {
			self, selfErr := client.Self(ctx)
			if selfErr != nil {
				authOK <- false
				return selfErr
			}

			name := self.FirstName
			if self.LastName != "" {
				name += " " + self.LastName
			}

			sess.mu.Lock()
			sess.TGClient = client
			sess.TGRunCancel = runCancel
			sess.State = StateAwaitingChannelSearch
			sess.mu.Unlock()

			b.reply(chatID, fmt.Sprintf("✅ Сессия восстановлена! (%s)\n\nВведите название канала для поиска:", name))
			authOK <- true

			// Keep client alive
			<-ctx.Done()
			return nil
		})
		if err != nil {
			select {
			case authOK <- false:
			default:
			}
			runCancel()
		}
	}()

	// Wait for auth check with timeout
	select {
	case ok := <-authOK:
		if ok {
			b.log.Info("reused saved MTProto session", "user", userID)
			return true
		}
		runCancel()
		// Session expired, delete it
		b.storage.DeleteUserbotSession(context.Background(), userID)
		b.log.Info("saved session expired, requesting new auth", "user", userID)
		return false
	case <-time.After(10 * time.Second):
		runCancel()
		b.log.Warn("saved session check timed out", "user", userID)
		return false
	}
}

// --- Step 1: Phone number ---

func (b *Bot) handlePhone(msg *tgbotapi.Message) {
	phone := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(phone, "+") {
		b.reply(msg.Chat.ID, "Номер должен начинаться с +. Пример: +79991234567")
		return
	}

	sess := b.sessions.Get(msg.From.ID)
	if sess == nil {
		b.reply(msg.Chat.ID, "Сессия не найдена. Используй /clone.")
		return
	}

	// Create encrypted session storage for this user
	sessStore, err := tgclient.NewEncryptedSessionStorage(
		msg.From.ID,
		b.userbotSessionKey,
		b.storage.SaveUserbotSession,
		b.storage.LoadUserbotSession,
	)
	if err != nil {
		b.log.Error("create session storage failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка инициализации. Попробуй /clone заново.")
		return
	}

	client := tgclient.New(b.tgAppID, b.tgAppHash, sessStore, b.log)
	auth := tgclient.NewBotConversationAuth(phone)

	sess.mu.Lock()
	sess.TGClient = client
	sess.TGAuth = auth
	sess.State = StateAwaitingCode
	sess.mu.Unlock()

	// Run MTProto client and auth in a background goroutine.
	// The client stays connected until the clone flow completes or is cancelled.
	runCtx, runCancel := context.WithTimeout(context.Background(), 4*time.Hour)
	sess.mu.Lock()
	sess.TGRunCancel = runCancel
	sess.mu.Unlock()

	authDone := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in MTProto client", "recover", r, "user", msg.From.ID)
				select {
				case authDone <- fmt.Errorf("internal error: %v", r):
				default:
				}
				runCancel()
			}
		}()
		err := client.Run(runCtx, func(ctx context.Context) error {
			if authErr := client.Auth(ctx, auth); authErr != nil {
				authDone <- authErr
				return authErr
			}
			authDone <- nil
			<-ctx.Done()
			return nil
		})
		if err != nil {
			select {
			case authDone <- err:
			default:
			}
		}
	}()

	b.replyWithKeyboard(msg.Chat.ID, "📱 Код подтверждения будет отправлен в Telegram.\nВведите код:", keyboardCloneAuth())

	// Wait for auth result in another goroutine to handle timeout
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in auth watcher", "recover", r, "user", msg.From.ID)
			}
		}()
		select {
		case err := <-authDone:
			if err != nil {
				// Check if it's a 2FA requirement (Password was called)
				if auth.NeedsPassword() {
					b.replyWithKeyboard(msg.Chat.ID, "🔐 Требуется пароль двухфакторной аутентификации.\nВведите пароль:", keyboardCloneAuth())
					sess.mu.Lock()
					sess.State = StateAwaitingPassword
					sess.mu.Unlock()
					return
				}
				b.log.Error("clone auth failed", "error", err, "user", msg.From.ID)
				b.reply(msg.Chat.ID, fmt.Sprintf("❌ Ошибка авторизации: %v\nПопробуй /clone заново.", err))
				b.cleanupCloneSession(sess)
				return
			}
			b.onAuthSuccess(msg.Chat.ID, msg.From.ID, sess, client)
		case <-time.After(5 * time.Minute):
			b.reply(msg.Chat.ID, "⏰ Время ожидания кода истекло. Попробуй /clone заново.")
			b.cleanupCloneSession(sess)
		}
	}()
}

// --- Step 2: Auth code ---

func (b *Bot) handleCode(msg *tgbotapi.Message) {
	// Delete the message containing the auth code (security)
	b.deleteUserMessage(msg)

	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.TGAuth == nil {
		b.reply(msg.Chat.ID, "Сессия не найдена. Используй /clone.")
		return
	}
	sess.TGAuth.ProvideCode(msg.Text)
}

// --- Step 3: 2FA password ---

func (b *Bot) handlePassword(msg *tgbotapi.Message) {
	// Delete the message containing the 2FA password (security)
	b.deleteUserMessage(msg)

	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.TGAuth == nil {
		b.reply(msg.Chat.ID, "Сессия не найдена. Используй /clone.")
		return
	}
	sess.TGAuth.ProvidePassword(msg.Text)
	b.reply(msg.Chat.ID, "⏳ Проверяю пароль...")
}

// onAuthSuccess is called when MTProto authentication succeeds.
func (b *Bot) onAuthSuccess(chatID int64, userID int64, sess *Session, client *tgclient.Client) {
	self, err := client.Self(context.Background())
	if err != nil {
		b.log.Error("get self failed", "error", err)
		b.replyWithKeyboard(chatID, "✅ Авторизация успешна!\n\nВведите название канала для поиска:", keyboardCloneSearch())
	} else {
		name := self.FirstName
		if self.LastName != "" {
			name += " " + self.LastName
		}
		b.replyWithKeyboard(chatID, fmt.Sprintf("✅ Авторизация успешна! (%s)\n\nВведите название канала для поиска:", name), keyboardCloneSearch())
	}

	sess.mu.Lock()
	sess.State = StateAwaitingChannelSearch
	sess.mu.Unlock()
}

// --- Step 4: Channel search ---

func (b *Bot) handleChannelSearch(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.TGClient == nil {
		b.reply(msg.Chat.ID, "Сессия не найдена. Используй /clone.")
		return
	}

	b.reply(msg.Chat.ID, "🔍 Ищу каналы...")

	channels, err := sess.TGClient.SearchChannels(context.Background(), msg.Text)
	if err != nil {
		b.log.Error("search channels failed", "error", err, "query", msg.Text)
		b.reply(msg.Chat.ID, fmt.Sprintf("❌ Ошибка поиска: %v", err))
		return
	}

	if len(channels) == 0 {
		b.reply(msg.Chat.ID, "Каналы не найдены. Попробуйте другое название:")
		return
	}

	// Limit to 10 results
	if len(channels) > 10 {
		channels = channels[:10]
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, ch := range channels {
		label := ch.Title
		if ch.MembersCount > 0 {
			label += fmt.Sprintf(" (%s)", formatMembersCount(ch.MembersCount))
		}
		data := fmt.Sprintf("select_source:%d:%d", ch.ID, ch.AccessHash)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Найденные каналы:")
	reply.ReplyMarkup = keyboard
	if _, err := b.api.Send(reply); err != nil {
		b.log.Error("send channel list failed", "error", err)
	}

	sess.mu.Lock()
	sess.State = StateAwaitingChannelSelect
	sess.mu.Unlock()
}

// --- Step 5: Source channel selected ---

func (b *Bot) handleSourceSelect(chatID int64, userID int64, cb *tgbotapi.CallbackQuery) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.TGClient == nil {
		b.reply(chatID, "Сессия не найдена. Используй /clone.")
		return
	}

	// Parse "select_source:channelID:accessHash"
	parts := strings.Split(strings.TrimPrefix(cb.Data, "select_source:"), ":")
	if len(parts) != 2 {
		b.reply(chatID, "Ошибка выбора канала.")
		return
	}
	chID, _ := strconv.ParseInt(parts[0], 10, 64)
	accessHash, _ := strconv.ParseInt(parts[1], 10, 64)

	// Extract title from button text
	title := fmt.Sprintf("Channel %d", chID)
	if cb.Message != nil && cb.Message.ReplyMarkup != nil {
		for _, row := range cb.Message.ReplyMarkup.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData == cb.Data {
					title = btn.Text
					// Remove member count suffix
					if idx := strings.LastIndex(title, " ("); idx > 0 {
						title = title[:idx]
					}
					break
				}
			}
		}
	}

	info := &tgclient.ChannelInfo{
		ID:         chID,
		AccessHash: accessHash,
		Title:      title,
	}

	// Get message count
	count, err := sess.TGClient.GetChannelMessagesCount(context.Background(), info)
	if err != nil {
		b.log.Warn("get channel count failed", "error", err)
	}

	sess.mu.Lock()
	sess.SourceChannel = info
	sess.State = StateAwaitingDestChoice
	sess.mu.Unlock()

	text := fmt.Sprintf("📢 %s", title)
	if count > 0 {
		text += fmt.Sprintf(" — %d сообщений", count)
	}
	text += "\n\nКуда клонировать?"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 В Max", "dest_max"),
			tgbotapi.NewInlineKeyboardButtonData("📢 В Telegram", "dest_tg"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel_clone"),
		),
	)
	reply := tgbotapi.NewMessage(chatID, text)
	reply.ReplyMarkup = keyboard
	if _, err := b.api.Send(reply); err != nil {
		b.log.Error("send dest choice failed", "error", err)
	}
}

// --- Step 6: Destination choice ---

func (b *Bot) handleDestChoice(chatID int64, userID int64, cb *tgbotapi.CallbackQuery) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.SourceChannel == nil {
		b.reply(chatID, "Сессия не найдена. Используй /clone.")
		return
	}

	sess.mu.Lock()
	switch cb.Data {
	case "dest_max":
		sess.DestType = "max"
		sess.State = StateAwaitingCloneChat
		sess.mu.Unlock()
		b.replyWithKeyboard(chatID, "Введите название чата в Max для поиска.\nИли отправьте числовой ID чата:", keyboardCloneSearch())
	case "dest_tg":
		sess.DestType = "tg"
		sess.mu.Unlock()
		b.showCloneConfirm(chatID, sess)
	default:
		sess.mu.Unlock()
	}
}

// --- Step 6a: Max chat search (reuse existing pattern) ---

func (b *Bot) handleCloneChatSearch(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil {
		b.reply(msg.Chat.ID, "Сессия не найдена. Используй /clone.")
		return
	}

	// Try to parse as numeric ID
	if id, err := strconv.ParseInt(strings.TrimSpace(msg.Text), 10, 64); err == nil && id > 0 {
		sess.mu.Lock()
		sess.CloneChannelID = id
		sess.CloneChannelName = fmt.Sprintf("Chat %d", id)
		sess.mu.Unlock()
		b.showCloneConfirm(msg.Chat.ID, sess)
		return
	}

	// Search Max chats (reuse existing fetchMaxChats)
	chats := b.fetchMaxChats()
	if len(chats) == 0 {
		b.reply(msg.Chat.ID, "Не удалось получить список чатов Max. Отправьте числовой ID чата.")
		return
	}

	query := strings.ToLower(strings.TrimSpace(msg.Text))
	var matches []maxChat
	for _, c := range chats {
		if strings.Contains(strings.ToLower(c.title), query) {
			matches = append(matches, c)
		}
	}

	if len(matches) == 0 {
		b.reply(msg.Chat.ID, "Чаты не найдены. Попробуйте другое название или отправьте ID:")
		return
	}
	if len(matches) > 10 {
		matches = matches[:10]
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, c := range matches {
		data := fmt.Sprintf("select_clone_chat:%d", c.id)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(c.title, data),
		))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Выберите чат в Max:")
	reply.ReplyMarkup = keyboard
	if _, err := b.api.Send(reply); err != nil {
		b.log.Error("send clone chat list failed", "error", err)
	}
}

func (b *Bot) handleCloneChatSelect(chatID int64, userID int64, cb *tgbotapi.CallbackQuery) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Сессия не найдена. Используй /clone.")
		return
	}

	idStr := strings.TrimPrefix(cb.Data, "select_clone_chat:")
	maxChatID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		b.reply(chatID, "Ошибка выбора чата.")
		return
	}

	// Extract title from button
	chatName := fmt.Sprintf("Chat %d", maxChatID)
	if cb.Message != nil && cb.Message.ReplyMarkup != nil {
		for _, row := range cb.Message.ReplyMarkup.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData == cb.Data {
					chatName = btn.Text
					break
				}
			}
		}
	}

	sess.mu.Lock()
	sess.CloneChannelID = maxChatID
	sess.CloneChannelName = chatName
	sess.mu.Unlock()

	b.showCloneConfirm(chatID, sess)
}

// --- Step 7: Confirmation ---

func (b *Bot) showCloneConfirm(chatID int64, sess *Session) {
	sess.mu.Lock()
	source := sess.SourceChannel
	destType := sess.DestType
	cloneName := sess.CloneChannelName
	cloneID := sess.CloneChannelID
	sess.State = StateAwaitingCloneConfirm
	sess.mu.Unlock()

	var destLine string
	switch destType {
	case "max":
		destLine = fmt.Sprintf("📤 Назначение: %s (Max, ID: %d)", cloneName, cloneID)
	case "tg":
		destLine = fmt.Sprintf("📤 Назначение: новый канал в Telegram\n   Название: %s (clone)", source.Title)
	}

	text := fmt.Sprintf("Готово к клонированию:\n\n"+
		"📥 Источник: %s (TG)\n"+
		"%s\n\n"+
		"📎 Медиа (фото, видео, файлы) будут скачаны и перенесены.",
		source.Title, destLine)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Клонировать", "confirm_clone"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel_clone"),
		),
	)
	reply := tgbotapi.NewMessage(chatID, text)
	reply.ReplyMarkup = keyboard
	if _, err := b.api.Send(reply); err != nil {
		b.log.Error("send clone confirm failed", "error", err)
	}
}

// --- Step 8: Start clone migration ---

func (b *Bot) startCloneMigration(ctx context.Context, chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.TGClient == nil || sess.SourceChannel == nil {
		b.reply(chatID, "Сессия не найдена. Используй /clone.")
		return
	}

	if busy := b.checkBusy(chatID, userID); busy {
		return
	}

	sess.mu.Lock()
	sess.State = StateCloneMigrating
	sess.MigrationStart = time.Now()
	sess.PauseCh = make(chan struct{}, 1)
	sess.mu.Unlock()

	// Create temp dir for media
	mediaDir := filepath.Join(b.tempDir, fmt.Sprintf("clone_%d_%d", userID, time.Now().Unix()))
	os.MkdirAll(mediaDir, 0755)
	sess.mu.Lock()
	sess.TempMediaDir = mediaDir
	sess.mu.Unlock()

	b.replyWithKeyboard(chatID, "🚀 Запускаю клонирование...", keyboardMigrating())
	progressMsg := b.sendProgressMessage(chatID)

	// Log migration to DB
	sess.mu.Lock()
	source := sess.SourceChannel
	destType := sess.DestType
	cloneID := sess.CloneChannelID
	cloneName := sess.CloneChannelName
	sess.mu.Unlock()

	dbMigration := storage.Migration{
		UserID:        userID,
		MaxChatID:     cloneID,
		MaxChatName:   cloneName,
		SourceType:    "clone",
		SourceChannel: source.Title,
		DestType:      destType,
	}
	migID, _ := b.storage.StartMigration(ctx, dbMigration)
	sess.mu.Lock()
	sess.MigrationDBID = migID
	sess.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in clone migration", "recover", r, "user", userID)
				b.notifyAdmins(fmt.Sprintf("PANIC in clone migration (user %d): %v", userID, r))
			}
			os.RemoveAll(mediaDir)
			b.cleanupCloneSession(sess)
			b.replyWithKeyboard(chatID, "Клонирование завершено.", keyboardMain())
			b.notifyWaiting()
		}()

		migCtx, migCancel := context.WithTimeout(context.Background(), 4*time.Hour)
		defer migCancel()

		sess.mu.Lock()
		sess.Cancel = migCancel
		sess.mu.Unlock()

		// Step 1: Read channel history
		b.editMessage(chatID, progressMsg, "📥 Читаю историю канала...")
		results, err := sess.TGClient.ReadHistory(migCtx, source, mediaDir)
		if err != nil {
			b.log.Error("read history failed", "error", err, "channel", source.Title)
			b.editMessage(chatID, progressMsg, fmt.Sprintf("❌ Ошибка чтения канала: %v", err))
			b.storage.FinishMigration(ctx, migID, "failed", 0, err.Error())
			return
		}

		// Step 2: Convert to models.Message
		b.editMessage(chatID, progressMsg, fmt.Sprintf("🔄 Конвертирую %d сообщений...", len(results)))
		messages := make([]models.Message, 0, len(results))
		for _, r := range results {
			msg := tgclient.ConvertMessage(r.Raw, r.UserMap, r.MediaPath)
			messages = append(messages, msg)
		}

		if len(messages) == 0 {
			b.editMessage(chatID, progressMsg, "Канал пуст — нечего клонировать.")
			b.storage.FinishMigration(ctx, migID, "completed", 0, "")
			return
		}

		// Step 3: Create sender based on destination
		var sender migrator.Sender
		switch destType {
		case "max":
			s, err := maxbot.NewSender(b.maxToken, b.rps)
			if err != nil {
				b.editMessage(chatID, progressMsg, fmt.Sprintf("❌ Ошибка создания Max sender: %v", err))
				b.storage.FinishMigration(ctx, migID, "failed", 0, err.Error())
				return
			}
			sender = s
		case "tg":
			if sess.TGClient == nil || sess.TGClient.API() == nil {
				b.editMessage(chatID, progressMsg, "❌ MTProto клиент не подключён.")
				b.storage.FinishMigration(ctx, migID, "failed", 0, "mtproto client not connected")
				return
			}
			// Create new TG channel
			b.editMessage(chatID, progressMsg, "📢 Создаю канал в Telegram...")
			title := source.Title + " (clone)"
			newChID, newAccessHash, createErr := tgsender.CreateChannel(migCtx, sess.TGClient.API(), title, "Cloned by tg2max bot")
			if createErr != nil {
				b.editMessage(chatID, progressMsg, fmt.Sprintf("❌ Ошибка создания канала: %v", createErr))
				b.storage.FinishMigration(ctx, migID, "failed", 0, createErr.Error())
				return
			}
			sess.mu.Lock()
			sess.CloneChannelID = newChID
			sess.CloneChannelName = title
			cloneID = newChID
			cloneName = title
			sess.mu.Unlock()

			sender = tgsender.New(sess.TGClient.API(), newChID, newAccessHash, b.rps, b.log)
		}

		// Step 4: Set up cursor for resume support
		cursorFile := filepath.Join(mediaDir, "cursor.json")
		sess.mu.Lock()
		sess.CursorFile = cursorFile
		sess.CursorName = source.Title
		sess.mu.Unlock()

		// Step 5: Run migration using MigrateMessages (direct message passing)
		conv := converter.New()
		mig := migrator.New(sender, conv, cursorFile, b.log)
		mig.SetPauseCh(sess.PauseCh)

		b.editMessage(chatID, progressMsg,
			fmt.Sprintf("🚀 Клонирование: 0/%d сообщений...", len(messages)))

		// Start progress ticker
		go b.cloneProgressTicker(migCtx, chatID, progressMsg, sess, len(messages))

		stats, err := mig.MigrateMessages(migCtx, cloneID, source.Title, messages, "", 0)

		status := "completed"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
			if migCtx.Err() != nil {
				status = "cancelled"
				errMsg = "cancelled by user"
			}
		}

		b.storage.FinishMigration(ctx, migID, status, stats.Sent, errMsg)

		// Final status message
		duration := stats.Duration.Round(time.Second)
		finalText := fmt.Sprintf("✅ Клонирование завершено!\n\n"+
			"📢 %s → %s\n"+
			"📨 Отправлено: %d сообщений\n"+
			"⏱ Время: %s",
			source.Title, cloneName, stats.Sent, duration)
		if stats.MediaErrors > 0 {
			finalText += fmt.Sprintf("\n⚠️ Ошибок медиа: %d", stats.MediaErrors)
		}
		if err != nil && status != "cancelled" {
			finalText = fmt.Sprintf("❌ Клонирование завершилось с ошибкой:\n%v\n\n"+
				"📨 Отправлено до ошибки: %d", err, stats.Sent)
		}
		b.editMessage(chatID, progressMsg, finalText)
	}()
}

// cloneProgressTicker updates the progress message periodically.
func (b *Bot) cloneProgressTicker(ctx context.Context, chatID int64, msgID int, sess *Session, total int) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sess.mu.Lock()
			cursorFile := sess.CursorFile
			cursorName := sess.CursorName
			start := sess.MigrationStart
			paused := sess.State == StateClonePaused
			sess.mu.Unlock()

			sent, totalFromCursor := readCursorProgress(cursorFile, cursorName)
			if totalFromCursor > 0 {
				total = totalFromCursor
			}

			pct := 0
			if total > 0 {
				pct = sent * 100 / total
			}

			bar := progressBar(pct)
			text := fmt.Sprintf("🚀 Клонирование:\n%s %d%% (%d/%d)", bar, pct, sent, total)

			elapsed := time.Since(start)
			if sent > 0 && elapsed.Seconds() > 0 {
				speed := float64(sent) / elapsed.Seconds()
				remaining := time.Duration(float64(total-sent) / speed * float64(time.Second))
				text += fmt.Sprintf("\n⏱ ~%s осталось", formatDuration(remaining))
			}
			if paused {
				text += "\n⏸ На паузе"
			}

			b.editMessage(chatID, msgID, text)
		}
	}
}

// --- Cancel / Cleanup ---

func (b *Bot) cancelClone(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Нет активной сессии.")
		return
	}

	sess.mu.Lock()
	state := sess.State
	if state == StateCloneMigrating || state == StateClonePaused {
		if sess.Cancel != nil {
			sess.Cancel()
		}
		if state == StateClonePaused && sess.PauseCh != nil {
			select {
			case sess.PauseCh <- struct{}{}:
			default:
			}
		}
		sess.mu.Unlock()
		b.replyWithKeyboard(chatID, "⏹ Клонирование отменено.", keyboardMain())
		return
	}
	sess.mu.Unlock()

	b.cleanupCloneSession(sess)
	b.replyWithKeyboard(chatID, "Отменено.", keyboardMain())
}

func (b *Bot) cleanupCloneSession(sess *Session) {
	sess.mu.Lock()
	if sess.TGRunCancel != nil {
		sess.TGRunCancel()
		sess.TGRunCancel = nil
	}
	sess.TGClient = nil
	sess.TGAuth = nil
	sess.SourceChannel = nil
	sess.DestType = ""
	sess.CloneChannelID = 0
	sess.CloneChannelName = ""
	if sess.TempMediaDir != "" {
		os.RemoveAll(sess.TempMediaDir)
		sess.TempMediaDir = ""
	}
	sess.State = StateIdle
	sess.mu.Unlock()
}

// --- Helpers ---

func (b *Bot) sendProgressMessage(chatID int64) int {
	msg := tgbotapi.NewMessage(chatID, "⏳ Подготовка...")
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0
	}
	return sent.MessageID
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%d ч %d мин", h, m)
	}
	if m < 1 {
		return "~1 мин"
	}
	return fmt.Sprintf("%d мин", m)
}

func formatMembersCount(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM подписчиков", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK подписчиков", float64(n)/1000)
	}
	return fmt.Sprintf("%d подписчиков", n)
}
