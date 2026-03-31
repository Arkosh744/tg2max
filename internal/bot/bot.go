package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	maxbotapi "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/export"
	"github.com/arkosh/tg2max/internal/maxbot"
	"github.com/arkosh/tg2max/internal/migrator"
	"github.com/arkosh/tg2max/internal/storage"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

type Bot struct {
	api            *tgbotapi.BotAPI
	sessions       *SessionStore
	storage        storage.Storage
	maxToken       string
	rps            float64
	tempDir        string
	tgAPIEndpoint  string
	tgAPIFilesDir  string
	allowedUserIDs map[int64]struct{}
	log            *slog.Logger
}

type Config struct {
	TelegramToken  string
	MaxToken       string
	RateLimitRPS   float64
	TempDir        string
	TGAPIEndpoint  string  // Local Bot API server URL, e.g. "http://localhost:8081"
	TGAPIFilesDir  string  // Host path to local Bot API files volume
	AllowedUserIDs []int64 // if empty, open to everyone (NOT recommended for production)
	DBPath         string  // path to SQLite database; empty = no persistent storage
}

func New(cfg Config, log *slog.Logger) (*Bot, error) {
	var api *tgbotapi.BotAPI
	var err error
	if cfg.TGAPIEndpoint != "" {
		api, err = tgbotapi.NewBotAPIWithAPIEndpoint(cfg.TelegramToken, cfg.TGAPIEndpoint+"/bot%s/%s")
	} else {
		api, err = tgbotapi.NewBotAPI(cfg.TelegramToken)
	}
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
		cfg.RateLimitRPS = 1
	}

	allowed := make(map[int64]struct{}, len(cfg.AllowedUserIDs))
	for _, id := range cfg.AllowedUserIDs {
		allowed[id] = struct{}{}
	}

	var store storage.Storage
	if cfg.DBPath != "" {
		store, err = storage.NewSQLite(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
	} else {
		store = storage.Nop{}
	}

	return &Bot{
		api:            api,
		sessions:       NewSessionStore(),
		storage:        store,
		maxToken:       cfg.MaxToken,
		rps:            cfg.RateLimitRPS,
		tempDir:        cfg.TempDir,
		tgAPIEndpoint:  cfg.TGAPIEndpoint,
		tgAPIFilesDir:  cfg.TGAPIFilesDir,
		allowedUserIDs: allowed,
		log:            log,
	}, nil
}

// Close releases resources held by the bot (database connection, etc.).
func (b *Bot) Close() error {
	return b.storage.Close()
}

func (b *Bot) isAuthorized(userID int64) bool {
	if len(b.allowedUserIDs) == 0 {
		return true
	}
	_, ok := b.allowedUserIDs[userID]
	return ok
}

func (b *Bot) Run(ctx context.Context) error {
	b.log.Info("bot started", "username", b.api.Self.UserName)
	b.sessions.StartCleanup(ctx)

	// Register bot commands in Telegram menu
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "Начать работу"},
		tgbotapi.BotCommand{Command: "help", Description: "Справка"},
		tgbotapi.BotCommand{Command: "status", Description: "Текущий статус"},
		tgbotapi.BotCommand{Command: "history", Description: "История миграций"},
		tgbotapi.BotCommand{Command: "cancel", Description: "Отменить миграцию"},
		tgbotapi.BotCommand{Command: "reset", Description: "Сбросить сессию"},
		tgbotapi.BotCommand{Command: "stats", Description: "Статистика (админ)"},
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
			tgbotapi.NewKeyboardButton("🔄 Сменить чат"),
			tgbotapi.NewKeyboardButton("❌ Отмена"),
		),
	)
}

func keyboardMigrating() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⏸ Пауза"),
			tgbotapi.NewKeyboardButton("📊 Статус"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("❌ Отменить миграцию"),
		),
	)
}

func keyboardPaused() tgbotapi.ReplyKeyboardMarkup {
	return tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("▶️ Продолжить"),
			tgbotapi.NewKeyboardButton("📊 Статус"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("❌ Отменить миграцию"),
		),
	)
}

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

// showFilterKeyboard sends the inline keyboard asking the user which messages to migrate.
func (b *Bot) showFilterKeyboard(chatID int64, chatName string, maxChatID int64) {
	text := fmt.Sprintf("Выбран чат: %s (ID: %d)\n\nЧто переносить?", chatName, maxChatID)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Всё", "filter:all"),
			tgbotapi.NewInlineKeyboardButtonData("Только текст", "filter:text"),
			tgbotapi.NewInlineKeyboardButtonData("Только медиа", "filter:media"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("За 3 месяца", "filter:3m"),
			tgbotapi.NewInlineKeyboardButtonData("За 6 месяцев", "filter:6m"),
			tgbotapi.NewInlineKeyboardButtonData("За всё время", "filter:all"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleFilterCallback parses a filter:* callback, saves filter to session, moves to confirm.
func (b *Bot) handleFilterCallback(chatID int64, userID int64, data string) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		b.reply(chatID, "Сначала отправь ZIP-экспорт.")
		return
	}

	token := strings.TrimPrefix(data, "filter:")

	var filterType string
	var filterMonths int

	switch token {
	case "text":
		filterType = "text"
	case "media":
		filterType = "media"
	case "3m":
		filterMonths = 3
	case "6m":
		filterMonths = 6
	default: // "all" and any unknown value
		filterType = ""
		filterMonths = 0
	}

	sess.mu.Lock()
	sess.FilterType = filterType
	sess.FilterMonths = filterMonths
	sess.State = StateAwaitingConfirm
	sess.mu.Unlock()

	b.showPreviewAndConfirm(chatID, userID)
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

// --- Step 1: Upload ZIP ---

func (b *Bot) handleDocument(msg *tgbotapi.Message) {
	doc := msg.Document
	if doc.MimeType != "application/zip" && !strings.HasSuffix(doc.FileName, ".zip") {
		b.reply(msg.Chat.ID, "Нужен ZIP-архив с экспортом Telegram Desktop.")
		return
	}

	// Global busy lock — reject if another user is migrating
	if b.checkBusy(msg.Chat.ID, msg.From.ID) {
		return
	}

	sess := b.sessions.GetOrCreate(msg.From.ID)
	sess.mu.Lock()
	if sess.State == StateMigrating || sess.State == StatePaused {
		sess.mu.Unlock()
		b.reply(msg.Chat.ID, "Миграция уже идёт. Нажми «Отменить миграцию» или /cancel.")
		return
	}
	sess.mu.Unlock()

	maxZipSize := 512 * 1024 * 1024 // 512 MiB via standard Telegram API
	if b.tgAPIEndpoint != "" {
		maxZipSize = 2 * 1024 * 1024 * 1024 // 2 GiB via Local Bot API
	}
	if int64(doc.FileSize) > int64(maxZipSize) {
		limit := "512 МБ"
		if b.tgAPIEndpoint != "" {
			limit = "2 ГБ"
		}
		b.reply(msg.Chat.ID, fmt.Sprintf("Файл слишком большой (макс %s).", limit))
		return
	}

	b.reply(msg.Chat.ID, "⏳ Загружаю и распаковываю...")

	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: doc.FileID})
	if err != nil {
		b.log.Error("get file failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка получения файла. Попробуй ещё раз.")
		return
	}
	b.log.Info("file info", "filePath", file.FilePath, "fileID", doc.FileID, "fileSize", doc.FileSize)

	userDir := filepath.Join(b.tempDir, fmt.Sprintf("user_%d", msg.From.ID))

	// Preserve cursor.json across re-uploads so migration can resume.
	// cursor.json sits next to result.json inside the export subdirectory.
	cursorBackup := filepath.Join(b.tempDir, fmt.Sprintf("cursor_%d.json", msg.From.ID))
	if sess := b.sessions.Get(msg.From.ID); sess != nil && sess.ExportPath != "" {
		oldCursor := filepath.Join(filepath.Dir(sess.ExportPath), "cursor.json")
		if data, err := os.ReadFile(oldCursor); err == nil {
			if err := os.WriteFile(cursorBackup, data, 0600); err != nil {
				b.log.Warn("cursor backup failed", "error", err)
			}
		}
	}

	os.RemoveAll(userDir)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		b.log.Error("create user dir failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка создания рабочей директории.")
		return
	}

	zipPath := filepath.Join(userDir, "export.zip")
	b.log.Debug("downloading file", "filePath", file.FilePath)
	if err := b.resolveFile(file.FilePath, zipPath); err != nil {
		b.log.Error("file download failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка скачивания файла. Попробуй ещё раз.")
		return
	}

	extractDir := filepath.Join(userDir, "extracted")
	resultJSON, err := export.Unzip(zipPath, extractDir)
	if err != nil {
		b.log.Error("unzip failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка распаковки. Убедись что в архиве есть result.json.")
		return
	}
	os.Remove(zipPath)

	// Check if the uploaded ZIP contains the same export as the current session.
	// If hash matches and the export directory still exists, skip re-analysis and resume.
	newHash := hashFile(resultJSON)
	if newHash != "" {
		sess.mu.Lock()
		oldHash := sess.ExportHash
		oldExportDir := sess.ExportDir
		sess.mu.Unlock()

		if oldHash != "" && oldHash == newHash {
			if _, statErr := os.Stat(oldExportDir); statErr == nil {
				// Restore session state and show appropriate keyboard
				sess.mu.Lock()
				state := sess.State
				sess.mu.Unlock()
				switch state {
				case StateMigrating, StatePaused:
					b.reply(msg.Chat.ID, "Миграция уже идёт с этим экспортом.")
				case StateAwaitingConfirm:
					b.replyWithKeyboard(msg.Chat.ID, "Экспорт уже загружен. Нажми «Подтвердить перенос».", keyboardAwaitingConfirm())
				default:
					b.replyWithKeyboard(msg.Chat.ID, "Экспорт уже загружен.\nВведи название чата в Max для поиска.", keyboardMain())
				}
				os.RemoveAll(userDir) // clean up duplicate extraction
				return
			}
		}
	}

	// Restore cursor.json next to result.json so migration resumes
	cursorRestored := false
	restoredCursor := filepath.Join(filepath.Dir(resultJSON), "cursor.json")
	if data, err := os.ReadFile(cursorBackup); err == nil {
		if err := os.WriteFile(restoredCursor, data, 0600); err != nil {
			b.log.Warn("cursor restore failed", "error", err)
		} else {
			cursorRestored = true
			b.log.Info("cursor restored, migration will resume")
		}
		os.Remove(cursorBackup)
	}

	info, err := export.Analyze(resultJSON)
	if err != nil {
		b.log.Error("analyze failed", "error", err)
		b.reply(msg.Chat.ID, "Ошибка анализа экспорта. Проверь формат result.json.")
		return
	}

	// Log upload in persistent storage
	uploadID, uploadErr := b.storage.SaveUpload(context.Background(), storage.Upload{
		UserID:       msg.From.ID,
		Filename:     doc.FileName,
		FileSize:     int64(doc.FileSize),
		ExportHash:   newHash,
		MessageCount: info.Messages,
	})
	if uploadErr != nil {
		b.log.Warn("save upload failed", "error", uploadErr)
	}

	sess.mu.Lock()
	sess.ExportPath = resultJSON
	sess.ExportDir = extractDir
	sess.ExportHash = newHash
	sess.State = StateAwaitingChatSearch
	sess.LastUploadID = uploadID
	sess.mu.Unlock()

	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ Экспорт загружен!\n\n")
	if info.ChatName != "" {
		fmt.Fprintf(&sb, "💬 Чат: %s\n", info.ChatName)
	}
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
	if info.FirstDate != "" && info.LastDate != "" {
		fmt.Fprintf(&sb, "📅 Период: %s — %s\n", info.FirstDate, info.LastDate)
	}
	if info.MissingMedia > 0 {
		fmt.Fprintf(&sb, "⚠️ Медиа-файлов не найдено: %d (будут отправлены как текст)\n", info.MissingMedia)
	}

	if cursorRestored {
		sb.WriteString("\n🔄 Прогресс предыдущей миграции восстановлен.\n")
	}

	// Auto-search Max chats by TG chat name
	if info.ChatName != "" {
		chats := b.fetchMaxChats()
		queryLower := strings.ToLower(info.ChatName)
		var matched []maxChat
		for _, c := range chats {
			if strings.Contains(strings.ToLower(c.title), queryLower) {
				matched = append(matched, c)
			}
		}

		if len(matched) == 1 {
			sb.WriteString(fmt.Sprintf("\n🎯 Найден чат в Max: %s\n", matched[0].title))
			var rows [][]tgbotapi.InlineKeyboardButton
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("✅ %s", matched[0].title),
					fmt.Sprintf("select_chat:%d", matched[0].id),
				),
			))
			reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
			reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
			b.api.Send(reply)
			return
		} else if len(matched) > 1 {
			sb.WriteString("\nНайдено несколько чатов в Max:")
			var rows [][]tgbotapi.InlineKeyboardButton
			for _, c := range matched {
				rows = append(rows, tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData(c.title, fmt.Sprintf("select_chat:%d", c.id)),
				))
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
			reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
			b.api.Send(reply)
			return
		}
	}

	sb.WriteString("\nВведи название чата в Max для поиска.\nИли отправь числовой ID чата напрямую.")
	b.reply(msg.Chat.ID, sb.String())
}

// --- Step 2: Search / Set chat ID ---

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
		b.log.Error("read export for preview failed", "error", err)
		b.reply(chatID, "Ошибка чтения экспорта. Попробуй загрузить ZIP заново.")
		return
	}

	conv := converter.New()
	total := len(result.Messages)
	limit := min(3, total)

	var sb strings.Builder
	sess.mu.Lock()
	chatName := sess.MaxChatName
	chatIDVal := sess.MaxChatID
	filterType := sess.FilterType
	filterMonths := sess.FilterMonths
	sess.mu.Unlock()

	if chatName != "" {
		fmt.Fprintf(&sb, "Чат Max: %s (ID: %d)\n", chatName, chatIDVal)
	} else {
		fmt.Fprintf(&sb, "Max chat ID: %d\n", chatIDVal)
	}
	fmt.Fprintf(&sb, "Всего сообщений: %d\n", total)

	// Show active filter description
	switch {
	case filterType == "text":
		sb.WriteString("Фильтр: только текст\n")
	case filterType == "media":
		sb.WriteString("Фильтр: только медиа\n")
	case filterMonths > 0:
		fmt.Fprintf(&sb, "Фильтр: за последние %d мес.\n", filterMonths)
	}

	// Apply filters before dry-run for accurate stats
	filtered := result.Messages
	if filterType != "" || filterMonths > 0 {
		filtered = migrator.FilterMessages(filtered, filterType, filterMonths)
		fmt.Fprintf(&sb, "После фильтрации: %d сообщений\n", len(filtered))
	}

	// Run dry-run analysis for accurate stats and ETA
	dryStats := migrator.DryRun(filtered, conv)

	// Duration estimate based on actual output requests (more accurate than raw message count)
	estimateSec := float64(dryStats.OutputMessages) / b.rps
	if estimateSec > 60 {
		fmt.Fprintf(&sb, "⏱ Ожидаемое время: ~%s\n", (time.Duration(estimateSec) * time.Second).Round(time.Minute))
	}

	sb.WriteString("\nПервые сообщения (так будут в Max):\n\n")

	for i := 0; i < limit; i++ {
		sb.WriteString(conv.FormatForMax(result.Messages[i], ""))
		sb.WriteString("\n---\n")
	}

	// Dry-run statistics block
	fmt.Fprintf(&sb, "\n📊 Анализ миграции:\n")
	fmt.Fprintf(&sb, "  Входных сообщений: %d\n", dryStats.TotalInput)
	fmt.Fprintf(&sb, "  Будет отправлено: %d запросов\n", dryStats.OutputMessages)
	if dryStats.GroupedCount > 0 {
		fmt.Fprintf(&sb, "  Сгруппировано: %d → меньше запросов\n", dryStats.GroupedCount)
	}
	if dryStats.SplitCount > 0 {
		fmt.Fprintf(&sb, "  Разбито (длинные): %d\n", dryStats.SplitCount)
	}
	fmt.Fprintf(&sb, "  Текстовых: %d, медиа: %d", dryStats.TextOnlyCount, dryStats.MediaCount)
	if dryStats.StickerCount > 0 {
		fmt.Fprintf(&sb, ", стикеров: %d", dryStats.StickerCount)
	}
	sb.WriteString("\n")

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

	// Global busy lock — reject if another user is migrating
	if b.checkBusy(chatID, userID) {
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
	migCtx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	sess.Cancel = cancel
	exportPath := sess.ExportPath
	maxChatID := sess.MaxChatID
	maxChatName := sess.MaxChatName
	filterType := sess.FilterType
	filterMonths := sess.FilterMonths
	lastUploadID := sess.LastUploadID
	pauseCh := make(chan struct{}, 1)
	sess.PauseCh = pauseCh
	sess.MigrationStart = time.Now()
	sess.CursorFile = filepath.Join(filepath.Dir(exportPath), "cursor.json")
	sess.CursorName = fmt.Sprintf("max-%d", maxChatID)
	sess.mu.Unlock()

	// Log migration start in persistent storage
	migDBID, dbErr := b.storage.StartMigration(ctx, storage.Migration{
		UserID:       userID,
		UploadID:     lastUploadID,
		MaxChatID:    maxChatID,
		MaxChatName:  maxChatName,
		FilterType:   filterType,
		FilterMonths: filterMonths,
	})
	if dbErr != nil {
		b.log.Warn("log migration start failed", "error", dbErr)
	}
	sess.mu.Lock()
	sess.MigrationDBID = migDBID
	sess.mu.Unlock()

	// Show migrating keyboard
	b.replyWithKeyboard(chatID, "🚀 Начинаю перенос...", keyboardMigrating())

	// Send progress message (will be edited)
	progressMsg := tgbotapi.NewMessage(chatID, "⏳ Подготовка...")
	sent, err := b.api.Send(progressMsg)
	if err != nil {
		b.log.Error("failed to send progress message", "error", err)
		cancel()
		sess.mu.Lock()
		sess.State = StateIdle
		sess.Cancel = nil
		sess.mu.Unlock()
		return
	}
	progressMsgID := sent.MessageID
	cursorFile := filepath.Join(filepath.Dir(exportPath), "cursor.json")

	go func() {
		defer func() {
			cancel()
			sess.mu.Lock()
			exportDir := sess.ExportDir
			sess.State = StateIdle
			sess.Cancel = nil
			sess.ExportPath = ""
			sess.ExportDir = ""
			sess.mu.Unlock()
			if exportDir != "" {
				os.RemoveAll(filepath.Dir(exportDir))
			}
			// Restore main keyboard (final status is in the edited progress message)
			b.replyWithKeyboard(chatID, "Миграция завершена. Отправь новый ZIP для следующего чата.", keyboardMain())
		}()

		botSender, senderErr := maxbot.NewSender(b.maxToken, b.rps)
		if senderErr != nil {
			b.log.Error("max api sender init failed", "error", senderErr)
			b.editMessage(chatID, progressMsgID, "❌ Ошибка подключения к Max API. Попробуйте позже.")
			return
		}

		botSender.SetOnRetry(func(attempt int, err error, wait time.Duration) {
			b.log.Warn("retrying", "attempt", attempt, "wait", wait, "error", err)
			errStr := err.Error()
			label := "Ошибка сети"
			if strings.Contains(errStr, "429") {
				label = "Rate limit"
			}
			b.editMessage(chatID, progressMsgID,
				fmt.Sprintf("⏳ %s (попытка %d), жду %s...", label, attempt, wait.Round(time.Second)))
		})

		b.editMessage(chatID, progressMsgID, "⏳ Перенос запущен, отправляю сообщения...")

		conv := converter.New()
		mig := migrator.New(botSender, conv, cursorFile, b.log)
		mig.SetPauseCh(pauseCh)

		cursorName := fmt.Sprintf("max-%d", maxChatID)
		mapping := []models.ChatMapping{{
			Name:         cursorName,
			TGExportPath: exportPath,
			MaxChatID:    maxChatID,
			FilterType:   filterType,
			FilterMonths: filterMonths,
		}}

		progressDone := make(chan struct{})
		defer close(progressDone)
		startedAt := time.Now()
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-progressDone:
					return
				case <-migCtx.Done():
					return
				case <-ticker.C:
					sentCount, total := readCursorProgress(cursorFile, cursorName)
					if total <= 0 {
						continue
					}
					pct := sentCount * 100 / total
					bar := progressBar(pct)
					elapsed := time.Since(startedAt).Round(time.Second)
					extra := ""
					if sentCount > 0 {
						speed := float64(sentCount) / time.Since(startedAt).Seconds()
						remaining := time.Duration(float64(total-sentCount) / speed * float64(time.Second))
						extra = fmt.Sprintf("\n%.1f msg/s · ~%s осталось", speed, remaining.Round(time.Second))
					}
					text := fmt.Sprintf("Перенос: %s %d%%\n(%d / %d) · %s%s", bar, pct, sentCount, total, elapsed, extra)
					b.editMessage(chatID, progressMsgID, text)
				}
			}
		}()

		stats, migErr := mig.MigrateAll(migCtx, mapping)

		// Log migration result in persistent storage
		if migDBID > 0 {
			finStatus := "completed"
			finErr := ""
			if migErr != nil {
				if migCtx.Err() != nil {
					finStatus = "cancelled"
				} else {
					finStatus = "failed"
				}
				finErr = migErr.Error()
			}
			if dbErr := b.storage.FinishMigration(context.Background(), migDBID, finStatus, stats.Sent, finErr); dbErr != nil {
				b.log.Warn("log migration finish failed", "error", dbErr)
			}
		}

		if migErr != nil {
			b.log.Error("migration failed", "chat", maxChatName, "error", migErr, "sent", stats.Sent)
			b.editMessage(chatID, progressMsgID,
				fmt.Sprintf("⚠️ Миграция прервана\n\n"+
					"Отправлено: %d\n\n"+
					"Прогресс сохранён — отправь тот же ZIP чтобы продолжить.", stats.Sent))
			return
		}

		var result strings.Builder
		fmt.Fprintf(&result, "✅ Перенос завершён!\n\n")
		fmt.Fprintf(&result, "📨 Отправлено: %d\n", stats.Sent)
		if stats.Skipped > 0 {
			fmt.Fprintf(&result, "⏭ Пропущено (уже были): %d\n", stats.Skipped)
		}
		if stats.MediaErrors > 0 {
			fmt.Fprintf(&result, "⚠️ Ошибки медиа: %d (отправлены как текст)\n", stats.MediaErrors)
		}
		fmt.Fprintf(&result, "⏱ Время: %s", stats.Duration.Round(time.Second))
		if stats.ForwardedCount > 0 {
			fmt.Fprintf(&result, "\n📤 Пересланных: %d", stats.ForwardedCount)
		}

		if len(stats.AuthorCounts) > 0 {
			type authorEntry struct {
				name  string
				count int
			}
			authors := make([]authorEntry, 0, len(stats.AuthorCounts))
			for name, count := range stats.AuthorCounts {
				authors = append(authors, authorEntry{name, count})
			}
			sort.Slice(authors, func(i, j int) bool {
				return authors[i].count > authors[j].count
			})
			result.WriteString("\n\nТоп авторов:")
			limit := 3
			if len(authors) < limit {
				limit = len(authors)
			}
			for _, a := range authors[:limit] {
				fmt.Fprintf(&result, "\n  %s — %d", a.name, a.count)
			}
		}

		if len(stats.FailedFiles) > 0 {
			result.WriteString("\n\n⚠️ Файлы, которые не удалось загрузить как медиа")
			result.WriteString(" (были отправлены как текст):\n")
			limit := 10
			if len(stats.FailedFiles) < limit {
				limit = len(stats.FailedFiles)
			}
			for _, f := range stats.FailedFiles[:limit] {
				fmt.Fprintf(&result, "  • %s\n", f)
			}
			if len(stats.FailedFiles) > 10 {
				fmt.Fprintf(&result, "  ...и ещё %d файлов\n", len(stats.FailedFiles)-10)
			}
			result.WriteString("Повторная попытка: отправь тот же ZIP снова.")
		}

		b.editMessage(chatID, progressMsgID, result.String())

		// Send an explicit ping for long migrations so the user gets a sound notification.
		if stats.Duration > 5*time.Minute {
			b.reply(chatID, fmt.Sprintf("🔔 Миграция завершена! Отправлено: %d, время: %s", stats.Sent, stats.Duration.Round(time.Second)))
		}
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

func (b *Bot) pauseMigration(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	if sess.State != StateMigrating {
		sess.mu.Unlock()
		b.reply(chatID, "Нет активной миграции.")
		return
	}
	sess.State = StatePaused
	pauseCh := sess.PauseCh
	sess.mu.Unlock()
	if pauseCh != nil {
		select {
		case pauseCh <- struct{}{}:
		default:
		}
	}
	b.replyWithKeyboard(chatID, "⏸ Миграция приостановлена. Прогресс сохранён.", keyboardPaused())
}

func (b *Bot) resumeMigration(chatID int64, userID int64) {
	sess := b.sessions.Get(userID)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	if sess.State != StatePaused {
		sess.mu.Unlock()
		b.reply(chatID, "Миграция не на паузе.")
		return
	}
	sess.State = StateMigrating
	pauseCh := sess.PauseCh
	sess.mu.Unlock()
	if pauseCh != nil {
		select {
		case pauseCh <- struct{}{}:
		default:
		}
	}
	b.replyWithKeyboard(chatID, "▶️ Миграция продолжается...", keyboardMigrating())
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

// --- Max API ---

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

// hashFile returns the hex-encoded SHA-256 digest of a file's contents.
// Uses streaming to avoid loading the entire file into memory.
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- Storage helpers ---

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

// --- Busy lock ---

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
// If busy, sends a message to chatID with ETA and returns true.
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
	b.reply(chatID, msg)
	return true
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

// resolveFile copies/downloads the file to dest.
// Local Bot API (--local mode) stores files on disk and returns absolute paths,
// so we read directly from the shared volume. Remote API uses HTTP download.
func (b *Bot) resolveFile(filePath, dest string) error {
	if b.tgAPIFilesDir != "" {
		// filePath is absolute container path: /var/lib/telegram-bot-api/TOKEN/documents/file.zip
		// Remap to shared volume path.
		const containerPrefix = "/var/lib/telegram-bot-api"
		relativePath := strings.TrimPrefix(filePath, containerPrefix)
		hostPath := filepath.Join(b.tgAPIFilesDir, relativePath)
		if !strings.HasPrefix(filepath.Clean(hostPath)+"/",
			filepath.Clean(b.tgAPIFilesDir)+"/") {
			return fmt.Errorf("file path escapes allowed directory")
		}
		return copyFile(hostPath, dest)
	}
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.api.Token, filePath)
	return downloadFile(url, dest)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}


const maxDownloadSize = 2 << 30 // 2 GiB

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, io.LimitReader(resp.Body, maxDownloadSize))
	return err
}
