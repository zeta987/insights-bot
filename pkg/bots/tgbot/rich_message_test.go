package tgbot

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/require"
)

func TestBotSendRichMessageReturnsTelegramMessage(t *testing.T) {
	type capturedRequest struct {
		path   string
		values url.Values
	}

	requests := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		requests <- capturedRequest{path: r.URL.Path, values: r.PostForm}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":42,"date":1710000000,"chat":{"id":-100123,"type":"supergroup"}}}`)
	}))
	t.Cleanup(server.Close)

	botAPI := &tgbotapi.BotAPI{Token: "test-token", Client: server.Client()}
	botAPI.SetAPIEndpoint(server.URL + "/bot%s/%s")
	bot := &Bot{BotAPI: botAPI}

	message, err := bot.SendRichMessage(RichMessageConfig{
		ChatID:   -100123,
		Markdown: "# Recap\n\nRich details",
	})
	require.NoError(t, err)
	require.Equal(t, 42, message.MessageID)
	require.Equal(t, int64(-100123), message.Chat.ID)

	request := <-requests
	require.Equal(t, "/bottest-token/sendRichMessage", request.path)
	require.Equal(t, "-100123", request.values.Get("chat_id"))
	require.JSONEq(t, `{"markdown":"# Recap\n\nRich details"}`, request.values.Get("rich_message"))
}

func TestBotSendRichMessageReturnsTelegramBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`)
	}))
	t.Cleanup(server.Close)

	botAPI := &tgbotapi.BotAPI{Token: "test-token", Client: server.Client()}
	botAPI.SetAPIEndpoint(server.URL + "/bot%s/%s")
	bot := &Bot{BotAPI: botAPI}

	message, err := bot.SendRichMessage(RichMessageConfig{
		ChatID:   -100123,
		Markdown: "invalid markdown",
	})
	require.Error(t, err)
	require.Empty(t, message)

	var telegramError *tgbotapi.Error
	require.ErrorAs(t, err, &telegramError)
	require.Equal(t, http.StatusBadRequest, telegramError.Code)
	require.Equal(t, "Bad Request: can't parse rich message", telegramError.Message)
}

func TestBotSendRichMessageIncludesReplyOptions(t *testing.T) {
	requests := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		requests <- r.PostForm

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":43,"date":1710000000,"chat":{"id":-100123,"type":"supergroup"}}}`)
	}))
	t.Cleanup(server.Close)

	botAPI := &tgbotapi.BotAPI{Token: "test-token", Client: server.Client()}
	botAPI.SetAPIEndpoint(server.URL + "/bot%s/%s")
	bot := &Bot{BotAPI: botAPI}
	replyMarkup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Vote", "vote:up"),
		),
	)

	_, err := bot.SendRichMessage(RichMessageConfig{
		ChatID:   -100123,
		Markdown: "# Recap",
		ReplyParameters: &RichMessageReplyParameters{
			MessageID:                7,
			ChatID:                   -100456,
			AllowSendingWithoutReply: true,
		},
		ReplyMarkup:         &replyMarkup,
		DisableNotification: true,
	})
	require.NoError(t, err)

	values := <-requests
	require.JSONEq(t, `{"message_id":7,"chat_id":-100456,"allow_sending_without_reply":true}`, values.Get("reply_parameters"))
	require.JSONEq(t, `{"inline_keyboard":[[{"text":"Vote","callback_data":"vote:up"}]]}`, values.Get("reply_markup"))
	require.Equal(t, "true", values.Get("disable_notification"))
}

func TestBotServiceSendRichMessageReturnsTelegramMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":44,"date":1710000000,"chat":{"id":-100123,"type":"supergroup"}}}`)
	}))
	t.Cleanup(server.Close)

	botAPI := &tgbotapi.BotAPI{Token: "test-token", Client: server.Client()}
	botAPI.SetAPIEndpoint(server.URL + "/bot%s/%s")
	service := &BotService{BotAPI: botAPI}

	message, err := service.SendRichMessage(RichMessageConfig{
		ChatID:   -100123,
		Markdown: "# Recap",
	})
	require.NoError(t, err)
	require.Equal(t, 44, message.MessageID)
}
