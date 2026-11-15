package chathistories

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

var condensedSummaryLabelPattern = regexp.MustCompile(`(?i)^\s*(?:\*\*)?(?:濃縮總結|浓缩总结)(?:\*\*)?\s*[：:]?\s*(?:🤖(?:️)?\s*)?`)

type RichRecapSummaryConfig struct {
	Title                 string
	Hours                 int
	Automatic             bool
	InitiatorName         string
	InitiatorUserID       int64
	CondensedSummary      string
	GeneralGroupNotice    bool
	SubscriptionChatTitle string
	CondensedTrace        *openai.CondensedExecutionTrace
	RecapTrace            *openai.RecapExecutionTrace
	// RecapReuse adds a model-info line when earlier recaps were merged.
	RecapReuse *RecapReuse
}

func richRecapHeading(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "# 聊天回顧"
	}
	if strings.HasPrefix(title, "【") && strings.HasSuffix(title, "】") {
		return "# " + EscapeRichMarkdownText(title) + "聊天回顧"
	}
	return "# 【" + EscapeRichMarkdownText(title) + "】聊天回顧"
}

func richRecapUserMention(name string, userID int64) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未知用戶"
	}
	escapedName := EscapeRichMarkdownText(name)
	if userID <= 0 {
		return escapedName
	}
	return fmt.Sprintf("[%s](tg://user?id=%d)", escapedName, userID)
}

func richRecapMetadata(config RichRecapSummaryConfig) string {
	if config.Automatic {
		if config.Hours > 0 {
			return fmt.Sprintf("_自動產生 **%d 小時**總結_", config.Hours)
		}
		return "_自動產生_"
	}

	if strings.TrimSpace(config.InitiatorName) == "" && config.InitiatorUserID <= 0 {
		return ""
	}
	initiator := richRecapUserMention(config.InitiatorName, config.InitiatorUserID)
	if config.Hours > 0 {
		return fmt.Sprintf("_用戶 %s 發起 **%d 小時**總結_", initiator, config.Hours)
	}
	return fmt.Sprintf("_由用戶 %s 發起_", initiator)
}

func cleanCondensedSummaryForDisplay(summary string) string {
	summary = strings.TrimSpace(summary)
	cleaned := strings.TrimSpace(condensedSummaryLabelPattern.ReplaceAllString(summary, ""))
	if cleaned == "" {
		return summary
	}
	return cleaned
}

// condensedSummaryFallbackUnitLimit caps the derived condensed summary,
// measured in UTF-16 code units like every Telegram-facing length limit.
const condensedSummaryFallbackUnitLimit = 120

// FallbackCondensedSummary derives a condensed summary from the detailed
// summarizations when sarcastic condensed generation fails. The joined text is
// truncated without splitting citation links or emphasis spans, and
// defaultSummary is used when nothing readable remains.
func FallbackCondensedSummary(summarizations []string, defaultSummary string) string {
	joined := strings.TrimSpace(strings.Join(summarizations, " "))
	head, _ := splitFragmentAtPreferredCut(joined, condensedSummaryFallbackUnitLimit, utf16UnitBudget, richMarkdownProtectedSpanPatterns)

	head = strings.TrimSpace(head)
	if head == "" {
		return strings.TrimSpace(defaultSummary)
	}

	return head
}

// BuildRichRecapSummary creates the always-visible Rich Markdown prefix shared
// by manual, automatic, subscribed, and forwarded recaps.
func BuildRichRecapSummary(config RichRecapSummaryConfig) string {
	blocks := []string{richRecapHeading(config.Title)}
	if metadata := richRecapMetadata(config); metadata != "" {
		blocks = append(blocks, metadata)
	}
	if strings.TrimSpace(config.SubscriptionChatTitle) != "" {
		blocks = append(blocks, "> 📬 這是您訂閱的 **"+
			EscapeRichMarkdownText(config.SubscriptionChatTitle)+"** 群組定時聊天回顧。")
	}

	blocks = append(blocks, "## 濃縮總結")
	if condensed := SanitizeCondensedRecapMarkdown(cleanCondensedSummaryForDisplay(config.CondensedSummary)); condensed != "" {
		blocks = append(blocks, condensed)
	}
	if config.GeneralGroupNotice {
		blocks = append(blocks, "> 💡 一般群組來源暫時不顯示原訊息引用；升級為 supergroup 後即可建立連結。")
	}
	modelInfo := BuildRichRecapModelInfo(config.CondensedTrace, config.RecapTrace)

	if reuseLine := richRecapReuseLine(config.RecapReuse); reuseLine != "" {
		modelInfo += "\n" + reuseLine
	}

	blocks = append(blocks, "---", modelInfo)

	return strings.Join(blocks, "\n\n")
}

// richRecapReuseLine reports how many earlier recaps were merged, how long
// ago the newest of them ended, and how many messages were summarized anew.
func richRecapReuseLine(reuse *RecapReuse) string {
	if reuse == nil || reuse.ReusedRecaps <= 0 {
		return ""
	}

	summarized := "沒有其他訊息需要摘要"
	if reuse.SummarizedMessages > 0 {
		summarized = fmt.Sprintf("另摘要 %d 則訊息", reuse.SummarizedMessages)
	}

	return fmt.Sprintf("> - 沿用回顧：%d 份（最近一份涵蓋到%s），%s",
		reuse.ReusedRecaps,
		formatRecapReuseAge(reuse.LastReusedMessageAge),
		summarized,
	)
}

func formatRecapReuseAge(age time.Duration) string {
	minutes := int(age / time.Minute)

	switch {
	case minutes < 1:
		return "剛剛"
	case minutes < 60:
		return fmt.Sprintf(" %d 分鐘前", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf(" %d 小時前", minutes/60)
	default:
		return fmt.Sprintf(" %d 小時 %d 分前", minutes/60, minutes%60)
	}
}
