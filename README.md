# tg2max

Утилита для переноса истории чатов из Telegram в мессенджер [Max](https://max.ru) и обратно.

Два способа миграции:
- **ZIP-экспорт**: загрузить JSON-экспорт из Telegram Desktop — бот мигрирует в Max
- **Clone канала**: авторизоваться через свой TG-аккаунт — бот сам прочитает канал и склонирует в Max или новый TG-канал

## Что переносится

- Текстовые сообщения с форматированием (bold, italic, code, ссылки, цитаты)
- Фото, видео, аудио, документы, голосовые сообщения
- Имена авторов и время отправки (MSK, конфигурируется)
- Пересланные сообщения
- Reply-цепочки с цитатами оригинала
- Стикеры (как эмодзи)
- Опросы, контакты, геолокации (как форматированный текст)

## Особенности

- Автоматическая группировка последовательных сообщений одного автора
- Поиск чатов Max по названию с inline-кнопками
- Авто-подстановка имени TG-чата для поиска в Max
- Фильтрация: только текст / только медиа / за последние N месяцев
- Пауза/возобновление миграции
- Прогресс-бар с ETA и скоростью (msg/s)
- Возобновление при сбое (cursor-based resume)
- Dry-run предпросмотр с аналитикой
- Уведомление при завершении длительных миграций
- Дедупликация повторных ZIP-загрузок (SHA-256)
- Авторизация по Telegram user ID
- **Clone**: авторизация через MTProto, поиск каналов, скачивание медиа
- **Clone**: автоматический реюз сохранённых сессий (AES-256-GCM шифрование)
- **Clone**: создание новых TG-каналов для клонирования
- **Admin panel**: веб-панель с SSE live updates, графиками, фильтрами

## Три режима работы

| Режим | Бинарник | Описание |
|-------|----------|----------|
| **CLI** | `tg2max` | Пакетная миграция по конфигу. Указываешь маппинги (экспорт -> чат) в YAML, запускаешь один раз. |
| **Telegram-бот** | `tg2max-bot` | Интерактивный. Пишешь боту в Telegram, отправляешь ZIP-архив или используешь /clone. |
| **Admin panel** | (встроен в `tg2max-bot`) | Веб-панель мониторинга: статистика, активные миграции, графики. |

## Quick Start (бот)

### 1. Создать `.env`

```bash
TELEGRAM_API_ID=your_api_id
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_TOKEN=your_telegram_bot_token
MAX_TOKEN=your_max_bot_token
ALLOWED_USER_IDS=123456789
ADMIN_ENABLED=true
ADMIN_PASSWORD=your_admin_password
DB_PATH=/tmp/tg2max/tg2max.db
```

- **Max-бот**: создать через `@MasterBot` в Max
- **TG-бот**: создать через [@BotFather](https://t.me/BotFather)
- **API ID/Hash**: получить на [my.telegram.org](https://my.telegram.org) (для Local Bot API и clone flow)
- **ALLOWED_USER_IDS**: через запятую, пустое = открыт для всех

### 2. Для clone flow (опционально)

```bash
# Те же API ID/Hash что и выше
TG_APP_ID=your_api_id
TG_APP_HASH=your_api_hash
# AES-ключ для шифрования MTProto сессий (openssl rand -hex 32)
USERBOT_SESSION_KEY=your_64_char_hex_key
```

### 3. Запустить

```bash
docker compose up -d
```

Поднимает:
- `tg-bot-api` -- Local Bot API (файлы до 2 ГБ)
- `tg2max-bot` -- Telegram-бот + admin panel на `:8080`

### 4. Использование

#### ZIP-экспорт
1. `/start` -- бот приветствует и объясняет шаги
2. Экспортировать чат в Telegram Desktop (JSON формат + медиа)
3. Отправить ZIP-архив боту
4. Выбрать чат Max, фильтры, подтвердить

#### Clone канала
1. `/clone` -- начать клонирование
2. Ввести номер телефона, код подтверждения (бот удаляет чувствительные сообщения)
3. Ввести название канала для поиска
4. Выбрать назначение: Max или новый TG-канал
5. Подтвердить -- бот читает историю, скачивает медиа, переносит

#### Admin panel (веб-интерфейс)

Доступна по адресу `http://VPS_IP:8080/admin/` (или `localhost:8080` локально).
Вход по паролю (`ADMIN_PASSWORD`). Дашборд с live-обновлениями (SSE), графики, миграции, юзеры.

Команды: `/help`, `/status`, `/cancel`, `/reset`, `/clone`, `/setchat <id>`, `/history`, `/stats`

## CLI режим

```bash
cp configs/config.example.yaml config.yaml
# Отредактировать config.yaml
./bin/tg2max --config config.yaml [--dry-run] [--verbose]
```

## Конфигурация

Все параметры задаются через `.env` (приоритет) или `config.yaml`. Пример: `configs/config.example.yaml`.

| Параметр | Env | Описание | Default |
|----------|-----|----------|---------|
| `telegram_token` | `TELEGRAM_TOKEN` | TG Bot API токен | -- |
| `max_token` | `MAX_TOKEN` | Max Bot API токен | -- |
| `rate_limit_rps` | -- | Запросов в секунду к Max API | 1 |
| `tg_api_endpoint` | `TG_API_ENDPOINT` | URL Local Bot API | -- |
| `tg_api_files_dir` | `TG_API_FILES_DIR` | Volume Local Bot API | -- |
| `allowed_user_ids` | `ALLOWED_USER_IDS` | Разрешённые TG user ID | все |
| `admin_user_ids` | `ADMIN_USER_IDS` | Админы для /stats | allowed |
| `db_path` | `DB_PATH` | Путь к SQLite БД | -- |
| `admin_enabled` | `ADMIN_ENABLED` | Включить веб-панель | false |
| `admin_addr` | `ADMIN_ADDR` | Адрес админки | :8080 |
| `admin_password` | `ADMIN_PASSWORD` | Пароль админки | -- |
| `tg_app_id` | `TG_APP_ID` | Telegram API ID (для clone) | -- |
| `tg_app_hash` | `TG_APP_HASH` | Telegram API Hash (для clone) | -- |
| -- | `USERBOT_SESSION_KEY` | AES-256 ключ (hex, 64 символа) | -- |
| -- | `ADMIN_SECURE_COOKIE` | Secure flag на cookie | true |

## Архитектура

```
cmd/tg2max/         -- CLI: пакетная миграция
cmd/tg2max-bot/     -- Telegram-бот: интерактивная миграция + admin panel
cmd/maxclean/       -- Утилита очистки чата Max
internal/admin/     -- Web admin UI (HTMX + Tailwind, SSE live updates, Chart.js)
internal/bot/       -- Логика бота: FSM, handlers, clone flow, keyboards
internal/config/    -- Загрузка и валидация конфига
internal/converter/ -- Форматирование сообщений (reply-chains, группировка, timezone)
internal/export/    -- ZIP-распаковка + анализ экспорта
internal/maxbot/    -- Клиент Max Bot API (retry, rate limit, fallback)
internal/migrator/  -- Движок миграции + cursor + dry-run + фильтрация
internal/storage/   -- SQLite persistence (WAL mode, schema v2)
internal/telegram/  -- Парсинг JSON-экспорта (polls, contacts, locations)
internal/tgclient/  -- MTProto клиент (gotd/td): auth, channels, media download
internal/tgsender/  -- Отправка в TG каналы через MTProto (migrator.Sender)
pkg/models/         -- Доменные модели
```

### FSM бота

```
ZIP flow:
  Idle -> AwaitingChatSearch -> AwaitingFilter -> AwaitingConfirm -> Migrating <-> Paused

Clone flow:
  Idle -> AwaitingPhone -> AwaitingCode -> [AwaitingPassword] -> AwaitingChannelSearch
       -> AwaitingChannelSelect -> AwaitingDestChoice -> [AwaitingCloneChat]
       -> AwaitingCloneConfirm -> CloneMigrating <-> ClonePaused
```

## Безопасность

- Авторизация по allowlist (ALLOWED_USER_IDS)
- Path traversal защита в ZIP-распаковке и медиа-путях
- Zip bomb защита (100K файлов, 2 GiB/файл, 5 GiB total)
- Лимит размера загрузки (512 МБ стандарт / 2 ГБ Local Bot API)
- Non-root контейнеры
- Local Bot API на localhost only
- Секреты только в `.env` (в .gitignore)
- MTProto сессии зашифрованы AES-256-GCM в SQLite
- Сообщения с кодом и паролем 2FA автоматически удаляются
- Admin: rate limiting (5 попыток/блок 15 мин), CSRF protection, Secure cookie
- Docker healthcheck (`/health` endpoint)
- Data race protection (sync.Mutex на shared state)
- Goroutine leak prevention (defer recover в MTProto горутинах)
- Clone: лимит 50K сообщений, 500 каналов (OOM prevention)

## Makefile

```bash
make build-all   # Собрать оба бинарника
make restart     # Пересобрать и перезапустить бота
make test        # Тесты
make lint        # golangci-lint
make up          # docker compose up -d --build
make down        # docker compose down
make logs-f      # Следить за логами docker
```

## Лицензия

MIT
