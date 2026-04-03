# Plan: Userbot Channel Clone (TG -> Max / TG)

## Context

Текущий бот работает через ZIP-экспорты из Telegram Desktop: пользователь экспортирует чат вручную, отправляет ZIP боту, бот парсит JSON и мигрирует в Max. Это неудобно для каналов — нужен Telegram Desktop, ручная архивация, ограничение по размеру файла.

**Цель**: добавить `/clone` flow — пользователь авторизуется через свой TG-аккаунт прямо в чате с ботом, выбирает канал по имени, бот сам читает всю историю через MTProto API и клонирует контент в новый канал Max или TG на выбор.

**Входная точка**: чат с текущим TG-ботом (`cmd/tg2max-bot`). Весь UX — через сообщения/кнопки в Telegram.

---

## Архитектура решения

```
Пользователь ↔ TG Bot API (существующий) ↔ Bot handlers
                                              │
                        ┌─────────────────────┤
                        │                     │
                   [ZIP flow]          [Clone flow] ← НОВОЕ
                   (как сейчас)             │
                        │           ┌───────┴────────┐
                        │           │                │
                        │    MTProto Client     Auth Manager
                        │    (gotd/td)         (phone→code→2FA)
                        │           │
                        │    Read channel history
                        │    Download media
                        │           │
                        │    Convert tg.Message → models.Message
                        │           │
                        ├───────────┤
                        │           │
                   migrator.MigrateAll (переиспользуем)
                        │
                ┌───────┴───────┐
                │               │
          maxbot.Sender    tgsender.Sender ← НОВОЕ
          (→ Max chat)     (→ TG channel via MTProto)
```

---

## Библиотека: `github.com/gotd/td`

Pure Go MTProto 2.0. Без CGO — совместимо с текущим стеком (modernc.org/sqlite тоже pure Go). Встроенные `auth.Flow`, `query.GetHistoryQueryBuilder`, `downloader`. Активно поддерживается.

---

## Новые пакеты

### 1. `internal/tgclient/` — обёртка MTProto

| Файл | Назначение |
|------|-----------|
| `client.go` | `Client` struct, конструктор `New(appID, appHash, sessionStore, log)`, `Run()` |
| `auth.go` | `BotConversationAuth` — реализует `auth.UserAuthenticator` через Go-каналы (бот передаёт phone/code/password) |
| `channels.go` | `ListChannels()`, `SearchChannels(query)`, `ReadHistory(channel, opts) ([]models.Message, error)`, `DownloadMedia()` |
| `converter.go` | `ConvertMessage(tg.Message, users, basePath) → models.Message` — мост между MTProto и `pkg/models` |
| `session.go` | `SQLiteSessionStorage` — зашифрованное хранение MTProto-сессий в SQLite (AES-256-GCM) |

### 2. `internal/tgsender/` — Sender для TG (MTProto)

| Файл | Назначение |
|------|-----------|
| `sender.go` | Реализует `migrator.Sender` через MTProto: `SendText`, `SendWithPhoto`, `SendWithMedia`, `CreateChannel()`. Обработка FLOOD_WAIT. |

---

## Изменения в существующих файлах

### `internal/bot/session.go` — новые FSM-состояния

```go
StateAwaitingPhone         State = 10 // ждём номер телефона
StateAwaitingCode          State = 11 // ждём код подтверждения
StateAwaitingPassword      State = 12 // ждём 2FA-пароль
StateAwaitingChannelSearch State = 13 // ждём имя канала для поиска
StateAwaitingChannelSelect State = 14 // показали список, ждём выбор
StateAwaitingDestChoice    State = 15 // ждём выбор: Max или TG
StateAwaitingCloneChat     State = 16 // Max выбран → ждём выбор чата Max
StateAwaitingCloneConfirm  State = 17 // всё выбрано, ждём подтверждение
StateCloneMigrating        State = 18 // клон-миграция идёт
StateClonePaused           State = 19 // клон-миграция на паузе
```

Новые поля `Session`:
```go
TGClient          *tgclient.Client              // MTProto-клиент
TGAuth            *tgclient.BotConversationAuth  // координатор auth-flow
SourceChannel     *tgclient.ChannelInfo          // выбранный исходный канал
DestType          string                         // "max" | "tg"
CloneChannelID    int64                          // ID созданного канала-клона
CloneChannelName  string                         // имя канала-клона
TempMediaDir      string                         // временная папка для медиа
```

### `internal/bot/bot.go` — конфиг

Новые поля `Config`:
```go
TGAppID           int    // Telegram API app_id (my.telegram.org)
TGAppHash         string // Telegram API app_hash
UserbotSessionKey string // AES-ключ для шифрования сессий
```

Новые поля `Bot`:
```go
tgAppID           int
tgAppHash         string
userbotSessionKey []byte
```

### `internal/bot/handlers.go` — роутинг

Добавить в `handleMessage`:
```go
case "clone":
    b.handleClone(msg)
```

Добавить в state-routing:
```go
case StateAwaitingPhone:    b.handlePhone(msg)
case StateAwaitingCode:     b.handleCode(msg)
case StateAwaitingPassword: b.handlePassword(msg)
case StateAwaitingChannelSearch: b.handleChannelSearch(msg)
```

Добавить в `handleCallback`:
```go
case strings.HasPrefix(cb.Data, "select_source:"): ...
case cb.Data == "dest_max" || cb.Data == "dest_tg": ...
case strings.HasPrefix(cb.Data, "select_clone_chat:"): ...
case cb.Data == "confirm_clone": ...
```

### `internal/bot/clone.go` — НОВЫЙ ФАЙЛ (~500 строк)

Все хэндлеры clone-flow:
- `handleClone(msg)` → показывает предупреждение, спрашивает телефон
- `handlePhone(msg)` → запускает MTProto auth в горутине, ждёт код
- `handleCode(msg)` → передаёт код в auth-flow; при 2FA → `StateAwaitingPassword`
- `handlePassword(msg)` → передаёт пароль
- `handleChannelSearch(msg)` → поиск по имени среди каналов пользователя
- `handleChannelSelect(cb)` → показывает inline-кнопки [Max] [Telegram]
- `handleDestChoice(cb)` → Max: searchMaxChats, TG: создаёт канал
- `startCloneMigration(ctx, chatID, userID)` → горутина миграции

### `internal/bot/keyboard.go` — новые клавиатуры

```go
keyboardCloneDest()      // [📦 В Max] [📢 В Telegram]
keyboardCloneConfirm()   // [✅ Клонировать] [👁 Предпросмотр] / [❌ Отмена]
keyboardCloneMigrating() // [⏸ Пауза] [📊 Статус] / [❌ Отменить]
keyboardClonePaused()    // [▶️ Продолжить] [📊 Статус] / [❌ Отменить]
```

### `internal/migrator/migrator.go` — новый метод

Выделить `MigrateMessages(ctx, chatID, chatName string, messages []models.Message, filterType string, filterMonths int) (Stats, error)` — ядро миграции без привязки к файлу. Существующий `migrate()` вызывает `MigrateMessages` после `ReadAll`. Clone-flow вызывает `MigrateMessages` напрямую с сконвертированными сообщениями.

### `internal/storage/` — схема v2

Новая таблица:
```sql
CREATE TABLE userbot_sessions (
    user_id      INTEGER PRIMARY KEY REFERENCES users(telegram_id),
    session_data BLOB NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
```

Новые колонки в `migrations`:
```sql
ALTER TABLE migrations ADD COLUMN source_type TEXT NOT NULL DEFAULT 'zip';
ALTER TABLE migrations ADD COLUMN source_channel TEXT NOT NULL DEFAULT '';
ALTER TABLE migrations ADD COLUMN dest_type TEXT NOT NULL DEFAULT 'max';
```

Новые методы `Storage`:
```go
SaveUserbotSession(ctx, userID int64, data []byte) error
LoadUserbotSession(ctx, userID int64) ([]byte, error)
DeleteUserbotSession(ctx, userID int64) error
```

### `cmd/tg2max-bot/main.go` — env-переменные

```
TG_APP_ID, TG_APP_HASH, USERBOT_SESSION_KEY
```

### `internal/bot/bot.go` — команда в меню

```go
tgbotapi.BotCommand{Command: "clone", Description: "Клонировать канал через аккаунт"},
```

---

## UX Flow (чат с ботом)

```
User: /clone
Bot:  ⚠️ Для клонирования нужен доступ к вашему TG-аккаунту.
      Это безопасно — сессия используется только для чтения канала
      и удаляется после завершения.
      
      Введите номер телефона (формат: +7XXXXXXXXXX):

User: +79991234567
Bot:  📱 Код подтверждения отправлен в Telegram.
      Введите код:

User: 12345
Bot:  ✅ Авторизация успешна!
      Введите название канала для поиска:

User: Python News
Bot:  Найдено:
      [Python News Daily (12.5K подписчиков)]
      [Python News RU (3.2K подписчиков)]
      [All About Python (890 подписчиков)]

User: [нажимает "Python News Daily"]
Bot:  📢 Python News Daily — 4,521 сообщений
      
      Куда клонировать?
      [📦 В Max]  [📢 В Telegram]

User: [нажимает "📦 В Max"]
Bot:  Введите название чата в Max для поиска:
      (или числовой ID)

User: Python
Bot:  [Python Dev (канал)]
      [Python Chat (группа)]

User: [нажимает "Python Dev"]
Bot:  Готово к клонированию:
      📥 Источник: Python News Daily (TG)
      📤 Назначение: Python Dev (Max)
      📊 Сообщений: ~4,521
      ⏱ Примерное время: ~75 мин (при 1 msg/s)
      
      [✅ Клонировать] [👁 Предпросмотр]
      [❌ Отмена]

User: [✅ Клонировать]
Bot:  🚀 Клонирование запущено...
      ░░░░░░░░░░░░░░░░░░░░ 0% (0/4521) ~75 мин
      [⏸ Пауза] [📊 Статус]
      [❌ Отменить]
```

При выборе "📢 В Telegram":
```
Bot:  Создать новый канал в Telegram?
      Название: Python News Daily (clone)
      
      [✅ Создать и клонировать] [❌ Отмена]
```

---

## Фазы реализации

### Фаза 1: Фундамент — MTProto + Auth (~2 дня)
**Создать**: `internal/tgclient/client.go`, `auth.go`, `session.go`
**Изменить**: `go.mod`, `cmd/tg2max-bot/main.go`, `internal/bot/bot.go`, `internal/bot/session.go`, `internal/storage/storage.go`, `internal/storage/sqlite.go`, `internal/storage/nop.go`
**Проверка**: бот принимает `/clone`, проводит auth через phone+code, печатает имя аккаунта

### Фаза 2: Поиск и выбор каналов (~1 день)
**Создать**: `internal/tgclient/channels.go`, `internal/bot/clone.go` (первая часть), `internal/bot/keyboard.go` (дополнения)
**Изменить**: `internal/bot/handlers.go`
**Проверка**: авторизованный пользователь ищет канал по имени, видит результаты в inline-кнопках

### Фаза 3: Чтение истории + конвертация (~2 дня)
**Создать**: `internal/tgclient/converter.go`
**Изменить**: `internal/tgclient/channels.go` (ReadHistory + DownloadMedia)
**Проверка**: юнит-тесты конвертера `tg.Message → models.Message`. Ручная проверка: прочитать 100 сообщений канала, вывести preview

### Фаза 4: Рефакторинг мигратора + Clone→Max (~1 день)
**Изменить**: `internal/migrator/migrator.go` (выделить `MigrateMessages`), `internal/bot/clone.go` (добавить `startCloneMigration`)
**Проверка**: e2e: auth → выбрать канал → выбрать Max чат → миграция 50 сообщений → проверить в Max

### Фаза 5: Clone→Telegram — TG Sender (~2 дня)
**Создать**: `internal/tgsender/sender.go`
**Изменить**: `internal/bot/clone.go` (ветка TG с созданием канала)
**Проверка**: e2e: auth → выбрать канал → выбрать "В Telegram" → создан новый канал → сообщения клонированы

### Фаза 6: Прогресс, пауза, hardening (~1 день)
- Progress bar с ETA (переиспользовать паттерн из `migration.go`)
- Pause/Resume для clone-миграций
- Таймаут на auth-flow (5 мин)
- Отключение MTProto клиента после миграции
- Очистка temp media dir
- Обновление admin UI для отображения clone-миграций

---

## Безопасность

- **MTProto сессии**: AES-256-GCM шифрование в SQLite, ключ из env `USERBOT_SESSION_KEY`
- **Телефоны**: не сохраняются — только в памяти на время auth flow
- **2FA пароли**: проходят через Go-канал, не логируются, GC после использования
- **Ограничение доступа**: clone доступен только `allowed_user_ids`; опционально отдельный `clone_allowed_user_ids`
- **Дисклеймер**: предупреждение о доступе к аккаунту перед началом flow
- **Auto-disconnect**: MTProto клиент отключается после миграции или по таймауту сессии
- **Flood protection**: respect FLOOD_WAIT от Telegram, backoff с jitter

---

## Verification

1. **Unit tests**: `tgclient/converter_test.go` — конвертация всех типов сообщений (текст, медиа, replies, forwards, entities)
2. **Unit tests**: `tgsender/sender_test.go` — mock MTProto client, проверка отправки
3. **Integration**: auth flow с реальным TG аккаунтом (ручной тест)
4. **e2e Max**: clone test-канала → Max чат, проверить контент
5. **e2e TG**: clone test-канала → новый TG канал, проверить контент
6. **Resume**: прервать миграцию, перезапустить → нет дубликатов
7. **`make build`**: компиляция без ошибок
8. **`make test`**: все тесты проходят
9. **`make lint`**: golangci-lint clean

---

## Открытые вопросы

1. **Max Bot API — создание каналов**: поддерживает ли `max-bot-api-client-go` создание каналов? Если нет — для Max только выбор существующего чата (как сейчас)
2. **Лимиты gotd/td**: максимальный размер скачиваемого файла, rate limits для GetHistory — нужно проверить в docs
3. **Параллельные userbot-сессии**: разрешать ли нескольким пользователям одновременно авторизоваться? (рекомендация: да, но миграция — одна за раз, как сейчас)
