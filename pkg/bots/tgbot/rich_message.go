package tgbot

import (
	"encoding/json"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type InputRichMessage struct {
	Markdown string `json:"markdown"`
}

// RichMessageReplyParameters configures the message that a rich message replies to.
type RichMessageReplyParameters struct {
	MessageID                int   `json:"message_id"`
	ChatID                   int64 `json:"chat_id,omitempty"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply,omitempty"`
}

type RichMessageConfig struct {
	ChatID              int64
	Markdown            string
	ReplyParameters     *RichMessageReplyParameters
	ReplyMarkup         *tgbotapi.InlineKeyboardMarkup
	DisableNotification bool
}

func (config RichMessageConfig) params() (tgbotapi.Params, error) {
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", config.ChatID)
	if err := params.AddInterface("rich_message", InputRichMessage{Markdown: config.Markdown}); err != nil {
		return nil, err
	}
	if err := params.AddInterface("reply_parameters", config.ReplyParameters); err != nil {
		return nil, err
	}
	if err := params.AddInterface("reply_markup", config.ReplyMarkup); err != nil {
		return nil, err
	}
	params.AddBool("disable_notification", config.DisableNotification)

	return params, nil
}

func sendRichMessage(bot *tgbotapi.BotAPI, config RichMessageConfig) (tgbotapi.Message, error) {
	params, err := config.params()
	if err != nil {
		return tgbotapi.Message{}, err
	}

	response, err := bot.MakeRequest("sendRichMessage", params)
	if err != nil {
		return tgbotapi.Message{}, err
	}

	var message tgbotapi.Message
	if err := json.Unmarshal(response.Result, &message); err != nil {
		return tgbotapi.Message{}, err
	}

	return message, nil
}

func (b *Bot) SendRichMessage(config RichMessageConfig) (tgbotapi.Message, error) {
	return sendRichMessage(b.BotAPI, config)
}

func (b *BotService) SendRichMessage(config RichMessageConfig) (tgbotapi.Message, error) {
	return sendRichMessage(b.BotAPI, config)
}
