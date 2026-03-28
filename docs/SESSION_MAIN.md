# Session Log

## [2026-03-28 00:00:00] Tests: internal/migrator (83.5%) + internal/converter (94.2%)

### internal/migrator — добавлено в migrator_test.go
- `Test_ReplyTextFor_NilReplyToID/IDExistsInIndex/IDNotInIndex` — все ветки replyTextFor
- `Test_DryRun_EmptyMessages/SingleText/GroupableMessages/MediaMessages/StickerMessage/LongMessageSplits/MultipleMediaAttachments` — полное покрытие DryRun
- `Test_SendMessage_StickerEmojiSendsTextOnly` — стикер → только SendText, без photo/media
- `Test_SendMessage_TextOnlySendsText/PhotoMessage/NonPhotoMedia/WithReplyContext` — ветки sendMessage
- `Test_SendMessage_MultipleMediaAttachments/MultipleMediaWithNonPhoto/AdditionalStickerMediaViaPhotoPath` — дополнительные медиа
- `Test_SetPauseCh_AssignsChannel` — SetPauseCh теперь 100%
- `Test_CursorSave_FailsOnReadOnlyDir` — ветка ошибки cursor.Save
- `Test_MigrateAll_WithFilterType/WithFilterMonths` — ветка filter в migrate()

### internal/converter — добавлено в converter_test.go
- `TestSplitMessage_CyrillicText` — 10 кириллических рун, maxLen=5 → 2 чанка по 5
- `TestSplitMessage_MixedASCIIAndCyrillic` — ASCII + кириллица, разбивка по пробелу
- `TestSplitMessage_LongCyrillicNoBreakpoint` — 20 рун без пробелов, force-split
- `TestCanGroup` — 7 table-driven кейсов: same author/diff author/5min boundary/media/empty author
- `TestNewWithTimezone_DifferentZoneProducesDifferentOutput` — UTC vs MSK дают разный результат
- `TestNewWithTimezone_NilSafeCustomZone` — EST зона корректно применяется

## [2026-03-28 XX:XX:XX] Tests: internal/export + internal/telegram coverage

### internal/export (создан unzip_test.go)
- `TestAnalyze_ValidMessages` — messages/photos/videos/documents counted correctly
- `TestAnalyze_ChatName` — ChatName извлекается из JSON
- `TestAnalyze_FirstAndLastDate` — FirstDate/LastDate из date_unixtime (spot-check years)
- `TestAnalyze_MissingMedia` — файлы не на диске → MissingMedia++
- `TestAnalyze_MissingMedia_PresentFileNotCounted` — существующий файл не считается missing
- `TestAnalyze_OtherMediaType` — non-video media_type → Other++
- `TestAnalyze_ServiceMessagesIgnored` — type=service не считается
- `TestAnalyze_EmptyMessages` — пустой массив, FirstDate/LastDate пустые
- `TestAnalyze_InvalidJSON` — невалидный JSON → error
- `TestAnalyze_FileNotFound` — несуществующий файл → error
- `TestAnalyze_VideoMessage` — media_type=video_message → Videos++
- `TestUnzip_ValidZipWithResultJSON` — валидный ZIP → путь к result.json
- `TestUnzip_NestedResultJSON` — result.json в подпапке
- `TestUnzip_MissingResultJSON` — нет result.json → error с "result.json"
- `TestUnzip_PathTraversal` — ../../../tmp/evil.txt → error "illegal file path"
- `TestUnzip_InvalidZipFile` — не-ZIP файл → error
- `TestUnzip_DirectoryEntry` — ZIP с явной dir-записью + result.json
- `TestAnalyze_FormatDate_ZeroUnix` / `TestAnalyze_FormatDate_InvalidString` — edge-cases formatDate

### internal/telegram (добавлено в reader_test.go)
- `TestConvertMessage_PollData` — poll → TextPart с "📊 Опрос:"
- `TestConvertMessage_ContactInformation` — contact → TextPart с "👤 Контакт:"
- `TestConvertMessage_LocationInformation` — location → TextPart с "📍 Геолокация:"
- `TestSafeMediaPath_NormalPath/TraversalRejected/AbsolutePathRejected`
- `TestConvertMessage_StickerEmoji` — sticker_emoji поле заполнено
- `TestResolveMediaType` — table-driven, все 8 типов
- `TestParseTimestamp_FallbackToDateField` — date_unixtime отсутствует, используется date
- `TestParseTimestamp_NoValidTimestamp` — нет ни одного timestamp → Skipped++
- `TestTgText_UnmarshalJSON_MixedStringAndObject` — строка + объект в одном массиве
- `TestTgText_UnmarshalJSON_InvalidArray` — malformed JSON → error

### Покрытие
- `internal/export`: **89.4%**
- `internal/telegram`: **95.6%**

### Изменённые файлы
- `/home/arkosh/GolandProjects/tg2max/internal/export/unzip_test.go` (создан)
- `/home/arkosh/GolandProjects/tg2max/internal/telegram/reader_test.go` (дополнен)

## [2026-03-28 12:00:00] Features 8 & 9: Pause/Resume + Failed Media Tracking

### Изменённые файлы

**`internal/bot/session.go`**
- Добавлена константа `StatePaused` (после `StateMigrating`)
- Поля `PauseCh chan struct{}` и `FailedMediaIDs []int` в `Session`
- `StartCleanup`: сессии в `StatePaused` также не удаляются

**`internal/migrator/migrator.go`**
- `Stats.FailedFiles []string` — имена файлов, где media upload упал и был отправлен fallback-текст
- `Migrator.pauseCh <-chan struct{}` + `SetPauseCh(ch)` — сеттер для канала паузы
- `MigrateAll`: аккумулирует `FailedFiles` из chatStats
- `migrate` loop: после `i += len(group)` — non-blocking check pauseCh; при сигнале сохраняет cursor и блокируется до resume-сигнала
- `sendMessage`: сигнатура изменена на `(mediaErrors int, failedFile string, err error)`; при media-ошибке первого вложения записывает `failedFile = first.FileName`

**`internal/bot/bot.go`**
- `keyboardMigrating()`: кнопки `⏸ Пауза` + `📊 Статус` / `❌ Отменить миграцию`
- `keyboardPaused()`: кнопки `▶️ Продолжить` + `📊 Статус` / `❌ Отменить миграцию`
- `handleMessage` switch: новые кейсы `"⏸ Пауза"` → `pauseMigration`, `"▶️ Продолжить"` → `resumeMigration`
- `pauseMigration(chatID, userID)`: State StateMigrating → StatePaused, неблокирующая отправка в PauseCh
- `resumeMigration(chatID, userID)`: State StatePaused → StateMigrating, неблокирующая отправка в PauseCh
- `startMigration`: создаёт `pauseCh = make(chan struct{}, 1)`, присваивает `sess.PauseCh`, вызывает `mig.SetPauseCh(pauseCh)`
- `startMigration` summary: если `len(stats.FailedFiles) > 0` — выводит список (до 10) с пометкой о повторе
- `showStatus`: добавлен `StatePaused: "⏸ Миграция на паузе"`
- `cancelMigration`: обрабатывает оба `StateMigrating` и `StatePaused`; при паузе разблокирует горутину перед cancel
- `handleReset`: аналогично обрабатывает StatePaused
- `handleStart`: при StatePaused показывает `keyboardPaused()`
- `handleDocument`: блокирует загрузку ZIP при `StatePaused`

### Результат
- Сборка: OK
- Тесты: OK (все пакеты, 0 failures)

## [2026-03-28 23:45:00] Feature 10: Dry-run preview with metrics

### Изменённые файлы
- `internal/migrator/dryrun.go` — новый файл; `DryRunStats` struct + `DryRun()` функция; полный проход по сообщениям без отправки, подсчёт: TotalInput, OutputMessages, GroupedCount, SplitCount, MediaCount, StickerCount, TextOnlyCount
- `internal/bot/bot.go` — `showPreviewAndConfirm`: вызов `migrator.DryRun()` после чтения экспорта; блок `📊 Анализ миграции:` в конце превью; оценка времени теперь от `dryStats.OutputMessages` вместо `total`

### Результат
- Сборка: OK
- Тесты: OK (5 пакетов, 0 failures)

## [2026-03-28 23:25:00] Feature 6: Content filtering before migration

### Изменённые файлы
- `internal/bot/session.go` — добавлены `FilterType string` и `FilterMonths int` в `Session`; добавлен `StateAwaitingFilter`
- `pkg/models/message.go` — добавлены `FilterType string` и `FilterMonths int` в `ChatMapping`
- `internal/bot/bot.go`:
  - `handleSelectChat` — переход в `StateAwaitingFilter` вместо `StateAwaitingConfirm`; вызов `showFilterKeyboard`
  - `showFilterKeyboard` — новая функция, отправляет inline-клавиатуру 2×3: Всё/Только текст/Только медиа/За 3 мес/За 6 мес/За всё время
  - `handleFilterCallback` — парсит `filter:*` callback, сохраняет фильтр в сессию, переходит к `showPreviewAndConfirm`
  - `handleCallback` — добавлена ветка `strings.HasPrefix(cb.Data, "filter:")`
  - `searchMaxChats` — при вводе числа тоже показывает фильтры (StateAwaitingFilter)
  - `showPreviewAndConfirm` — читает FilterType/FilterMonths из сессии, добавляет строку "Фильтр: ..." в вывод
  - `startMigration` — читает FilterType/FilterMonths, передаёт в `ChatMapping`
  - `showStatus` — добавлен `StateAwaitingFilter` в карту состояний; вывод фильтра
- `internal/migrator/migrator.go` — добавлена функция `filterMessages`; применяется после построения `pending` если фильтры заданы; логирует до/после
- `internal/migrator/migrator_test.go` — 6 новых тестов: All, TextOnly, MediaOnly, DateCutoff, DateAndTypeCombo, EmptyInput

### Результат
- `go build ./...` — OK
- `go test ./... -count=1` — все пакеты прошли

## [2026-03-28 01:00:00] Feature 5 + Feature 7: Rich summary, polls/contacts/locations

### Feature 5: Rich migration summary with top authors
- `internal/migrator/migrator.go` — добавлены поля `AuthorCounts map[string]int` и `ForwardedCount int` в `Stats`; инициализация карты в `migrate()`; подсчёт `msg.Author` и `msg.ForwardedFrom` в цикле после отправки; слияние в `MigrateAll`
- `internal/bot/bot.go` — добавлен импорт `"sort"`; блок форматирования успеха расширен: вывод `ForwardedCount` (если > 0), сортировка авторов по убыванию через `sort.Slice`, вывод топ-3

### Feature 7: Parse polls, contacts, locations from TG export
- `internal/telegram/models.go` — добавлены типы `tgPoll`, `tgPollAnswer`, `tgContact`, `tgLocation`; поля `Poll`, `Contact`, `Location` в `tgMessage`
- `internal/telegram/reader.go` — в `convertMessage` после цикла text parts: если Poll/Contact/Location не nil — форматируем TextPart и добавляем в `m.RawParts`

### Результат
- `go build ./...` — OK
- `go test ./... -count=1` — все пакеты прошли (config, converter, migrator, telegram)

## [2026-03-28 00:00:00] Feature: Reply-chains + Sticker emoji

### Feature 1: Reply-chains
- `FormatForMax(msg, replyText string)` — добавлен второй параметр; вставляет `> ↪ text\n` перед телом, truncate 60 runes + "..."
- `FormatGroupForMax(msgs, firstReplyText string)` — аналогично для первого сообщения группы
- `internal/migrator/migrator.go` — `replyIndex map[int]string` строится перед основным циклом; helper `replyTextFor`; `sendMessage` получает `replyIndex`
- `internal/bot/bot.go` — `FormatForMax(msg, "")` в preview (reply context не нужен в превью)

### Feature 2: Sticker emoji
- `internal/telegram/models.go` — `StickerEmoji string \`json:"sticker_emoji"\`` в `tgMessage`
- `pkg/models/message.go` — `StickerEmoji string` в `Message`
- `internal/telegram/reader.go` — `StickerEmoji: msg.StickerEmoji` в `convertMessage`
- `internal/converter/converter.go` — если `msg.StickerEmoji != ""`, вывести `\n{emoji} (стикер)` вместо медиа-строки
- `internal/migrator/migrator.go` — если `msg.StickerEmoji != ""`, пропустить загрузку медиа, отправить текстом

### Тесты
- Обновлены все вызовы `FormatForMax` в `converter_test.go` — добавлен `""` вторым аргументом
- Добавлены 8 новых тестов: reply truncation, reply exact 60, group reply, sticker emoji (3 случая)
- `go build ./...` — OK; `go test ./... -count=1` — все пакеты прошли

## [2026-03-16 00:00:00] Fix issues in tg2max

### Task 1: Log skipped messages in reader
- Added `ReadResult` struct to `internal/telegram/reader.go` with `Messages`, `Skipped`, `Total` fields
- Changed `ReadAll` signature from `([]models.Message, error)` to `(ReadResult, error)`
- Added `slog.Warn` for each skipped message with message_id and error reason
- Updated `internal/migrator/migrator.go` to use `ReadResult` and log skipped count
- Updated `internal/telegram/reader_test.go` to use new return type

### Task 2: Memory optimization
- Added `data = nil` after `json.Unmarshal` in `ReadAll` to free raw bytes

### Task 3: Config validation
- `TGExportPath` not empty AND file exists via `os.Stat`
- `MaxChatID > 0`
- `Name` not empty AND unique across mappings (via `seen` map)
- `RateLimitRPS > 0` (changed from `!= 0` to `<= 0`)
- Fallback to `os.Getenv("MAX_TOKEN")` if `max_token` is empty in config

### Additional fixes (linter-introduced)
- Fixed `schemes.ApiError` (nonexistent) to `maxbotapi.APIError` with correct field `Code` in sender.go
- Adapted to linter's refactoring: `MigrateAll` now returns `(Stats, error)`, `Migrate` renamed to private `migrate`

### Files changed
- `/home/arkosh/GolandProjects/tg2max/internal/telegram/reader.go`
- `/home/arkosh/GolandProjects/tg2max/internal/telegram/reader_test.go`
- `/home/arkosh/GolandProjects/tg2max/internal/migrator/migrator.go`
- `/home/arkosh/GolandProjects/tg2max/internal/config/config.go`
- `/home/arkosh/GolandProjects/tg2max/internal/maxbot/sender.go`
- `/home/arkosh/GolandProjects/tg2max/cmd/tg2max/main.go`

### Result
All tests pass, project compiles successfully.

## [2026-03-16 13:06:26] Reliability and UX features

### Task 1: Retry with exponential backoff in sender
- Added `withRetry` method with backoff [1s, 2s, 4s], 3 attempts
- Retries on network errors (`NetworkError`, `TimeoutError`) and HTTP 429/5xx (`APIError`)
- Non-retryable errors returned immediately
- All `SendText`, `SendWithPhoto`, `SendWithMedia` wrapped

### Task 2: Message length splitting in converter
- Added `MaxMessageLength = 4096` constant
- Added `SplitMessage(text, maxLen)` method: splits at newline > space > force
- Added 7 tests for SplitMessage

### Task 3: CLI flags --dry-run and --verbose
- `--dry-run`: uses `dryRunSender` (counts messages, does not send)
- `--verbose`: sets slog level to Debug
- Stats printed at end in both modes

### Task 4: Migration statistics
- Added `Stats` struct: `Sent`, `Skipped`, `MediaErrors`, `Duration`
- `MigrateAll` returns `(Stats, error)`
- `sendMessage` returns `(int, error)` for media error tracking
- `migrate` accumulates stats per chat, `MigrateAll` aggregates

### Files changed
- `internal/maxbot/sender.go`
- `internal/converter/converter.go`
- `internal/converter/converter_test.go`
- `internal/migrator/migrator.go`
- `cmd/tg2max/main.go`

### Result
All builds pass, all tests pass (converter: 32 tests, telegram: 10 tests).

## [2026-03-16 15:00:00] Bot entry point and DX files

### Created files
- `cmd/tg2max-bot/main.go` — entry point: YAML/env config, graceful shutdown via SIGINT/SIGTERM
- `internal/bot/bot.go` — stub: Config, Bot struct, New constructor, Run method
- `Makefile` — build, build-bot, build-all, test, lint, clean targets
- `Dockerfile` — multi-stage build (golang:1.26-alpine -> alpine:3.21)

### Result
`go build ./cmd/tg2max-bot/` compiles successfully.

## [2026-03-16 16:00:00] Telegram bot handlers

### Implementation
- Full `internal/bot/bot.go` with all command handlers
- Commands: /start, /help, /setchat, /status, /preview, /migrate, /cancel
- ZIP upload: download via TG API, extract, analyze, store session
- Migration: runs in goroutine, progress tracked by reading cursor.json every 3s
- Progress bar with unicode block chars, edits TG message in-place
- Cancel support via context cancellation
- `downloadFile` helper using net/http

### Design decisions
- No migrator modification needed: progress tracked by reading cursor.json periodically
- Separate goroutine for progress updates, communicates via channel + context
- Session state machine prevents concurrent migrations

### Files changed
- `internal/bot/bot.go` — full rewrite from stub

### Result
`go build ./...` compiles successfully.

## [2026-03-16 18:00:00] DevOps infrastructure

### docker-compose.yml (created)
- Service `tg-bot-api`: aiogram/telegram-bot-api:latest, port 47819:8081, local TG API with custom entrypoint
- Service `tg2max-bot`: builds from Dockerfile, depends_on tg-bot-api, mounts config.yaml and /tmp/tg2max

### Dockerfile (updated)
- Now builds both binaries (tg2max + tg2max-bot) in build stage
- Runtime stage changed to alpine:latest with docker-cli installed

### Makefile (updated)
- Added `up`, `down`, `logs-docker`, `logs-docker-f` targets for docker-compose management
- kill/restart targets already had pidfile approach from previous session

### Validation
- `docker compose config` passes successfully

### Files changed
- `/home/arkosh/GolandProjects/tg2max/docker-compose.yml` (new)
- `/home/arkosh/GolandProjects/tg2max/Dockerfile` (updated)
- `/home/arkosh/GolandProjects/tg2max/Makefile` (updated)

## [2026-03-16 20:00:00] Refactoring: code cleanup and typed errors

### Changes
1. **Migrated websocket** — `nhooyr.io/websocket` -> `github.com/coder/websocket` in `client.go`
2. **Removed unused code** in `bot.go` — `copyOrDownload`, `copyFile`, `fetchMaxChats`, `maxChat` struct, `maxbotapi` import
3. **Fixed linter** — removed unused `ctx` param from `handleSMSCode`, updated call site
4. **Constants** — `DeviceTypeWeb`/`DeviceTypeDesktop` in `maxuser/auth.go`; `maxStatusActive`/`maxTypeDIALOG` in `bot.go`
5. **Typed errors** — `MaxError` struct in `maxuser/errors.go`, used in `client.go` Send(); `isRetryableMaxError` uses `errors.As`
6. **Token refresh** — `AuthWithTokenFile` persists new token if server returns one
7. **Truncated debug logs** — `readLoop` truncates raw recv data to 500 chars

### Files changed
- `internal/maxuser/client.go` (websocket import, MaxError, truncated logs)
- `internal/maxuser/errors.go` (new)
- `internal/maxuser/auth.go` (constants, token refresh)
- `internal/maxuser/sender.go` (errors.As for MaxError)
- `internal/bot/bot.go` (removed dead code, constants, linter fix)
- `go.mod` / `go.sum` (coder/websocket added, nhooyr removed)

### Result
All packages compile successfully. `go vet` passes.

## [2026-03-16 22:00:00] Architectural improvements: reconnect, Close interface, localized errors

### Task 1: WebSocket auto-reconnect in Client
- Added `tokenFile` field to Client struct
- Added `SetTokenFile(path)` method
- Added `reconnect(ctx)` method: closes old conn, Connect(), AuthWithTokenFile() if tokenFile set
- `Send()`: on write fail, attempts reconnect once and retries the write
- `readLoop()`: on read error, attempts reconnect if tokenFile is set (new readLoop starts from Connect)
- `maxuser.Sender` calls `client.SetTokenFile()` after auth

### Task 2: Close() in migrator.Sender interface
- Added `Close() error` to `migrator.Sender` interface
- Added `defer m.sender.Close()` at start of `MigrateAll()`
- Added `Close() error` noop to `maxbot.Sender`
- Added `Close() error` noop to `dryRunSender` in main.go
- Removed `defer userSender.Close()` from `bot.go` and `main.go` (migrator handles it now)

### Task 3: Localized error messages
- Added `LocalizedMessage() string` to `MaxError` in errors.go
- Maps: auth.request.forbidden, verify.token, attachment.not.ready, limit.violat*, proto.payload
- Added `userFriendlyError(err)` helper in bot.go
- Updated 4 error messages in bot.go to use localized messages

### Files changed
- `internal/maxuser/client.go` (tokenFile, SetTokenFile, reconnect, Send retry, readLoop reconnect)
- `internal/maxuser/sender.go` (SetTokenFile call)
- `internal/maxuser/errors.go` (LocalizedMessage)
- `internal/migrator/migrator.go` (Close in interface, defer in MigrateAll)
- `internal/maxbot/sender.go` (Close noop)
- `internal/bot/bot.go` (userFriendlyError, removed defer Close, errors import)
- `cmd/tg2max/main.go` (dryRunSender.Close, removed defer Close)

### Result
All packages compile successfully.

## [2026-03-16 23:30:00] Telegram bot UX improvements

### Task 1: Fix double preview bug
- `handleSelectChat` no longer calls `showPreviewAndConfirm` immediately
- Instead shows confirmation message with selected chat name
- Added `MaxChatName` field to Session struct
- Extracts button text from inline keyboard to display chat name
- User must explicitly click "Предпросмотр" to see preview

### Task 2: Upload progress feedback
- After sender initialization, progress message updated to "Перенос запущен, отправляю сообщения..."
- Replaces the stale "Подготовка..." that showed during sender auth

### Task 3: Re-auth without full /start
- `handleDocument`: if token missing/invalid, transitions to `StateAwaitingPhone` with re-auth prompt
- `startMigration`: if `NewSender` fails with auth error, transitions to `StateAwaitingPhone`

### Task 4: Better cancel during uploads
- Migration context wrapped with `context.WithTimeout(ctx, 4*time.Hour)`
- Prevents infinite hangs on stuck HTTP uploads

### Files changed
- `internal/bot/bot.go` (handleSelectChat, handleDocument, startMigration)
- `internal/bot/session.go` (MaxChatName field)

### Result
All packages compile successfully.
