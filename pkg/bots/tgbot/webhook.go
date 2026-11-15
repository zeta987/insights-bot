package tgbot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/samber/lo"
)

func newWebhookServer(patternPath, port string, bot *tgbotapi.BotAPI, updateChan chan<- tgbotapi.Update) *http.Server {
	srv := http.NewServeMux()
	srv.HandleFunc(patternPath+"/"+bot.Token, func(w http.ResponseWriter, r *http.Request) {
		update, err := decodeWebhookUpdate(r)
		if err != nil {
			errMsg, _ := json.Marshal(map[string]string{"error": err.Error()})

			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(errMsg)

			return
		}

		updateChan <- *update
	})

	return &http.Server{
		Addr:              net.JoinHostPort("", lo.Ternary(port == "", "7071", port)),
		ReadTimeout:       time.Second * 15,
		ReadHeaderTimeout: time.Second * 15,
		Handler:           srv,
	}
}

// maxWebhookBodyBytes bounds one update body; Telegram updates are a few
// kilobytes even with long messages.
const maxWebhookBodyBytes = 4 << 20

// decodeWebhookUpdate does what BotAPI.HandleUpdate does, but keeps the raw
// body so PatchUpdate can recover fields the library does not decode.
func decodeWebhookUpdate(r *http.Request) (*tgbotapi.Update, error) {
	if r.Method != http.MethodPost {
		return nil, errors.New("wrong HTTP method required POST")
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		return nil, err
	}

	var update tgbotapi.Update
	if err := json.Unmarshal(body, &update); err != nil {
		return nil, err
	}

	PatchUpdate(&update, body)

	return &update, nil
}

func setWebhook(webhookURL string, bot *tgbotapi.BotAPI) error {
	webhookConfig, err := tgbotapi.NewWebhook(webhookURL + "/" + bot.Token)
	if err != nil {
		return fmt.Errorf("failed to create webhook config: %w", err)
	}

	_, err = bot.Request(webhookConfig)
	if err != nil {
		return fmt.Errorf("failed to set webhook: %w", err)
	}

	return nil
}
