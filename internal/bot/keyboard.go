package bot

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

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
