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

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/export"
	"github.com/arkosh/tg2max/internal/maxbot"
	"github.com/arkosh/tg2max/internal/migrator"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

// Bot is the Telegram bot that orchestrates chat migration from Telegram to Max.
type Bot struct {
	api      *tgbotapi.BotAPI
	sessions *SessionStore
	maxToken string
	rps      int
	tempDir  string
	log      *slog.Logger
}

// Config holds the bot configuration.
type Config struct {
	TelegramToken string
	MaxToken      string
	RateLimitRPS  int
	TempDir       string
}

// New creates a new Bot instance.
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

// Run starts the bot polling loop and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.log.Info("bot started", "username", b.api.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
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
		case "migrate":
			b.handleMigrate(ctx, msg)
		case "cancel":
			b.handleCancel(msg)
		case "preview":
			b.handlePreview(msg)
		default:
			b.reply(msg, "Неизвестная команда. /help для списка команд.")
		}
		return
	}

	// Handle document uploads (ZIP files)
	if msg.Document != nil {
		b.handleDocument(msg)
		return
	}
}

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	text := `Привет! Я помогу перенести историю чата из Telegram в Max.

Как использовать:
1. Экспортируй чат в Telegram Desktop:
   ... > Export Chat History > JSON + Media > ZIP
2. Отправь мне ZIP-файл
3. Укажи Max chat ID: /setchat <id>
4. /preview — посмотреть как будут выглядеть сообщения
5. /migrate — начать перенос

Команды: /help`
	b.reply(msg, text)
}

func (b *Bot) handleHelp(msg *tgbotapi.Message) {
	text := `Команды:
/setchat <id> — задать ID чата в Max
/status — статус текущей миграции
/preview — показать первые 3 сообщения
/migrate — начать перенос
/cancel — отменить текущую миграцию
/help — эта справка

Как узнать Max chat ID:
Добавьте бота в чат Max, отправьте сообщение — бот покажет chat_id в логах.`
	b.reply(msg, text)
}

func (b *Bot) handleSetChat(msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	if args == "" {
		b.reply(msg, "Укажите ID чата: /setchat 123456789")
		return
	}

	chatID, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Неверный ID: %s. Укажите число.", args))
		return
	}

	sess := b.sessions.GetOrCreate(msg.From.ID)
	sess.mu.Lock()
	sess.MaxChatID = chatID
	sess.mu.Unlock()

	b.reply(msg, fmt.Sprintf("Max chat ID установлен: %d", chatID))
}

func (b *Bot) handleDocument(msg *tgbotapi.Message) {
	doc := msg.Document

	if doc.MimeType != "application/zip" && !strings.HasSuffix(doc.FileName, ".zip") {
		b.reply(msg, "Отправьте ZIP-архив с экспортом Telegram Desktop.")
		return
	}

	sess := b.sessions.GetOrCreate(msg.From.ID)
	sess.mu.Lock()
	if sess.State == StateMigrating {
		sess.mu.Unlock()
		b.reply(msg, "Миграция уже идёт. /cancel для отмены.")
		return
	}
	sess.State = StateAwaitingExport
	sess.mu.Unlock()

	b.reply(msg, "Загружаю и распаковываю архив...")

	fileConfig := tgbotapi.FileConfig{FileID: doc.FileID}
	file, err := b.api.GetFile(fileConfig)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка загрузки файла: %s", err))
		return
	}

	// Create temp dir for this user
	userDir := filepath.Join(b.tempDir, fmt.Sprintf("user_%d", msg.From.ID))
	os.RemoveAll(userDir)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка создания директории: %s", err))
		return
	}

	zipPath := filepath.Join(userDir, "export.zip")
	link := file.Link(b.api.Token)

	if err := downloadFile(link, zipPath); err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка скачивания: %s", err))
		return
	}

	extractDir := filepath.Join(userDir, "extracted")
	resultJSON, err := export.Unzip(zipPath, extractDir)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка распаковки: %s", err))
		return
	}

	// Remove ZIP to save space
	os.Remove(zipPath)

	info, err := export.Analyze(resultJSON)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка анализа: %s", err))
		return
	}

	sess.mu.Lock()
	sess.ExportPath = resultJSON
	sess.ExportDir = extractDir
	sess.State = StateIdle
	sess.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("Экспорт загружен!\n\n")
	sb.WriteString(fmt.Sprintf("Сообщений: %d\n", info.Messages))
	if info.Photos > 0 {
		sb.WriteString(fmt.Sprintf("Фото: %d\n", info.Photos))
	}
	if info.Videos > 0 {
		sb.WriteString(fmt.Sprintf("Видео: %d\n", info.Videos))
	}
	if info.Documents > 0 {
		sb.WriteString(fmt.Sprintf("Документов: %d\n", info.Documents))
	}
	if info.Other > 0 {
		sb.WriteString(fmt.Sprintf("Другое: %d\n", info.Other))
	}
	sb.WriteString("\n/preview — посмотреть первые 3 сообщения")
	sb.WriteString("\n/migrate — начать перенос")

	b.reply(msg, sb.String())
}

func (b *Bot) handlePreview(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(msg, "Сначала отправьте ZIP-экспорт.")
		return
	}

	reader := telegram.NewReader(sess.ExportPath)
	result, err := reader.ReadAll(context.Background())
	if err != nil {
		b.reply(msg, fmt.Sprintf("Ошибка чтения: %s", err))
		return
	}

	conv := converter.New()
	limit := 3
	if len(result.Messages) < limit {
		limit = len(result.Messages)
	}

	var sb strings.Builder
	sb.WriteString("Предпросмотр (первые 3 сообщения):\n\n")
	for i := 0; i < limit; i++ {
		text := conv.FormatForMax(result.Messages[i])
		sb.WriteString(text)
		sb.WriteString("\n---\n")
	}

	b.reply(msg, sb.String())
}

func (b *Bot) handleMigrate(ctx context.Context, msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil || sess.ExportPath == "" {
		b.reply(msg, "Сначала отправьте ZIP-экспорт.")
		return
	}

	sess.mu.Lock()
	if sess.MaxChatID == 0 {
		sess.mu.Unlock()
		b.reply(msg, "Сначала укажите Max chat ID: /setchat <id>")
		return
	}
	if sess.State == StateMigrating {
		sess.mu.Unlock()
		b.reply(msg, "Миграция уже идёт. /status для прогресса, /cancel для отмены.")
		return
	}
	sess.State = StateMigrating
	migCtx, cancel := context.WithCancel(ctx)
	sess.Cancel = cancel
	exportPath := sess.ExportPath
	maxChatID := sess.MaxChatID
	sess.mu.Unlock()

	progressMsg := tgbotapi.NewMessage(msg.Chat.ID, "Начинаю перенос...")
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
		}()

		sender, senderErr := maxbot.NewSender(b.maxToken, b.rps)
		if senderErr != nil {
			b.editMessage(msg.Chat.ID, progressMsgID, fmt.Sprintf("Ошибка подключения к Max: %s", senderErr))
			return
		}

		conv := converter.New()
		mig := migrator.New(sender, conv, cursorFile, b.log)

		mapping := []models.ChatMapping{{
			Name:         "bot-migration",
			TGExportPath: exportPath,
			MaxChatID:    maxChatID,
		}}

		// Progress tracking goroutine: reads cursor file every 3 seconds
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
					text := fmt.Sprintf("Перенос: %s %d%% (%d/%d)", bar, pct, sentCount, total)
					b.editMessage(msg.Chat.ID, progressMsgID, text)
				}
			}
		}()

		stats, migErr := mig.MigrateAll(migCtx, mapping)

		close(progressDone)

		if migErr != nil {
			b.editMessage(msg.Chat.ID, progressMsgID,
				fmt.Sprintf("Ошибка миграции: %s\n\nОтправлено: %d\nПерезапустите /migrate для продолжения.", migErr, stats.Sent))
			return
		}

		text := fmt.Sprintf("Перенос завершён!\n\nОтправлено: %d\nПропущено: %d\nОшибки медиа: %d\nВремя: %s",
			stats.Sent, stats.Skipped, stats.MediaErrors, stats.Duration.Round(time.Second))
		b.editMessage(msg.Chat.ID, progressMsgID, text)
	}()
}

func (b *Bot) handleStatus(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil {
		b.reply(msg, "Нет активной сессии. Отправьте ZIP-экспорт.")
		return
	}

	sess.mu.Lock()
	state := sess.State
	chatID := sess.MaxChatID
	exportPath := sess.ExportPath
	sess.mu.Unlock()

	var status string
	switch state {
	case StateIdle:
		status = "Ожидание"
	case StateAwaitingExport:
		status = "Загрузка экспорта"
	case StateMigrating:
		status = "Миграция идёт"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Статус: %s\n", status))
	sb.WriteString(fmt.Sprintf("Max chat ID: %d\n", chatID))
	if exportPath != "" {
		sb.WriteString("Экспорт: загружен\n")
	} else {
		sb.WriteString("Экспорт: не загружен\n")
	}

	b.reply(msg, sb.String())
}

func (b *Bot) handleCancel(msg *tgbotapi.Message) {
	sess := b.sessions.Get(msg.From.ID)
	if sess == nil {
		b.reply(msg, "Нет активной сессии.")
		return
	}

	sess.mu.Lock()
	if sess.State != StateMigrating || sess.Cancel == nil {
		sess.mu.Unlock()
		b.reply(msg, "Нет активной миграции для отмены.")
		return
	}
	sess.Cancel()
	sess.mu.Unlock()

	b.reply(msg, "Миграция отменена. Прогресс сохранён, /migrate для продолжения.")
}

func (b *Bot) reply(msg *tgbotapi.Message, text string) {
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	if _, err := b.api.Send(reply); err != nil {
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
	filled := pct / 10
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	empty := 10 - filled
	return strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", empty)
}

// readCursorProgress reads the cursor file and returns (sent, total) for the given chat name.
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

// downloadFile downloads a URL to a local file path.
func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec // URL is from Telegram API
	if err != nil {
		return fmt.Errorf("http get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http get %s: status %d", url, resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file %s: %w", dest, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write file %s: %w", dest, err)
	}

	return nil
}
