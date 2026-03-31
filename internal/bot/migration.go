package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/internal/maxbot"
	"github.com/arkosh/tg2max/internal/migrator"
	"github.com/arkosh/tg2max/internal/storage"
	"github.com/arkosh/tg2max/internal/telegram"
	"github.com/arkosh/tg2max/pkg/models"
)

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
			// Notify waiting users that the bot is free
			b.notifyWaiting()
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

func progressBar(pct int) string {
	filled := max(0, min(10, pct/10))
	return strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 10-filled)
}
