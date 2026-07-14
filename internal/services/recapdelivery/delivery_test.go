package recapdelivery

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
)

type fakeSender struct {
	richConfigs []tgbot.RichMessageConfig
	richErrors  []error
	plain       []tgbotapi.MessageConfig
	nextID      int
}

func (f *fakeSender) SendRichMessage(config tgbot.RichMessageConfig) (tgbotapi.Message, error) {
	f.richConfigs = append(f.richConfigs, config)
	index := len(f.richConfigs) - 1
	if index < len(f.richErrors) && f.richErrors[index] != nil {
		return tgbotapi.Message{}, f.richErrors[index]
	}

	return f.nextMessage(config.ChatID), nil
}

func (f *fakeSender) Send(chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
	config, ok := chattable.(tgbotapi.MessageConfig)
	if !ok {
		return tgbotapi.Message{}, errors.New("unexpected chattable type")
	}

	f.plain = append(f.plain, config)
	return f.nextMessage(config.ChatID), nil
}

func (f *fakeSender) nextMessage(chatID int64) tgbotapi.Message {
	f.nextID++
	return tgbotapi.Message{
		MessageID: f.nextID,
		Chat:      &tgbotapi.Chat{ID: chatID},
	}
}

func TestSendRichRecapParts(t *testing.T) {
	sender := &fakeSender{}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("vote", "vote")),
	)

	messages, err := SendRichRecapParts(sender, Config{
		ChatID:           -100123,
		Parts:            []string{"first", "second"},
		ReplyToMessageID: 77,
		ReplyMarkup:      &keyboard,
	})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Len(t, sender.richConfigs, 2)
	assert.Empty(t, sender.plain)

	first := sender.richConfigs[0]
	require.NotNil(t, first.ReplyParameters)
	assert.Equal(t, 77, first.ReplyParameters.MessageID)
	assert.Same(t, &keyboard, first.ReplyMarkup)

	second := sender.richConfigs[1]
	require.NotNil(t, second.ReplyParameters)
	assert.Equal(t, messages[0].MessageID, second.ReplyParameters.MessageID)
	assert.Nil(t, second.ReplyMarkup)
}

func TestSendRichRecapPartsFallsBackToPlainTextForFormattingBadRequest(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: can't parse rich message: unsupported markdown block",
		}},
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup()
	rich := "# Recap\n\n<details><summary>詳細總結</summary>\n\nSee [1](https://t.me/c/123/45).\n\n</details>"

	messages, err := SendRichRecapParts(sender, Config{
		ChatID:           123,
		Parts:            []string{rich},
		ReplyToMessageID: 9,
		ReplyMarkup:      &keyboard,
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, sender.richConfigs, 1)
	require.Len(t, sender.plain, 1)

	plain := sender.plain[0]
	assert.Empty(t, plain.ParseMode)
	assert.Equal(t, 9, plain.ReplyToMessageID)
	assert.Equal(t, keyboard, plain.ReplyMarkup)
	assert.NotContains(t, plain.Text, "<details>")
	assert.NotContains(t, plain.Text, "</details>")
	assert.Contains(t, plain.Text, "1 (https://t.me/c/123/45)")
}

func TestSendRichRecapPartsFallsBackForEntityParseErrorWithPlainFormatting(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: can't parse entities: malformed markdown",
		}},
	}
	rich := "## Recap\n\n<details><summary>詳細總結</summary>\n\n**Bold** _italic_ ~~removed~~ `code` [1](https://t.me/c/123/45)\\!\n\n</details>"

	_, err := SendRichRecapParts(sender, Config{ChatID: 123, Parts: []string{rich}})
	require.NoError(t, err)
	require.Len(t, sender.plain, 1)
	assert.Equal(
		t,
		"Recap\n\n詳細總結\n\nBold italic removed code 1 (https://t.me/c/123/45)!",
		sender.plain[0].Text,
	)
}

func TestSendRichRecapPartsInvokesBeforeSendForRichAndFallbackAttempts(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: can't parse rich message markdown",
		}},
	}
	callCount := 0

	_, err := SendRichRecapParts(sender, Config{
		ChatID: 123,
		Parts:  []string{"fallback"},
		BeforeSend: func() {
			callCount++
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, callCount)
}

func TestSendRichRecapPartsDoesNotFallbackForOtherTelegramErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "server error",
			err:  &tgbotapi.Error{Code: 500, Message: "Internal Server Error"},
		},
		{
			name: "non-format bad request",
			err:  &tgbotapi.Error{Code: 400, Message: "Bad Request: chat not found"},
		},
		{
			name: "invalid chat identifier",
			err:  &tgbotapi.Error{Code: 400, Message: "Bad Request: invalid chat_id"},
		},
		{
			name: "invalid rich reply parameters",
			err:  &tgbotapi.Error{Code: 400, Message: "Bad Request: invalid reply parameters for rich message"},
		},
		{
			name: "network error",
			err:  errors.New("request timed out"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{richErrors: []error{tt.err}}

			messages, err := SendRichRecapParts(sender, Config{ChatID: 123, Parts: []string{"recap"}})
			require.ErrorIs(t, err, tt.err)
			assert.Empty(t, messages)
			assert.Empty(t, sender.plain)
		})
	}
}

func TestSendRichRecapPartsUsesPlainTextForRemainingPartsAfterFallback(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: can't parse entities: malformed markdown",
		}},
	}

	messages, err := SendRichRecapParts(sender, Config{
		ChatID: 123,
		Parts:  []string{"**first**", "**second**"},
	})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Len(t, sender.richConfigs, 1)
	require.Len(t, sender.plain, 2)
	assert.Equal(t, "first", sender.plain[0].Text)
	assert.Equal(t, "second", sender.plain[1].Text)
	assert.Equal(t, messages[0].MessageID, sender.plain[1].ReplyToMessageID)
}

func TestSendRichRecapPartsSplitsPlainTextFallbackAndRepliesToFirstMessage(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: invalid rich message markdown",
		}},
	}
	rich := "<details><summary>詳細總結</summary>\n\n" + strings.Repeat("界", PlainTextLimit+10) + "\n\n</details>"

	messages, err := SendRichRecapParts(sender, Config{ChatID: 123, Parts: []string{rich}})
	require.NoError(t, err)
	require.Len(t, sender.plain, 2)
	assert.Len(t, messages, 2)
	for _, config := range sender.plain {
		assert.LessOrEqual(t, utf8.RuneCountInString(config.Text), PlainTextLimit)
	}
	assert.Zero(t, sender.plain[0].ReplyToMessageID)
	assert.Equal(t, messages[0].MessageID, sender.plain[1].ReplyToMessageID)
}

func TestRichRecapSummaryFallsBackToCompleteSegmentedPlainText(t *testing.T) {
	sender := &fakeSender{
		richErrors: []error{&tgbotapi.Error{
			Code:    400,
			Message: "Bad Request: invalid rich message markdown",
		}},
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("vote", "vote")),
	)
	summary := chathistories.BuildRichRecapSummary(chathistories.RichRecapSummaryConfig{
		Title:            "TG BOT 測試",
		Hours:            1,
		InitiatorName:    "TestUser(測試...)",
		InitiatorUserID:  42,
		CondensedSummary: "### 測試摘要\n\n- **觸發方式**：/recap",
		CondensedTrace: &openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "condensedModel"},
			Check: openai.ConditionalModelExecutionTrace{
				Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "checkModel"},
			},
		},
		RecapTrace: &openai.RecapExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "detailModel"},
		},
	})
	detail := "## 討論內容\n\n" +
		strings.Repeat("這是需要分段的詳細內容。", 500) +
		"\n\n參考 [1](https://t.me/c/123/45)。"
	parts := chathistories.ComposeRichRecapMessages(summary, []string{detail})

	messages, err := SendRichRecapParts(sender, Config{
		ChatID:           123,
		Parts:            parts,
		ReplyToMessageID: 9,
		ReplyMarkup:      &keyboard,
	})

	require.NoError(t, err)
	require.GreaterOrEqual(t, len(sender.plain), 2)
	require.Len(t, messages, len(sender.plain))
	require.Len(t, sender.richConfigs, 1)

	plainTexts := make([]string, 0, len(sender.plain))
	for index, config := range sender.plain {
		plainTexts = append(plainTexts, config.Text)
		require.LessOrEqual(t, utf8.RuneCountInString(config.Text), PlainTextLimit)
		if index == 0 {
			require.Equal(t, keyboard, config.ReplyMarkup)
			require.Equal(t, 9, config.ReplyToMessageID)
			continue
		}
		require.Nil(t, config.ReplyMarkup)
		require.Equal(t, messages[0].MessageID, config.ReplyToMessageID)
	}

	plain := strings.Join(plainTexts, "\n")
	require.Contains(t, plain, "【TG BOT 測試】聊天回顧")
	require.Contains(t, plain, "用戶 TestUser(測試...) 發起 1 小時總結")
	require.NotContains(t, plain, "tg://user")
	require.Contains(t, plain, "模型資訊")
	require.Contains(t, plain, "Check：checkModel")
	require.NotContains(t, plain, "> ")
	require.Contains(t, plain, "1 (https://t.me/c/123/45)")
}

func TestSplitPlainTextKeepsCitationLabelAndURLTogether(t *testing.T) {
	citation := "1 (https://t.me/c/123/456)"
	text := strings.Repeat("a", PlainTextLimit-10) + " " + citation

	parts := splitPlainText(text)
	require.Len(t, parts, 2)
	assert.NotContains(t, parts[0], "https://")
	assert.Equal(t, citation, parts[1])
	for _, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), PlainTextLimit)
	}
}

func TestRichMarkdownToPlainTextKeepsTelegramUserLabelWithoutTarget(t *testing.T) {
	rich := "# 🧾 聊天回顧\n\n_用戶 [TestUser](tg://user?id=42) 發起 **1 小時**總結_"

	require.Equal(t, "🧾 聊天回顧\n\n用戶 TestUser 發起 1 小時總結", richMarkdownToPlainText(rich))
}
