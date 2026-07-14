package recapdelivery

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
)

// PlainTextLimit is Telegram's sendMessage text limit.
const PlainTextLimit = 4096

var (
	detailsOpenPattern      = regexp.MustCompile(`(?i)<details>\s*<summary>([^<]*)</summary>\s*`)
	markdownLinkPattern     = regexp.MustCompile(`\[([^\]\r\n]+)\]\((https?://[^)\s]+)\)`)
	telegramUserLinkPattern = regexp.MustCompile(`\[((?:\\.|[^\]\r\n])*)\]\(tg://user\?id=\d+\)`)
	markdownHeadingPattern  = regexp.MustCompile(`(?m)^#{1,6}[ \t]+`)
	markdownQuotePattern    = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`)
	plainCitationPattern    = regexp.MustCompile(`\S+[ \t]+\(https?://[^)\s]+\)`)
	markdownInlinePatterns  = []*regexp.Regexp{
		regexp.MustCompile(`\*\*((?:\\.|[^*\r\n])*)\*\*`),
		regexp.MustCompile(`__((?:\\.|[^_\r\n])*)__`),
		regexp.MustCompile(`~~((?:\\.|[^~\r\n])*)~~`),
		regexp.MustCompile("`([^`\\r\\n]*)`"),
		regexp.MustCompile(`\*((?:\\.|[^*\r\n])*)\*`),
		regexp.MustCompile(`_((?:\\.|[^_\r\n])*)_`),
	}
)

// Sender is the Telegram surface needed to deliver a rich recap and its
// deterministic plain-text fallback.
type Sender interface {
	SendRichMessage(config tgbot.RichMessageConfig) (tgbotapi.Message, error)
	Send(chattable tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Config controls delivery metadata shared by every recap part.
type Config struct {
	ChatID                   int64
	Parts                    []string
	ReplyToMessageID         int
	ReplyMarkup              *tgbotapi.InlineKeyboardMarkup
	DisableNotification      bool
	AllowSendingWithoutReply bool
	BeforeSend               func()
}

// SendRichRecapParts sends every Rich Markdown part and replies continuation
// parts to the first delivered message. Telegram formatting-specific 400
// errors fall back to split plain text; transport and server errors do not.
func SendRichRecapParts(sender Sender, config Config) ([]tgbotapi.Message, error) {
	messages := make([]tgbotapi.Message, 0, len(config.Parts))
	plainMode := false

	for index, part := range config.Parts {
		var formattingErr error
		if !plainMode {
			replyToMessageID := continuationReplyID(messages, config.ReplyToMessageID)
			richConfig := tgbot.RichMessageConfig{
				ChatID:              config.ChatID,
				Markdown:            part,
				DisableNotification: config.DisableNotification,
			}
			if replyToMessageID != 0 {
				richConfig.ReplyParameters = &tgbot.RichMessageReplyParameters{
					MessageID:                replyToMessageID,
					AllowSendingWithoutReply: config.AllowSendingWithoutReply,
				}
			}
			if index == 0 {
				richConfig.ReplyMarkup = config.ReplyMarkup
			}

			invokeBeforeSend(config.BeforeSend)
			message, err := sender.SendRichMessage(richConfig)
			if err == nil {
				messages = append(messages, message)
				continue
			}
			if !isRichMessageFormattingError(err) {
				return messages, fmt.Errorf("send rich recap part %d: %w", index+1, err)
			}

			formattingErr = err
			plainMode = true
		}

		plainParts := splitPlainText(richMarkdownToPlainText(part))
		if len(plainParts) == 0 {
			if formattingErr != nil {
				return messages, fmt.Errorf("rich recap part %d has no plain-text fallback: %w", index+1, formattingErr)
			}
			return messages, fmt.Errorf("rich recap part %d has no plain-text fallback", index+1)
		}
		for plainIndex, plainPart := range plainParts {
			plainMessage := tgbotapi.NewMessage(config.ChatID, plainPart)
			plainMessage.DisableNotification = config.DisableNotification
			plainMessage.AllowSendingWithoutReply = config.AllowSendingWithoutReply
			plainMessage.ReplyToMessageID = continuationReplyID(messages, config.ReplyToMessageID)
			if index == 0 && plainIndex == 0 && config.ReplyMarkup != nil {
				plainMessage.ReplyMarkup = *config.ReplyMarkup
			}

			invokeBeforeSend(config.BeforeSend)
			sent, sendErr := sender.Send(plainMessage)
			if sendErr != nil {
				return messages, fmt.Errorf("send plain recap fallback part %d.%d: %w", index+1, plainIndex+1, sendErr)
			}
			messages = append(messages, sent)
		}
	}

	return messages, nil
}

func invokeBeforeSend(beforeSend func()) {
	if beforeSend != nil {
		beforeSend()
	}
}

func continuationReplyID(messages []tgbotapi.Message, initialReplyID int) int {
	if len(messages) != 0 {
		return messages[0].MessageID
	}
	return initialReplyID
}

func isRichMessageFormattingError(err error) bool {
	var telegramError *tgbotapi.Error
	if !errors.As(err, &telegramError) || telegramError.Code != 400 {
		return false
	}

	message := strings.ToLower(telegramError.Message)
	for _, signature := range []string{
		"can't parse entities",
		"cannot parse entities",
		"failed to parse",
		"parse error",
		"can't find end of the entity",
		"unsupported start tag",
		"markdown",
	} {
		if strings.Contains(message, signature) {
			return true
		}
	}

	richContext := strings.Contains(message, "rich message") ||
		strings.Contains(message, "rich_message") ||
		strings.Contains(message, "inputrichmessage")
	if !richContext {
		return false
	}
	for _, signature := range []string{
		"can't parse rich message",
		"cannot parse rich message",
		"failed to parse rich message",
		"invalid rich message",
		"rich message is invalid",
		"rich message format",
		"rich message block",
		"rich message nesting",
		"rich message is too long",
	} {
		if strings.Contains(message, signature) {
			return true
		}
	}
	return false
}

func richMarkdownToPlainText(markdown string) string {
	plain := strings.ReplaceAll(markdown, "\r\n", "\n")
	plain = detailsOpenPattern.ReplaceAllString(plain, "$1\n\n")
	plain = strings.ReplaceAll(plain, "</details>", "")
	plain = telegramUserLinkPattern.ReplaceAllString(plain, "$1")
	plain = markdownLinkPattern.ReplaceAllString(plain, "$1 ($2)")
	plain = markdownHeadingPattern.ReplaceAllString(plain, "")
	plain = markdownQuotePattern.ReplaceAllString(plain, "")
	plain = strings.ReplaceAll(plain, "```", "")
	plain = strings.ReplaceAll(plain, "~~~", "")
	for _, pattern := range markdownInlinePatterns {
		plain = pattern.ReplaceAllString(plain, "$1")
	}
	plain = unescapeRichMarkdownText(plain)
	return strings.TrimSpace(plain)
}

func unescapeRichMarkdownText(text string) string {
	runes := []rune(text)
	var builder strings.Builder
	builder.Grow(len(text))

	for index := 0; index < len(runes); index++ {
		if runes[index] == '\\' && index+1 < len(runes) &&
			strings.ContainsRune("\\*_{}[]()#+-.!|><~`", runes[index+1]) {
			index++
		}
		builder.WriteRune(runes[index])
	}

	return builder.String()
}

func splitPlainText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{}
	}

	parts := make([]string, 0, utf8.RuneCountInString(text)/PlainTextLimit+1)
	remaining := []rune(text)
	for len(remaining) > PlainTextLimit {
		cut := preferredPlainTextCut(remaining, PlainTextLimit)
		part := strings.TrimSpace(string(remaining[:cut]))
		if part != "" {
			parts = append(parts, part)
		}
		remaining = []rune(strings.TrimSpace(string(remaining[cut:])))
	}
	if tail := strings.TrimSpace(string(remaining)); tail != "" {
		parts = append(parts, tail)
	}

	return parts
}

func preferredPlainTextCut(runes []rune, limit int) int {
	cut := limit
	for index := limit - 1; index >= limit/2; index-- {
		switch runes[index] {
		case '\n', ' ', '\t', '。', '！', '？', '；':
			cut = index + 1
			index = -1
		}
	}

	adjusted := adjustCutBeforePattern(string(runes), cut, plainCitationPattern)
	if adjusted > 0 {
		return adjusted
	}
	return cut
}

func adjustCutBeforePattern(text string, cut int, pattern *regexp.Regexp) int {
	adjusted := cut
	for _, match := range pattern.FindAllStringIndex(text, -1) {
		start := utf8.RuneCountInString(text[:match[0]])
		end := start + utf8.RuneCountInString(text[match[0]:match[1]])
		if start < adjusted && adjusted < end && start > 0 {
			adjusted = start
		}
	}
	return adjusted
}
