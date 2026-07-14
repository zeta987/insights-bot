package chathistories

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/stretchr/testify/require"
)

func TestBuildRichRecapSummaryUsesClickableInitiatorAndClearHierarchy(t *testing.T) {
	summary := BuildRichRecapSummary(RichRecapSummaryConfig{
		Title:            "TG BOT [測試]",
		Hours:            1,
		InitiatorName:    "TestUser(測試)",
		InitiatorUserID:  42,
		CondensedSummary: "濃縮總結 🤖 ### 測試主題\n\n- **觸發方式**：/recap",
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

	require.Equal(t, "# 【TG BOT \\[測試\\]】聊天回顧\n\n"+
		"_用戶 [TestUser\\(測試\\)](tg://user?id=42) 發起 **1 小時**總結_\n\n"+
		"## 濃縮總結\n\n"+
		"### 測試主題\n\n"+
		"- **觸發方式**：/recap\n\n"+
		"---\n\n"+
		"> **模型資訊**\n>\n"+
		"> - 濃縮總結：condensedModel\n"+
		"> - 詳細總結：detailModel\n"+
		"> - Check：checkModel", summary)
}

func TestBuildRichRecapSummaryAddsAutomaticAndSubscriptionNotices(t *testing.T) {
	summary := BuildRichRecapSummary(RichRecapSummaryConfig{
		Title:                 "TG BOT 測試",
		Hours:                 4,
		Automatic:             true,
		CondensedSummary:      "摘要",
		GeneralGroupNotice:    true,
		SubscriptionChatTitle: "TG BOT 測試",
	})

	require.Contains(t, summary, "_自動產生 **4 小時**總結_")
	require.Contains(t, summary, "# 【TG BOT 測試】聊天回顧")
	require.Contains(t, summary, "> 📬 這是您訂閱的 **TG BOT 測試** 群組定時聊天回顧。")
	require.Contains(t, summary, "> 💡 一般群組來源暫時不顯示原訊息引用；升級為 supergroup 後即可建立連結。")
	require.NotContains(t, summary, "#recap")
}

func TestRichRecapHeadingPreservesCompleteGroupName(t *testing.T) {
	require.Equal(t, "# 【每日聊天回顧】聊天回顧", richRecapHeading("每日聊天回顧"))
	require.Equal(t, "# 【聊天回顧】聊天回顧", richRecapHeading("聊天回顧"))
}

func TestFallbackCondensedSummaryKeepsShortJoinedSummarizations(t *testing.T) {
	result := FallbackCondensedSummary([]string{"第一段總結", "第二段總結"}, "預設摘要")
	require.Equal(t, "第一段總結 第二段總結", result)
}

func TestFallbackCondensedSummaryUsesDefaultWhenSummarizationsAreBlank(t *testing.T) {
	require.Equal(t, "過去 4 小時的群組聊天回顧", FallbackCondensedSummary([]string{"   ", "\n\t"}, "過去 4 小時的群組聊天回顧"))
	require.Equal(t, "預設摘要", FallbackCondensedSummary(nil, "預設摘要"))
}

func TestFallbackCondensedSummaryDoesNotSliceCitationLinks(t *testing.T) {
	prefix := strings.Repeat("聊", 100)
	citation := "[1](https://t.me/c/1234567890/12345)"

	result := FallbackCondensedSummary([]string{prefix + citation}, "預設摘要")

	require.Equal(t, prefix, result)
	require.NotContains(t, result, "[1](")
	require.LessOrEqual(t, utf8.RuneCountInString(result), 120)
}

func TestFallbackCondensedSummaryDoesNotSliceEmphasisSpans(t *testing.T) {
	prefix := strings.Repeat("聊", 115)

	result := FallbackCondensedSummary([]string{prefix + "**重點內容**"}, "預設摘要")

	require.Equal(t, prefix, result)
	require.NotContains(t, result, "*")
}

func TestFallbackCondensedSummaryTruncatesLongTextAtNaturalBreak(t *testing.T) {
	first := strings.Repeat("很", 80) + "。"
	second := strings.Repeat("多", 80)

	result := FallbackCondensedSummary([]string{first, second}, "預設摘要")

	require.Equal(t, first, result)
	require.LessOrEqual(t, utf8.RuneCountInString(result), 120)
}
