# tg2max

Утилита для переноса истории чатов из Telegram в мессенджер [Max](https://max.ru).

Читает JSON-экспорт из Telegram Desktop, конвертирует сообщения (текст, медиа, форматирование) и отправляет в указанный чат Max через Bot API.

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

## Два режима работы

| Режим | Бинарник | Описание |
|-------|----------|----------|
| **CLI** | `tg2max` | Пакетная миграция по конфигу. Указываешь маппинги (экспорт -> чат) в YAML, запускаешь один раз. |
| **Telegram-бот** | `tg2max-bot` | Интерактивный. Пишешь боту в Telegram, отправляешь ZIP-архив экспорта, выбираешь чат -- бот мигрирует. |

## Quick Start (бот)

### 1. Создать `.env`

```bash
TELEGRAM_API_ID=your_api_id
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_TOKEN=your_telegram_bot_token
MAX_TOKEN=your_max_bot_token
ALLOWED_USER_IDS=123456789
```

- **Max-бот**: создать через `@MasterBot` в Max
- **TG-бот**: создать через [@BotFather](https://t.me/BotFather)
- **API ID/Hash**: получить на [my.telegram.org](https://my.telegram.org) (для файлов >20 МБ)
- **ALLOWED_USER_IDS**: через запятую, пустое = открыт для всех

### 2. Запустить

```bash
docker compose up -d
```

Поднимает:
- `tg-bot-api` -- Local Bot API (файлы до 2 ГБ)
- `tg2max-bot` -- Telegram-бот

### 3. Использование

1. `/start` -- бот приветствует и объясняет шаги
2. Отправить ZIP-архив экспорта (до 512 МБ)
3. Бот показывает статистику экспорта и ищет чат в Max по названию
4. Выбрать фильтр (всё / текст / медиа / за N месяцев)
5. Предпросмотр с dry-run аналитикой
6. Подтвердить -- миграция запускается

Команды: `/help`, `/status`, `/cancel`, `/reset`, `/setchat <id>`

## CLI режим

```bash
./bin/tg2max --config config.yaml [--dry-run] [--verbose]
```

```yaml
max_token: "your-max-bot-token"
rate_limit_rps: 1
cursor_file: "cursor.json"
mappings:
  - name: "Dev Chat"
    tg_export_path: "/path/to/export/result.json"
    max_chat_id: 123456789
```

## Конфигурация

Все параметры задаются через `.env` (приоритет) или `config.yaml`.

| Параметр | Env | Описание | Default |
|----------|-----|----------|---------|
| `telegram_token` | `TELEGRAM_TOKEN` | TG Bot API токен | -- |
| `max_token` | `MAX_TOKEN` | Max Bot API токен | -- |
| `rate_limit_rps` | -- | Запросов в секунду к Max API | 1 |
| `tg_api_endpoint` | `TG_API_ENDPOINT` | URL Local Bot API | -- |
| `tg_api_files_dir` | `TG_API_FILES_DIR` | Volume Local Bot API | -- |
| `allowed_user_ids` | `ALLOWED_USER_IDS` | Разрешённые TG user ID (через запятую) | все |
| `temp_dir` | -- | Директория для распаковки | /tmp |

## Архитектура

```
cmd/tg2max/         -- CLI: пакетная миграция
cmd/tg2max-bot/     -- Telegram-бот: интерактивная миграция
cmd/maxclean/       -- Утилита очистки чата Max
internal/bot/       -- Логика бота + FSM
internal/config/    -- Загрузка и валидация конфига
internal/converter/ -- Форматирование сообщений (reply-chains, группировка, timezone)
internal/export/    -- ZIP-распаковка + анализ экспорта
internal/maxbot/    -- Клиент Max Bot API (retry, rate limit, fallback)
internal/migrator/  -- Движок миграции + cursor + dry-run + фильтрация
internal/telegram/  -- Парсинг JSON-экспорта (polls, contacts, locations)
pkg/models/         -- Доменные модели
```

### FSM бота

```
Idle -> AwaitingChatSearch -> AwaitingFilter -> AwaitingConfirm -> Migrating
                                                                      ↕
                                                                   Paused
```

## Тестовое покрытие

| Пакет | Покрытие |
|-------|----------|
| config | 97% |
| converter | 94% |
| export | 89% |
| migrator | 83% |
| telegram | 95% |

```bash
make test
```

## Безопасность

- Авторизация по allowlist (ALLOWED_USER_IDS)
- Path traversal защита в ZIP-распаковке и медиа-путях
- Zip bomb защита (100K файлов, 2 GiB/файл, 5 GiB total)
- Лимит размера загрузки (512 МБ)
- Non-root контейнеры
- Local Bot API на localhost only
- Секреты только в `.env` (в .gitignore)
- Sanitized error messages (без системных путей)
- Session TTL (24h автоочистка)

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
