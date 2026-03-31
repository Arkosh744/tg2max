package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- Message routing ---

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg.From == nil {
		return
	}
	if !b.isAuthorized(msg.From.ID) {
		b.reply(msg.Chat.ID, "Доступ запрещён.")
		return
	}
	b.log.Debug("incoming message", "from", msg.From.ID, "text", msg.Text, "command", msg.Command())

	// Track user in persistent storage (fire-and-forget)
	b.trackUser(ctx, msg.From)

	// Handle commands
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			b.handleStart(msg)
		case "help":
			b.handleHelp(msg)
		case "setchat":
			b.handleSetChat(msg)
		case "status":
			b.handleStatus(msg)
		case "history":
			b.handleHistory(ctx, msg)
		case "stats":
			b.handleStats(ctx, msg)
		case "cancel":
			b.handleCancel(msg)
		case "reset":
			b.handleReset(msg)
		default:
			b.replyWithKeyboard(msg.Chat.ID, "Неизвестная команда. /help", keyboardMain())
		}
		return
	}

	// Handle button text from reply keyboard
	switch msg.Text {
	case "📋 Справка":
		b.handleHelp(msg)
		return
	case "📊 Статус":
		b.handleStatus(msg)
		return
	case "👁 Предпросмотр":
		b.showPreviewAndConfirm(msg.Chat.ID, msg.From.ID)
		return
	case "✅ Подтвердить перенос":
		b.startMigration(ctx, msg.Chat.ID, msg.From.ID)
		return
	case "❌ Отмена", "❌ Отменить миграцию":
		b.cancelMigration(msg.Chat.ID, msg.From.ID)
		return
	case "⏸ Пауза":
		b.pauseMigration(msg.Chat.ID, msg.From.ID)
		return
	case "▶️ Продолжить":
		b.resumeMigration(msg.Chat.ID, msg.From.ID)
		return
	case "🔄 Сменить чат":
		b.handleChangeChat(msg)
		return
	}

	// ZIP upload
	if msg.Document != nil {
		b.handleDocument(msg)
		return
	}

	// State-based text routing
	sess := b.sessions.Get(msg.From.ID)
	if sess != nil && msg.Text != "" {
		sess.mu.Lock()
		state := sess.State
		sess.mu.Unlock()

		switch state {
		case StateAwaitingChatSearch:
			b.searchMaxChats(msg)
			return
		case StateAwaitingChatID:
			b.parseChatID(msg, sess)
			return
		case StateAwaitingFilter:
			b.reply(msg.Chat.ID, "Выбери фильтр из кнопок выше.")
			return
		case StateAwaitingConfirm:
			b.reply(msg.Chat.ID, "Нажми «Подтвердить перенос» или «Предпросмотр».")
			return
		}
	}
}

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	b.api.Request(tgbotapi.NewCallback(cb.ID, ""))

	if cb.Message == nil || cb.From == nil {
		return
	}
	if !b.isAuthorized(cb.From.ID) {
		return
	}

	chatID := cb.Message.Chat.ID
	userID := cb.From.ID

	switch {
	case cb.Data == "help":
		b.handleHelp(cb.Message)
	case cb.Data == "preview":
		b.showPreviewAndConfirm(chatID, userID)
	case cb.Data == "confirm_migrate":
		b.startMigration(ctx, chatID, userID)
	case cb.Data == "cancel":
		b.cancelMigration(chatID, userID)
	case cb.Data == "status":
		b.showStatus(chatID, userID)
	case strings.HasPrefix(cb.Data, "select_chat:"):
		b.handleSelectChat(chatID, userID, cb)
	case strings.HasPrefix(cb.Data, "filter:"):
		b.handleFilterCallback(chatID, userID, cb.Data)
	}
}

// --- Step 0: Start ---

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	// Don't replace keyboard if migration is running
	sess := b.sessions.Get(msg.From.ID)
	if sess != nil {
		sess.mu.Lock()
		state := sess.State
		sess.mu.Unlock()
		if state == StateMigrating {
			b.replyWithKeyboard(msg.Chat.ID, "Миграция идёт. Используй кнопки ниже.", keyboardMigrating())
			return
		}
		if state == StatePaused {
			b.replyWithKeyboard(msg.Chat.ID, "Миграция на паузе. Используй кнопки ниже.", keyboardPaused())
			return
		}
	}

	sizeLimit := "512 МБ"
	if b.tgAPIEndpoint != "" {
		sizeLimit = "2 ГБ"
	}
	text := "Привет! Я перенесу историю чата из Telegram в Max.\n\n" +
		"Шаг 1: Экспортируй чат в Telegram Desktop\n" +
		"  Чат → ⋯ → Export Chat History → Format: JSON\n" +
		"  (ВАЖНО: выбери формат JSON, не HTML)\n" +
		"  Включи нужные медиа (фото, видео, файлы)\n\n" +
		"Шаг 2: Заархивируй полученную папку в ZIP\n\n" +
		"Шаг 3: Отправь ZIP сюда\n\n" +
		"Максимальный размер: " + sizeLimit

	b.replyWithKeyboard(msg.Chat.ID, text, keyboardMain())
}

func (b *Bot) handleHelp(msg *tgbotapi.Message) {
	helpLimit := "512 МБ"
	if b.tgAPIEndpoint != "" {
		helpLimit = "2 ГБ"
	}
	text := fmt.Sprintf(`Как использовать:

1. Экспортируй чат в Telegram Desktop
   Чат → ⋯ → Export Chat History → JSON + медиа
2. Заархивируй папку в ZIP (макс %s)
3. Отправь ZIP сюда`, helpLimit) + `
4. Введи название чата в Max для поиска
   (или числовой ID чата)
5. Проверь предпросмотр и подтверди

Если миграция прервалась — отправь тот же ZIP повторно, прогресс сохранён.

Команды:
/setchat <id> — задать Max chat ID вручную
/status — текущий статус
/cancel — отменить/сбросить
/reset — полный сброс сессии`
	b.reply(msg.Chat.ID, text)
}

// --- Status / Cancel ---

func (b *Bot) handleStatus(msg *tgbotapi.Message) {
	b.showStatus(msg.Chat.ID, msg.From.ID)
}

func (b *Bot) showStatus(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Нет активной сессии. Отправь ZIP-экспорт.")
		return
	}

	sess.mu.Lock()
	state := sess.State
	maxID := sess.MaxChatID
	chatName := sess.MaxChatName
	hasExport := sess.ExportPath != ""
	filterType := sess.FilterType
	filterMonths := sess.FilterMonths
	sess.mu.Unlock()

	states := map[State]string{
		StateIdle:               "⏸ Ожидание",
		StateAwaitingChatSearch: "🔍 Жду название чата",
		StateAwaitingChatID:     "🔢 Жду Max chat ID",
		StateAwaitingFilter:     "🔽 Жду выбор фильтра",
		StateAwaitingConfirm:    "👁 Жду подтверждение",
		StateMigrating:          "🚀 Миграция идёт",
		StatePaused:             "⏸ Миграция на паузе",
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Статус: %s\n", states[state])
	if hasExport {
		sb.WriteString("Экспорт: ✅ загружен\n")
	} else {
		sb.WriteString("Экспорт: ❌ нет\n")
	}
	if maxID != 0 {
		if chatName != "" {
			fmt.Fprintf(&sb, "Чат Max: %s (ID: %d)\n", chatName, maxID)
		} else {
			fmt.Fprintf(&sb, "Чат Max ID: %d\n", maxID)
		}
	}
	switch {
	case filterType == "text":
		sb.WriteString("Фильтр: только текст")
	case filterType == "media":
		sb.WriteString("Фильтр: только медиа")
	case filterMonths > 0:
		fmt.Fprintf(&sb, "Фильтр: за последние %d мес.", filterMonths)
	}
	b.reply(chatID, sb.String())
}

func (b *Bot) handleCancel(msg *tgbotapi.Message) {
	b.cancelMigration(msg.Chat.ID, msg.From.ID)
}

func (b *Bot) cancelMigration(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Нет активной сессии.")
		return
	}

	sess.mu.Lock()
	state := sess.State

	if (state == StateMigrating || state == StatePaused) && sess.Cancel != nil {
		pauseCh := sess.PauseCh
		sess.Cancel()
		// If paused, unblock the migration goroutine so it can observe context cancellation
		if state == StatePaused && pauseCh != nil {
			select {
			case pauseCh <- struct{}{}:
			default:
			}
		}
		sess.mu.Unlock()
		b.replyWithKeyboard(chatID, "⏹ Миграция отменена. Прогресс сохранён.", keyboardMain())
		return
	}

	// Cancel from any non-idle state resets to idle
	if state != StateIdle {
		sess.State = StateIdle
		sess.MaxChatID = 0
		sess.MaxChatName = ""
		sess.mu.Unlock()
		b.replyWithKeyboard(chatID, "Отменено. Отправь ZIP чтобы начать заново.", keyboardMain())
		return
	}

	sess.mu.Unlock()
	b.reply(chatID, "Нечего отменять.")
}

func (b *Bot) handleReset(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil {
		b.replyWithKeyboard(msg.Chat.ID, "Нет активной сессии.", keyboardMain())
		return
	}

	sess.mu.Lock()
	state := sess.State
	if (state == StateMigrating || state == StatePaused) && sess.Cancel != nil {
		pauseCh := sess.PauseCh
		sess.Cancel()
		if state == StatePaused && pauseCh != nil {
			select {
			case pauseCh <- struct{}{}:
			default:
			}
		}
	}
	sess.mu.Unlock()

	b.sessions.Delete(msg.From.ID)
	b.replyWithKeyboard(msg.Chat.ID, "Сессия сброшена. Отправь ZIP чтобы начать.", keyboardMain())
}

func (b *Bot) handleChangeChat(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(msg.Chat.ID, "Сначала отправь ZIP-экспорт.")
		return
	}

	sess.mu.Lock()
	sess.MaxChatID = 0
	sess.MaxChatName = ""
	sess.State = StateAwaitingChatSearch
	sess.mu.Unlock()

	b.reply(msg.Chat.ID, "Введи название чата в Max для поиска.\nИли отправь числовой ID чата напрямую.")
}

// --- Step 2: Search / Set chat ID ---

func (b *Bot) handleSetChat(msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	if args == "" {
		b.reply(msg.Chat.ID, "Укажи ID чата: /setchat 123456789")
		return
	}
	sess := b.sessions.GetOrCreate(msg.From.ID)
	b.parseChatIDFromString(msg.Chat.ID, args, sess)
}

func (b *Bot) parseChatID(msg *tgbotapi.Message, sess *Session) {
	b.parseChatIDFromString(msg.Chat.ID, msg.Text, sess)
}

func (b *Bot) parseChatIDFromString(chatID int64, input string, sess *Session) {
	id, err := strconv.ParseInt(strings.TrimSpace(input), 10, 64)
	if err != nil || id <= 0 {
		b.reply(chatID, "Неверный ID. Отправь положительное число — ID чата в Max.")
		return
	}

	chatName := fmt.Sprintf("Chat %d", id)
	sess.mu.Lock()
	sess.MaxChatID = id
	sess.MaxChatName = chatName
	sess.State = StateAwaitingFilter
	sess.mu.Unlock()

	b.showFilterKeyboard(chatID, chatName, id)
}

func (b *Bot) handleSelectChat(tgChatID int64, userID int64, cb *tgbotapi.CallbackQuery) {
	idStr := strings.TrimPrefix(cb.Data, "select_chat:")
	maxChatID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		b.reply(tgChatID, "Ошибка выбора чата.")
		return
	}

	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(tgChatID, "Сначала отправь ZIP-экспорт.")
		return
	}

	// Extract the chat name from the pressed inline button text.
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
	sess.MaxChatID = maxChatID
	sess.MaxChatName = chatName
	sess.State = StateAwaitingFilter
	sess.mu.Unlock()

	b.showFilterKeyboard(tgChatID, chatName, maxChatID)
}

// showFilterKeyboard sends the inline keyboard for content type selection (step 1 of 2).
func (b *Bot) showFilterKeyboard(chatID int64, chatName string, maxChatID int64) {
	text := fmt.Sprintf("Выбран чат: %s (ID: %d)\n\nШаг 1/2: Какой контент переносить?", chatName, maxChatID)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 Всё", "filter:type:all"),
			tgbotapi.NewInlineKeyboardButtonData("📝 Только текст", "filter:type:text"),
			tgbotapi.NewInlineKeyboardButtonData("🖼 Только медиа", "filter:type:media"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		b.log.Error("send filter keyboard failed", "error", err)
	}
}

// showPeriodKeyboard sends the inline keyboard for period selection (step 2 of 2).
func (b *Bot) showPeriodKeyboard(chatID int64) {
	text := "Шаг 2/2: За какой период?"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("3 месяца", "filter:period:3m"),
			tgbotapi.NewInlineKeyboardButtonData("6 месяцев", "filter:period:6m"),
			tgbotapi.NewInlineKeyboardButtonData("За всё время", "filter:period:all"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		b.log.Error("send period keyboard failed", "error", err)
	}
}

// handleFilterCallback parses filter:type:* and filter:period:* callbacks.
// Two-step flow: type selection → period selection → preview.
func (b *Bot) handleFilterCallback(chatID int64, userID int64, data string) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Сначала отправь ZIP-экспорт.")
		return
	}

	token := strings.TrimPrefix(data, "filter:")

	switch {
	case strings.HasPrefix(token, "type:"):
		// Step 1: content type selected → show period keyboard
		typeVal := strings.TrimPrefix(token, "type:")
		sess.mu.Lock()
		switch typeVal {
		case "text":
			sess.FilterType = "text"
		case "media":
			sess.FilterType = "media"
		default:
			sess.FilterType = ""
		}
		sess.mu.Unlock()
		b.showPeriodKeyboard(chatID)

	case strings.HasPrefix(token, "period:"):
		// Step 2: period selected → move to confirm
		periodVal := strings.TrimPrefix(token, "period:")
		sess.mu.Lock()
		switch periodVal {
		case "3m":
			sess.FilterMonths = 3
		case "6m":
			sess.FilterMonths = 6
		default:
			sess.FilterMonths = 0
		}
		sess.State = StateAwaitingConfirm
		sess.mu.Unlock()
		b.showPreviewAndConfirm(chatID, userID)

	default:
		// Legacy single-step callbacks for backward compatibility
		sess.mu.Lock()
		sess.FilterType = ""
		sess.FilterMonths = 0
		sess.State = StateAwaitingConfirm
		sess.mu.Unlock()
		b.showPreviewAndConfirm(chatID, userID)
	}
}

// --- Admin: /stats ---

func (b *Bot) handleStats(ctx context.Context, msg *tgbotapi.Message) {
	if !b.isAuthorized(msg.From.ID) {
		b.reply(msg.Chat.ID, "Команда доступна только администраторам.")
		return
	}

	st, err := b.storage.GetStats(ctx)
	if err != nil {
		b.log.Error("get stats failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка получения статистики.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 Статистика бота\n\n")
	fmt.Fprintf(&sb, "👥 Пользователей: %d\n", st.TotalUsers)
	fmt.Fprintf(&sb, "📦 Миграций всего: %d\n", st.TotalMigrations)
	fmt.Fprintf(&sb, "  ✅ Завершено: %d\n", st.Completed)
	fmt.Fprintf(&sb, "  ❌ Ошибок: %d\n", st.Failed)
	fmt.Fprintf(&sb, "  ⏹ Отменено: %d\n", st.Cancelled)
	fmt.Fprintf(&sb, "📨 Сообщений отправлено: %d\n", st.TotalSent)
	if st.AvgDurationSec > 0 {
		avg := time.Duration(st.AvgDurationSec) * time.Second
		fmt.Fprintf(&sb, "⏱ Среднее время: %s\n", avg.Round(time.Second))
	}

	// Show active migration if any
	active := b.sessions.GetActiveMigration()
	if active != nil {
		active.mu.Lock()
		uid := active.UserID
		chatName := active.MaxChatName
		active.mu.Unlock()
		eta := b.estimateETA(active)
		fmt.Fprintf(&sb, "\n🚀 Активная миграция: user %d → %s", uid, chatName)
		if eta != "" {
			fmt.Fprintf(&sb, " (%s осталось)", eta)
		}
	}

	b.reply(msg.Chat.ID, sb.String())
}

// --- User: /history ---

func (b *Bot) handleHistory(ctx context.Context, msg *tgbotapi.Message) {
	entries, err := b.storage.GetUserHistory(ctx, msg.From.ID, 10)
	if err != nil {
		b.log.Error("get history failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка получения истории.")
		return
	}

	if len(entries) == 0 {
		b.reply(msg.Chat.ID, "История миграций пуста.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 История миграций\n\n")
	statusEmoji := map[string]string{
		"completed": "✅",
		"failed":    "❌",
		"cancelled": "⏹",
		"started":   "🚀",
	}
	for _, e := range entries {
		emoji := statusEmoji[e.Status]
		if emoji == "" {
			emoji = "❓"
		}
		dur := time.Duration(e.DurationSeconds) * time.Second
		fmt.Fprintf(&sb, "%s %s — %s\n", emoji, e.StartedAt.Format("02.01.2006 15:04"), e.MaxChatName)
		fmt.Fprintf(&sb, "   %d/%d сообщений", e.SentMessages, e.TotalMessages)
		if e.DurationSeconds > 0 {
			fmt.Fprintf(&sb, " · %s", dur.Round(time.Second))
		}
		sb.WriteString("\n")
	}

	b.reply(msg.Chat.ID, sb.String())
}
