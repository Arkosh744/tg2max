package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/arkosh/tg2max/internal/export"
	"github.com/arkosh/tg2max/internal/storage"
)

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
				sess.mu.Lock()
				state := sess.State
				sess.mu.Unlock()
				switch state {
				case StateMigrating, StatePaused:
					b.reply(msg.Chat.ID, "Миграция уже идёт с этим экспортом.")
				case StateAwaitingConfirm:
					b.replyWithKeyboard(msg.Chat.ID, "Экспорт уже загружен. Нажми «Подтвердить перенос».", keyboardAwaitingConfirm())
				default:
					keyboard := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("▶️ Продолжить с прогрессом", "resume_export"),
							tgbotapi.NewInlineKeyboardButtonData("🔄 Начать заново", "reset_cursor"),
						),
					)
					reply := tgbotapi.NewMessage(msg.Chat.ID, "Экспорт уже загружен. Продолжить с сохранённым прогрессом или начать заново?")
					reply.ReplyMarkup = keyboard
					b.api.Send(reply)
				}
				os.RemoveAll(userDir)
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
