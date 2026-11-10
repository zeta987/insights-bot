package chathistories

import (
	"regexp"
	"strings"

	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
)

var (
	richMarkdownDetailsOpenPattern = regexp.MustCompile(`(?i)<details>\s*<summary>([^<]*)</summary>\s*`)
	richMarkdownURLLinkPattern     = regexp.MustCompile(`\[([^\]\r\n]+)\]\((https?://[^)\s]+)\)`)
	richMarkdownUserLinkPattern    = regexp.MustCompile(`\[((?:\\.|[^\]\r\n])*)\]\(tg://user\?id=\d+\)`)
	richMarkdownHeadingPattern     = regexp.MustCompile(`(?m)^#{1,6}[ \t]+`)
	richMarkdownQuotePattern       = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`)
	plainTextCitationPattern       = regexp.MustCompile(`\S+[ \t]+\(https?://[^)\s]+\)`)
	plainTextProtectedSpanPatterns = []*regexp.Regexp{plainTextCitationPattern}
)

// RichMarkdownToPlainText converts application-generated Rich Markdown into
// plain text for surfaces that render no markup, such as the plain-text
// delivery fallback.
func RichMarkdownToPlainText(markdown string) string {
	plain := strings.ReplaceAll(markdown, "\r\n", "\n")
	plain = richMarkdownDetailsOpenPattern.ReplaceAllString(plain, "$1\n\n")
	plain = strings.ReplaceAll(plain, "</details>", "")
	plain = richMarkdownUserLinkPattern.ReplaceAllString(plain, "$1")
	plain = richMarkdownURLLinkPattern.ReplaceAllString(plain, "$1 ($2)")
	plain = richMarkdownHeadingPattern.ReplaceAllString(plain, "")
	plain = richMarkdownQuotePattern.ReplaceAllString(plain, "")
	plain = strings.ReplaceAll(plain, "```", "")
	plain = strings.ReplaceAll(plain, "~~~", "")

	for _, pattern := range richMarkdownInlineSpanPatterns {
		plain = pattern.ReplaceAllString(plain, "$1")
	}

	return strings.TrimSpace(UnescapeRichMarkdownText(plain))
}

// SplitPlainText splits plain text into parts of at most limit UTF-16 code
// units (the unit Telegram uses to measure message length), preferring
// sentence boundaries and keeping citation labels attached to their URLs.
func SplitPlainText(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return []string{}
	}

	parts := make([]string, 0, tgbot.MessageLengthInUTF16Units(text)/limit+1)
	remaining := text

	for tgbot.MessageLengthInUTF16Units(remaining) > limit {
		head, tail := splitFragmentAtPreferredCut(remaining, limit, utf16UnitBudget, plainTextProtectedSpanPatterns)
		if head == "" {
			break
		}

		parts = append(parts, head)
		remaining = tail
	}

	if remaining != "" {
		parts = append(parts, remaining)
	}

	return parts
}
