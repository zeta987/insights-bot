package recapdelivery

import (
	"errors"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
)

// Sender is the Telegram surface needed to deliver a rich recap and its
// deterministic plain-text fallback.
type Sender interface {
	SendRichMessage(config tgbot.RichMessageConfig) (tgbotapi.Message, error)
	Send(chattable tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Config controls delivery metadata shared by every recap part.
type Config struct {
	ChatID                   int64
	Parts                    []string
	ReplyToMessageID         int
	ReplyMarkup              *tgbotapi.InlineKeyboardMarkup
	DisableNotification      bool
	AllowSendingWithoutReply bool
	BeforeSend               func()
}

// SendRichRecapParts sends every Rich Markdown part and replies continuation
// parts to the first delivered message. Telegram formatting-specific 400
// errors fall back to split plain text; transport and server errors do not.
func SendRichRecapParts(sender Sender, config Config) ([]tgbotapi.Message, error) {
	messages := make([]tgbotapi.Message, 0, len(config.Parts))
	plainMode := false

	for index, part := range config.Parts {
		var formattingErr error
		if !plainMode {
			replyToMessageID := continuationReplyID(messages, config.ReplyToMessageID)
			richConfig := tgbot.RichMessageConfig{
				ChatID:              config.ChatID,
				Markdown:            part,
				DisableNotification: config.DisableNotification,
			}
			if replyToMessageID != 0 {
				richConfig.ReplyParameters = &tgbot.RichMessageReplyParameters{
					MessageID:                replyToMessageID,
					AllowSendingWithoutReply: config.AllowSendingWithoutReply,
				}
			}
			if index == 0 {
				richConfig.ReplyMarkup = config.ReplyMarkup
			}

			invokeBeforeSend(config.BeforeSend)
			message, err := sender.SendRichMessage(richConfig)
			if err == nil {
				messages = append(messages, message)
				continue
			}
			if !isRichMessageFormattingError(err) {
				return messages, fmt.Errorf("send rich recap part %d: %w", index+1, err)
			}

			formattingErr = err
			plainMode = true
		}

		plainParts := chathistories.SplitPlainText(chathistories.RichMarkdownToPlainText(part), tgbot.MessageLengthLimit)
		if len(plainParts) == 0 {
			if formattingErr != nil {
				return messages, fmt.Errorf("rich recap part %d has no plain-text fallback: %w", index+1, formattingErr)
			}
			return messages, fmt.Errorf("rich recap part %d has no plain-text fallback", index+1)
		}
		for plainIndex, plainPart := range plainParts {
			plainMessage := tgbotapi.NewMessage(config.ChatID, plainPart)
			plainMessage.DisableNotification = config.DisableNotification
			plainMessage.AllowSendingWithoutReply = config.AllowSendingWithoutReply
			plainMessage.ReplyToMessageID = continuationReplyID(messages, config.ReplyToMessageID)
			if index == 0 && plainIndex == 0 && config.ReplyMarkup != nil {
				plainMessage.ReplyMarkup = *config.ReplyMarkup
			}

			invokeBeforeSend(config.BeforeSend)
			sent, sendErr := sender.Send(plainMessage)
			if sendErr != nil {
				return messages, fmt.Errorf("send plain recap fallback part %d.%d: %w", index+1, plainIndex+1, sendErr)
			}
			messages = append(messages, sent)
		}
	}

	return messages, nil
}

func invokeBeforeSend(beforeSend func()) {
	if beforeSend != nil {
		beforeSend()
	}
}

func continuationReplyID(messages []tgbotapi.Message, initialReplyID int) int {
	if len(messages) != 0 {
		return messages[0].MessageID
	}
	return initialReplyID
}

func isRichMessageFormattingError(err error) bool {
	var telegramError *tgbotapi.Error
	if !errors.As(err, &telegramError) || telegramError.Code != 400 {
		return false
	}

	message := strings.ToLower(telegramError.Message)
	for _, signature := range []string{
		"can't parse entities",
		"cannot parse entities",
		"failed to parse",
		"parse error",
		"can't find end of the entity",
		"unsupported start tag",
		"markdown",
	} {
		if strings.Contains(message, signature) {
			return true
		}
	}

	richContext := strings.Contains(message, "rich message") ||
		strings.Contains(message, "rich_message") ||
		strings.Contains(message, "inputrichmessage")
	if !richContext {
		return false
	}
	for _, signature := range []string{
		"can't parse rich message",
		"cannot parse rich message",
		"failed to parse rich message",
		"invalid rich message",
		"rich message is invalid",
		"rich message format",
		"rich message block",
		"rich message nesting",
		"rich message is too long",
	} {
		if strings.Contains(message, signature) {
			return true
		}
	}
	return false
}
