package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	maxbotapi "github.com/max-messenger/max-bot-api-client-go"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/export"
	"github.com/arkosh/tg2max/internal/maxbot"
	"github.com/arkosh/tg2max/internal/migrator"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

type Bot struct {
	api      *tgbotapi.BotAPI
	sessions *SessionStore
	maxToken string
	rps      int
	tempDir  string
	log      *slog.Logger
}

type Config struct {
	TelegramToken string
	MaxToken      string
	RateLimitRPS  int
	TempDir       string
}

func New(cfg Config, log *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}

	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if err := os.MkdirAll(cfg.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	if cfg.RateLimitRPS <= 0 {
		cfg.RateLimitRPS = 25
	}

	return &Bot{
		api:      api,
		sessions: NewSessionStore(),
		maxToken: cfg.MaxToken,
		rps:      cfg.RateLimitRPS,
		tempDir:  cfg.TempDir,
		log:      log,
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	b.log.Info("bot started", "username", b.api.Self.UserName)

	// Register bot commands in Telegram menu
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начать работу"},
		tgbotapi.BotCommand{Command: "help", Description: "Справка"},
		tgbotapi.BotCommand{Command: "status", Description: "Текущий статус"},
		tgbotapi.BotCommand{Command: "cancel", Description: "Отменить миграцию"},
	)
	b.api.Request(commands)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case update := <-updates:
			if update.CallbackQuery != nil {
				b.handleCallback(ctx, update.CallbackQuery)
				continue
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}

// --- Keyboards ---

func keyboardMain() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📋 Справка"),
			tgbotapi.NewKeyboardButton("📊 Статус"),
		),
	)
}

func keyboardAwaitingConfirm() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("✅ Подтвердить перенос"),
			tgbotapi.NewKeyboardButton("👁 Предпросмотр"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("❌ Отмена"),
		),
	)
}

func keyboardMigrating() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("❌ Отменить миграцию"),
			tgbotapi.NewKeyboardButton("📊 Статус"),
		),
	)
}

// --- Message routing ---

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	b.log.Debug("incoming message", "from", msg.From.ID, "text", msg.Text, "command", msg.Command())

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
		case "cancel":
			b.handleCancel(msg)
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
	}

	// ZIP upload
	if msg.Document != nil {
		b.handleDocument(msg)
		return
	}

	// If awaiting chat ID, treat text as chat ID input
	sess := b.sessions.Get(msg.From.ID)
	if sess != nil {
		sess.mu.Lock()
		state := sess.State
		sess.mu.Unlock()
		if state == StateAwaitingChatID && msg.Text != "" {
			b.parseChatID(msg, sess)
			return
		}
	}
}

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	b.api.Request(tgbotapi.NewCallback(cb.ID, ""))

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
		b.handleSelectChat(chatID, userID, cb.Data)
	}
}

func (b *Bot) handleSelectChat(tgChatID int64, userID int64, data string) {
	idStr := strings.TrimPrefix(data, "select_chat:")
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

	sess.mu.Lock()
	sess.MaxChatID = maxChatID
	sess.mu.Unlock()

	b.showPreviewAndConfirm(tgChatID, userID)
}

// --- Step 0: Start ---

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	text := "Привет! Я перенесу историю чата из Telegram в Max.\n\n" +
		"Отправь мне ZIP-экспорт из Telegram Desktop:\n" +
		"Чат → ... → Export Chat History → JSON + Media\n\n" +
		"Заархивируй папку в ZIP и отправь сюда."

	b.replyWithKeyboard(msg.Chat.ID, text, keyboardMain())
}

func (b *Bot) handleHelp(msg *tgbotapi.Message) {
	text := `Как использовать:

1. Экспортируй чат в Telegram Desktop
   (... → Export Chat History → JSON)
2. Заархивируй папку в ZIP
3. Отправь ZIP сюда
4. Укажи Max chat ID
5. Проверь предпросмотр и подтверди

Команды:
/setchat <id> — задать Max chat ID вручную
/status — текущий статус
/cancel — отменить миграцию`
	b.reply(msg.Chat.ID, text)
}

// --- Step 1: Upload ZIP ---

func (b *Bot) handleDocument(msg *tgbotapi.Message) {
	doc := msg.Document
	if doc.MimeType != "application/zip" && !strings.HasSuffix(doc.FileName, ".zip") {
		b.reply(msg.Chat.ID, "Нужен ZIP-архив с экспортом Telegram Desktop.")
		return
	}

	sess := b.sessions.GetOrCreate(msg.From.ID)
	sess.mu.Lock()
	if sess.State == StateMigrating {
		sess.mu.Unlock()
		b.reply(msg.Chat.ID, "Миграция уже идёт. Нажми «Отменить миграцию» или /cancel.")
		return
	}
	sess.mu.Unlock()

	b.reply(msg.Chat.ID, "⏳ Загружаю и распаковываю...")

	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: doc.FileID})
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Ошибка: %s", err))
		return
	}

	userDir := filepath.Join(b.tempDir, fmt.Sprintf("user_%d", msg.From.ID))
	os.RemoveAll(userDir)
	os.MkdirAll(userDir, 0755)

	zipPath := filepath.Join(userDir, "export.zip")
	if err := downloadFile(file.Link(b.api.Token), zipPath); err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Ошибка скачивания: %s", err))
		return
	}

	extractDir := filepath.Join(userDir, "extracted")
	resultJSON, err := export.Unzip(zipPath, extractDir)
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Ошибка распаковки: %s\n\nУбедись что в архиве есть result.json", err))
		return
	}
	os.Remove(zipPath)

	info, err := export.Analyze(resultJSON)
	if err != nil {
		b.reply(msg.Chat.ID, fmt.Sprintf("Ошибка анализа: %s", err))
		return
	}

	sess.mu.Lock()
	sess.ExportPath = resultJSON
	sess.ExportDir = extractDir
	sess.State = StateAwaitingChatID
	sess.mu.Unlock()

	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ Экспорт загружен!\n\n")
	fmt.Fprintf(&sb, "📨 Сообщений: %d\n", info.Messages)
	if info.Photos > 0 {
		fmt.Fprintf(&sb, "🖼 Фото: %d\n", info.Photos)
	}
	if info.Videos > 0 {
		fmt.Fprintf(&sb, "🎬 Видео: %d\n", info.Videos)
	}
	if info.Documents > 0 {
		fmt.Fprintf(&sb, "📄 Документов: %d\n", info.Documents)
	}

	// Try to fetch Max chats for inline buttons
	chats := b.fetchMaxChats()
	if len(chats) > 0 {
		sb.WriteString("\nВыбери чат в Max для переноса:")
		reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
		var rows [][]tgbotapi.InlineKeyboardButton
		for _, c := range chats {
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
		reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		b.api.Send(reply)
	} else {
		sb.WriteString("\nОтправь Max chat ID (число).\n\n")
		sb.WriteString("Добавь Max-бота в нужный чат, затем я смогу показать список.")
		b.reply(msg.Chat.ID, sb.String())
	}
}

// --- Step 2: Set chat ID ---

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
	if err != nil {
		b.reply(chatID, "Неверный ID. Отправь число — ID чата в Max.")
		return
	}

	sess.mu.Lock()
	sess.MaxChatID = id
	sess.mu.Unlock()

	b.showPreviewAndConfirm(chatID, sess.UserID)
}

// --- Step 3: Preview + Confirm ---

func (b *Bot) showPreviewAndConfirm(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(chatID, "Сначала отправь ZIP-экспорт.")
		return
	}

	reader := telegram.NewReader(sess.ExportPath)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		b.reply(chatID, fmt.Sprintf("Ошибка чтения: %s", err))
		return
	}

	conv := converter.New()
	total := len(result.Messages)
	limit := min(3, total)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Max chat ID: %d\n", sess.MaxChatID)
	fmt.Fprintf(&sb, "Всего сообщений: %d\n\n", total)
	sb.WriteString("Последние сообщения (так будут в Max):\n\n")

	start := total - limit
	for i := start; i < total; i++ {
		sb.WriteString(conv.FormatForMax(result.Messages[i]))
		sb.WriteString("\n---\n")
	}
	sb.WriteString("\nНажми «Подтвердить перенос» для начала.")

	b.replyWithKeyboard(chatID, sb.String(), keyboardAwaitingConfirm())

	sess.mu.Lock()
	sess.State = StateAwaitingConfirm
	sess.mu.Unlock()
}

// --- Step 4: Migration ---

func (b *Bot) startMigration(ctx context.Context, chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(chatID, "Сначала отправь ZIP-экспорт.")
		return
	}

	sess.mu.Lock()
	if sess.MaxChatID == 0 {
		sess.mu.Unlock()
		b.reply(chatID, "Max chat ID не задан. /setchat <id>")
		return
	}
	if sess.State == StateMigrating {
		sess.mu.Unlock()
		b.reply(chatID, "Миграция уже идёт.")
		return
	}
	sess.State = StateMigrating
	migCtx, cancel := context.WithCancel(ctx)
	sess.Cancel = cancel
	exportPath := sess.ExportPath
	maxChatID := sess.MaxChatID
	sess.mu.Unlock()

	// Show migrating keyboard
	b.replyWithKeyboard(chatID, "🚀 Начинаю перенос...", keyboardMigrating())

	// Send progress message (will be edited)
	progressMsg := tgbotapi.NewMessage(chatID, "⏳ Подготовка...")
	sent, err := b.api.Send(progressMsg)
	if err != nil {
		b.log.Error("failed to send progress message", "error", err)
		return
	}
	progressMsgID := sent.MessageID
	cursorFile := filepath.Join(filepath.Dir(exportPath), "cursor.json")

	go func() {
		defer func() {
			sess.mu.Lock()
			sess.State = StateIdle
			sess.Cancel = nil
			sess.mu.Unlock()
			// Reset keyboard
			b.replyWithKeyboard(chatID, "Готово.", keyboardMain())
		}()

		sender, senderErr := maxbot.NewSender(b.maxToken, b.rps)
		if senderErr != nil {
			b.editMessage(chatID, progressMsgID, fmt.Sprintf("❌ Ошибка Max API: %s", senderErr))
			return
		}

		conv := converter.New()
		mig := migrator.New(sender, conv, cursorFile, b.log)

		mapping := []models.ChatMapping{{
			Name:         "bot-migration",
			TGExportPath: exportPath,
			MaxChatID:    maxChatID,
		}}

		progressDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-progressDone:
					return
				case <-migCtx.Done():
					return
				case <-ticker.C:
					sentCount, total := readCursorProgress(cursorFile, "bot-migration")
					if total <= 0 {
						continue
					}
					pct := sentCount * 100 / total
					bar := progressBar(pct)
					text := fmt.Sprintf("Перенос: %s %d%%\n(%d / %d)", bar, pct, sentCount, total)
					b.editMessage(chatID, progressMsgID, text)
				}
			}
		}()

		stats, migErr := mig.MigrateAll(migCtx, mapping)
		close(progressDone)

		if migErr != nil {
			b.editMessage(chatID, progressMsgID,
				fmt.Sprintf("⚠️ Ошибка: %s\n\nОтправлено: %d\nПрогресс сохранён — повтори /start", migErr, stats.Sent))
			return
		}

		b.editMessage(chatID, progressMsgID,
			fmt.Sprintf("✅ Перенос завершён!\n\n"+
				"Отправлено: %d\n"+
				"Пропущено: %d\n"+
				"Ошибки медиа: %d\n"+
				"Время: %s",
				stats.Sent, stats.Skipped, stats.MediaErrors, stats.Duration.Round(time.Second)))
	}()
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
	hasExport := sess.ExportPath != ""
	sess.mu.Unlock()

	states := map[State]string{
		StateIdle:            "⏸ Ожидание",
		StateAwaitingExport:  "📦 Жду экспорт",
		StateAwaitingChatID:  "🔢 Жду Max chat ID",
		StateAwaitingConfirm: "👁 Жду подтверждение",
		StateMigrating:       "🚀 Миграция идёт",
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Статус: %s\n", states[state])
	fmt.Fprintf(&sb, "Max chat ID: %d\n", maxID)
	if hasExport {
		sb.WriteString("Экспорт: ✅ загружен")
	} else {
		sb.WriteString("Экспорт: ❌ нет")
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
	if sess.State != StateMigrating || sess.Cancel == nil {
		sess.mu.Unlock()
		b.reply(chatID, "Нет активной миграции.")
		return
	}
	sess.Cancel()
	sess.mu.Unlock()

	b.replyWithKeyboard(chatID, "⏹ Миграция отменена. Прогресс сохранён.", keyboardMain())
}

// --- Max API ---

type maxChat struct {
	id    int64
	title string
}

func (b *Bot) fetchMaxChats() []maxChat {
	if b.maxToken == "" || b.maxToken == "placeholder" {
		return nil
	}

	api, err := maxbotapi.New(b.maxToken)
	if err != nil {
		b.log.Debug("failed to create max api for chat list", "error", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := api.Chats.GetChats(ctx, 50, 0)
	if err != nil {
		b.log.Debug("failed to fetch max chats", "error", err)
		return nil
	}

	var chats []maxChat
	for _, c := range list.Chats {
		if c.Status != "active" {
			continue
		}
		title := c.Title
		if c.Type == "dialog" {
			title = "Диалог"
		}
		chats = append(chats, maxChat{id: c.ChatId, title: title})
	}
	return chats
}

// --- Helpers ---

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

func progressBar(pct int) string {
	filled := max(0, min(10, pct/10))
	return strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 10-filled)
}

func readCursorProgress(cursorFile string, chatName string) (int, int) {
	data, err := os.ReadFile(cursorFile)
	if err != nil {
		return 0, 0
	}
	var cursors []struct {
		ChatName      string `json:"chat_name"`
		SentMessages  int    `json:"sent_messages"`
		TotalMessages int    `json:"total_messages"`
	}
	if err := json.Unmarshal(data, &cursors); err != nil {
		return 0, 0
	}
	for _, c := range cursors {
		if c.ChatName == chatName {
			return c.SentMessages, c.TotalMessages
		}
	}
	return 0, 0
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
