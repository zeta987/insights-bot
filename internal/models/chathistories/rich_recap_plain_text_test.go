package chathistories

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitPlainTextKeepsCitationLabelAndURLTogether(t *testing.T) {
	const limit = 4096

	citation := "1 (https://t.me/c/123/456)"
	text := strings.Repeat("a", limit-10) + " " + citation

	parts := SplitPlainText(text, limit)
	require.Len(t, parts, 2)
	assert.NotContains(t, parts[0], "https://")
	assert.Equal(t, citation, parts[1])

	for _, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), limit)
	}
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
