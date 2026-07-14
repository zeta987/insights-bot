package tgbot

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

func TestLiveRichMessageWithCitation(t *testing.T) {
	if os.Getenv("LIVE_TELEGRAM_RICH_MESSAGE_TEST") != "1" {
		t.Skip("set LIVE_TELEGRAM_RICH_MESSAGE_TEST=1 to send the opt-in Telegram smoke test")
	}

	require.NoError(t, godotenv.Load("../../../.env"))
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID, err := strconv.ParseInt(os.Getenv("AUTO_RECAP_TEST_CHAT_ID"), 10, 64)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotZero(t, chatID)

	botAPI, err := tgbotapi.NewBotAPI(token)
	require.NoError(t, err)
	bot := &Bot{BotAPI: botAPI}

	source := tgbotapi.NewMessage(chatID, "Rich Message 本機引用測試來源")
	sourceMessage, err := bot.Send(source)
	require.NoError(t, err)

	chatIDForLink := strings.TrimPrefix(strconv.FormatInt(chatID, 10), "-100")
	markdown := fmt.Sprintf(
		"## Rich Message 本機測試\n\n**濃縮總結**\n新版 recap 會先顯示濃縮內容，再於同一則訊息展開詳細內容。 ✨\n\n<details><summary>詳細總結</summary>\n\n### 引用與格式\n\n- 這一段保留原始 Telegram 訊息引用 [1](https://t.me/c/%s/%d)。\n- 超過限制時會依 Markdown 區塊分段，續段回覆第一則訊息。\n- Rich 格式發生明確 400 時才切換純文字備援。\n\n</details>",
		chatIDForLink,
		sourceMessage.MessageID,
	)

	richMessage, err := bot.SendRichMessage(RichMessageConfig{
		ChatID:   chatID,
		Markdown: markdown,
		ReplyParameters: &RichMessageReplyParameters{
			MessageID:                sourceMessage.MessageID,
			AllowSendingWithoutReply: true,
		},
	})
	require.NoError(t, err)
	require.NotZero(t, richMessage.MessageID)
}
