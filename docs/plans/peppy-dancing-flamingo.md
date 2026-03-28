# Plan: Restore fetchMaxChats + configure + verify

## Context

Проект tg2max перевели с user-авторизации Max (`maxuser`) на Bot API (`maxbot`). При удалении `maxuser` случайно удалили `fetchMaxChats()` из `bot.go` — она использовала **Bot API** (не maxuser) для листинга чатов. Пользователь получил рабочий API ключ Max-бота. Нужно восстановить UX автовыбора чатов, настроить конфиг и проверить сборку/тесты.

## Steps

### 1. Restore `fetchMaxChats()` in `internal/bot/bot.go`

Восстановить удалённый код:
- Добавить `maxbotapi "github.com/max-messenger/max-bot-api-client-go"` в импорты
- Восстановить struct `maxChat{id int64, title string}`
- Восстановить метод `fetchMaxChats()` — вызывает `api.Chats.GetChats(ctx, 50, 0)`, фильтрует `Status != "active"`, возвращает `[]maxChat`
- Использовать `schemes.ACTIVE` и `schemes.DIALOG` вместо строковых литералов (типобезопасность)

### 2. Restore inline buttons in `handleDocument()`

Заменить текущий fallback (просто просит ввести число) на:
- Вызов `fetchMaxChats()`
- Если чаты найдены → inline keyboard с кнопками `select_chat:<id>`
- Если нет → fallback с текстом "Отправь Max chat ID (число)"
- Код был в коммите 56e30f4, восстановить с минимальными правками

### 3. Configure `configs/config.example.yaml`

- Оставить placeholder `"your-max-bot-token"` (не коммитить реальный ключ)
- Пользователь сам создаст `config.yaml` с реальным ключом

### 4. Build + test

- `go build ./...`
- `go test ./... -count=1`
- `go vet ./...`

## Files to modify

- `internal/bot/bot.go` — восстановить fetchMaxChats + inline buttons

## Verification

1. `go build ./...` — компиляция
2. `go test ./... -count=1` — все тесты проходят
3. `go vet ./...` — без ошибок
