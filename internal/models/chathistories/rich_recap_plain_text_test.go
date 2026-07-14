package chathistories

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
)

func TestSplitPlainTextKeepsCitationLabelAndURLTogether(t *testing.T) {
	const limit = tgbot.MessageLengthLimit

	citation := "1 (https://t.me/c/123/456)"
	text := strings.Repeat("a", limit-10) + " " + citation

	parts := SplitPlainText(text, limit)
	require.Len(t, parts, 2)
	assert.NotContains(t, parts[0], "https://")
	assert.Equal(t, citation, parts[1])

	for _, part := range parts {
		assert.LessOrEqual(t, tgbot.MessageLengthInUTF16Units(part), limit)
	}
}

func TestSplitPlainTextMeasuresLimitInUTF16CodeUnits(t *testing.T) {
	// 🧾 is one rune but two UTF-16 code units: rune counting would keep all
	// of them in one chunk that Telegram rejects as too long.
	text := strings.Repeat("🧾", tgbot.MessageLengthLimit/2+1)

	parts := SplitPlainText(text, tgbot.MessageLengthLimit)

	require.Len(t, parts, 2)
	for _, part := range parts {
		require.LessOrEqual(t, tgbot.MessageLengthInUTF16Units(part), tgbot.MessageLengthLimit)
	}
	require.Equal(t, text, strings.Join(parts, ""))
}

func TestSplitPlainTextKeepsExactUTF16BoundaryInOnePart(t *testing.T) {
	text := strings.Repeat("🧾", tgbot.MessageLengthLimit/2)

	parts := SplitPlainText(text, tgbot.MessageLengthLimit)

	require.Equal(t, []string{text}, parts)
}

func TestSplitPlainTextKeepsLeadingCitationWholeWhenItFits(t *testing.T) {
	citation := strings.Repeat("x", 32) + " (https://t.me/c/123/456)"
	text := citation + strings.Repeat("y", 20)

	require.Equal(t, []string{citation, strings.Repeat("y", 20)}, SplitPlainText(text, 60))
}

func TestRichMarkdownToPlainTextKeepsTelegramUserLabelWithoutTarget(t *testing.T) {
	rich := "# 🧾 聊天回顧\n\n_用戶 [TestUser](tg://user?id=42) 發起 **1 小時**總結_"

	require.Equal(t, "🧾 聊天回顧\n\n用戶 TestUser 發起 1 小時總結", RichMarkdownToPlainText(rich))
}

func TestUnescapeRichMarkdownTextReversesEscapeForEveryEscapableCharacter(t *testing.T) {
	original := "plain " + richMarkdownEscapableCharacters + " text"

	require.Equal(t, original, UnescapeRichMarkdownText(EscapeRichMarkdownText(original)))
}
