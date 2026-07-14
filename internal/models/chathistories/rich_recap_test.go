package chathistories

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/stretchr/testify/require"
)

func TestResolveRichRecapReferencesUsesWhitelistedSupergroupMessages(t *testing.T) {
	markdown := "Decision {{tg-ref:1,99,2,1,bad,3,4,5,6}}"
	virtualToReal := map[int64]int64{
		1: 101,
		2: 202,
		3: 303,
		4: 404,
		5: 505,
		6: 606,
	}

	resolved := ResolveRichRecapReferences(
		markdown,
		-100123456789,
		telegram.ChatTypeSuperGroup,
		virtualToReal,
	)

	require.Equal(t, "Decision "+strings.Join([]string{
		"[1](https://t.me/c/123456789/101)",
		"[2](https://t.me/c/123456789/202)",
		"[3](https://t.me/c/123456789/303)",
		"[4](https://t.me/c/123456789/404)",
		"[5](https://t.me/c/123456789/505)",
	}, " "), resolved)
	require.NotContains(t, resolved, "606")
	require.NotContains(t, resolved, "tg-ref")
}

func TestEscapeRichMarkdownTextEscapesUserControlledMarkup(t *testing.T) {
	require.Equal(
		t,
		`Group \[alpha\]\_\*\# next`,
		EscapeRichMarkdownText("Group [alpha]_*#\nnext"),
	)
}

func TestResolveRichRecapReferencesRemovesMarkersWithoutSupergroupLinks(t *testing.T) {
	for _, chatType := range []telegram.ChatType{
		telegram.ChatTypePrivate,
		telegram.ChatTypeGroup,
		telegram.ChatTypeChannel,
	} {
		t.Run(string(chatType), func(t *testing.T) {
			resolved := ResolveRichRecapReferences(
				"Decision {{tg-ref:1}} and unknown {{tg-ref:99,bad}}.",
				-100123456789,
				chatType,
				map[int64]int64{1: 101},
			)

			require.NotContains(t, resolved, "tg-ref")
			require.NotContains(t, resolved, "https://t.me/c/")
		})
	}
}

func TestResolveRichRecapReferencesRemovesEmptyOrUnknownMarkers(t *testing.T) {
	resolved := ResolveRichRecapReferences(
		"Empty {{tg-ref:}} unknown {{tg-ref:7}} end",
		-100123456789,
		telegram.ChatTypeSuperGroup,
		map[int64]int64{1: 101},
	)

	require.NotContains(t, resolved, "tg-ref")
	require.NotContains(t, resolved, "https://t.me/c/")
}

func TestSanitizeDetailedRecapMarkdownAllowsOnlyControlledLinksAndBlocks(t *testing.T) {
	raw := "## Topic\n\n<script>alert</script>\n\n- **Decision** [external](https://example.com) {{tg-ref:1}}\n\n> quoted\n\n```go\ncode()\n```\n\nhttps://example.org/raw"

	sanitized := SanitizeDetailedRecapMarkdown(raw)
	resolved := ResolveRichRecapReferences(
		sanitized,
		-100123456789,
		telegram.ChatTypeSuperGroup,
		map[int64]int64{1: 101},
	)

	require.Contains(t, resolved, "## Topic")
	require.Contains(t, resolved, "- **Decision** external [1](https://t.me/c/123456789/101)")
	require.Contains(t, resolved, "quoted")
	require.Contains(t, resolved, "code()")
	require.NotContains(t, resolved, "<script>")
	require.NotContains(t, resolved, "</script>")
	require.NotContains(t, resolved, "```")
	require.NotContains(t, resolved, "https://example.com")
	require.NotContains(t, resolved, "https://example.org")
}

func TestSanitizeDetailedRecapMarkdownNeutralizesMentions(t *testing.T) {
	raw := "## Topic\n\n" +
		"- @alice and @bob_dev pinged @ChannelName\n" +
		"- (@carol) replied, 感謝@eve 支援\n" +
		"- @@dave escalated\n" +
		"@lead_1 opened the thread\n\n" +
		"contact admin@example.com for access"

	sanitized := SanitizeDetailedRecapMarkdown(raw)

	require.NotContains(t, sanitized, "@alice")
	require.NotContains(t, sanitized, "@bob_dev")
	require.NotContains(t, sanitized, "@ChannelName")
	require.NotContains(t, sanitized, "@carol")
	require.NotContains(t, sanitized, "@eve")
	require.NotContains(t, sanitized, "@dave")
	require.NotContains(t, sanitized, "@lead_1")
	require.Contains(t, sanitized, "- alice and bob_dev pinged ChannelName")
	require.Contains(t, sanitized, "- (carol) replied, 感謝eve 支援")
	require.Contains(t, sanitized, "- dave escalated")
	require.Contains(t, sanitized, "lead_1 opened the thread")
	require.Contains(t, sanitized, "admin@example.com")
}

func TestSanitizeCondensedRecapMarkdownNeutralizesMentions(t *testing.T) {
	raw := "# 主題\n\n" +
		"- **參與者**：@alice、@bob_dev\n" +
		"- 結論由 @@carol 拍板\n\n" +
		"回報請寄 support@example.com"

	sanitized := SanitizeCondensedRecapMarkdown(raw)

	require.NotContains(t, sanitized, "@alice")
	require.NotContains(t, sanitized, "@bob_dev")
	require.NotContains(t, sanitized, "@carol")
	require.Contains(t, sanitized, "- **參與者**：alice、bob_dev")
	require.Contains(t, sanitized, "- 結論由 carol 拍板")
	require.Contains(t, sanitized, "support@example.com")
}

func TestSanitizeCondensedRecapMarkdownPreservesReadableRichStructure(t *testing.T) {
	input := "# 測試主題\n\n- **觸發方式**：/recap\n\n| 指標 | 數值 |\n| --- | --- |\n| 次數 | 4 |\n\n<a href=\"https://evil.example\">補充</a>"

	sanitized := SanitizeCondensedRecapMarkdown(input)

	require.Contains(t, sanitized, "### 測試主題")
	require.Contains(t, sanitized, "- **觸發方式**：/recap")
	require.Contains(t, sanitized, "| 指標 | 數值 |")
	require.Contains(t, sanitized, "| 次數 | 4 |")
	require.Contains(t, sanitized, "補充")
	require.NotContains(t, sanitized, "<a")
	require.NotContains(t, sanitized, "https://evil.example")
}

func TestComposeRichRecapMessagesCombinesCondensedAndDetails(t *testing.T) {
	messages := ComposeRichRecapMessages(
		"濃縮摘要 ✨",
		[]string{"## 主題\n\n詳細內容 [1](https://t.me/c/123/456)"},
	)

	require.Equal(t, []string{
		"濃縮摘要 ✨\n\n<details><summary>詳細總結</summary>\n\n## 主題\n\n詳細內容 [1](https://t.me/c/123/456)\n\n</details>",
	}, messages)
}

func TestComposeRichRecapMessagesSplitsAtMarkdownBlockBoundaries(t *testing.T) {
	firstBlock := "## Topic A\n\n" + strings.Repeat("a", 17000)
	secondBlock := "## Topic B\n\n" + strings.Repeat("b", 17000)
	messages := ComposeRichRecapMessages(
		"Condensed summary",
		[]string{firstBlock + "\n\n" + secondBlock},
	)

	require.Len(t, messages, 2)
	for _, message := range messages {
		require.LessOrEqual(t, utf8.RuneCountInString(message), RichMessageUTF8CharacterLimit)
		require.Equal(t, 1, strings.Count(message, "<details><summary>詳細總結</summary>"))
		require.Equal(t, 1, strings.Count(message, "</details>"))
	}
	require.Contains(t, messages[0], "Condensed summary")
	require.Contains(t, messages[0], "## Topic A")
	require.NotContains(t, messages[0], "## Topic B")
	require.NotContains(t, messages[1], "Condensed summary")
	require.Contains(t, messages[1], "## Topic B")
}

func TestComposeRichRecapMessagesCountsUnicodeCharacters(t *testing.T) {
	messages := ComposeRichRecapMessages(
		"摘要",
		[]string{strings.Repeat("界", RichMessageUTF8CharacterLimit)},
	)

	require.Len(t, messages, 2)
	for _, message := range messages {
		require.LessOrEqual(t, utf8.RuneCountInString(message), RichMessageUTF8CharacterLimit)
		require.Equal(t, 1, strings.Count(message, "<details><summary>詳細總結</summary>"))
		require.Equal(t, 1, strings.Count(message, "</details>"))
	}
}

func TestComposeRichRecapMessagesDoesNotSplitCitationLinks(t *testing.T) {
	citation := "[1](https://t.me/c/123/456)"
	wrapperLength := utf8.RuneCountInString(richRecapDetailsOpen + richRecapDetailsClose)
	condensedPrefixLength := utf8.RuneCountInString("摘要\n\n")
	firstBodyLimit := RichMessageUTF8CharacterLimit - wrapperLength - condensedPrefixLength
	detail := strings.Repeat("a", firstBodyLimit-10) + " " + citation + strings.Repeat("b", 20)

	messages := ComposeRichRecapMessages("摘要", []string{detail})
	require.Len(t, messages, 2)
	require.Equal(t, 1, strings.Count(strings.Join(messages, ""), citation))
	require.NotContains(t, messages[0], "https://")
	require.Contains(t, messages[1], citation)
	for _, message := range messages {
		require.LessOrEqual(t, utf8.RuneCountInString(message), RichMessageUTF8CharacterLimit)
	}
}

func TestComposeRichRecapMessagesHandlesMissingSections(t *testing.T) {
	require.Equal(t, []string{"Only condensed"}, ComposeRichRecapMessages("Only condensed", nil))
	require.Empty(t, ComposeRichRecapMessages("", nil))
}

func TestComposeRichRecapMessagesSeparatesOversizedCondensedSummary(t *testing.T) {
	messages := ComposeRichRecapMessages(
		strings.Repeat("濃", RichMessageUTF8CharacterLimit+1),
		[]string{"Detailed summary"},
	)

	require.Len(t, messages, 3)
	require.NotContains(t, messages[0], "<details>")
	require.NotContains(t, messages[1], "<details>")
	require.Contains(t, messages[2], "<details><summary>詳細總結</summary>")
	for _, message := range messages {
		require.LessOrEqual(t, utf8.RuneCountInString(message), RichMessageUTF8CharacterLimit)
	}
}
