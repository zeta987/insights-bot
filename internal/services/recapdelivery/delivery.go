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

// PlainTextLimit is Telegram's sendMessage text limit, measured in UTF-16
// code units as the Bot API counts message length.
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
// parts to the first delivered message. Telegram formatting-specific and
// message-too-long 400 errors fall back to split plain text; transport and
// server errors do not.
func SendRichRecapParts(sender Sender, config Config) ([]tgbotapi.Message, error) {
	messages := make([]tgbotapi.Message, 0, len(config.Parts))
	plainMode := false

	for index, part := range config.Parts {
		var fallbackErr error
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
			if !isRichMessageFormattingError(err) && !isMessageTooLongError(err) {
				return messages, fmt.Errorf("send rich recap part %d: %w", index+1, err)
			}

			fallbackErr = err
			plainMode = true
		}

		plainParts := splitPlainText(richMarkdownToPlainText(part))
		if len(plainParts) == 0 {
			if fallbackErr != nil {
				return messages, fmt.Errorf("rich recap part %d has no plain-text fallback: %w", index+1, fallbackErr)
			}
			return messages, fmt.Errorf("rich recap part %d has no plain-text fallback", index+1)
		}
		for plainIndex, plainPart := range plainParts {
			withMarkup := index == 0 && plainIndex == 0
			if err := sendPlainRecapPart(sender, config, &messages, plainPart, PlainTextLimit, withMarkup, index, plainIndex); err != nil {
				return messages, err
			}
		}
	}

	return messages, nil
}

// sendPlainRecapPart delivers one plain-text chunk. When Telegram still
// rejects the chunk as too long, the chunk is re-split with a halved UTF-16
// budget and each piece is retried.
func sendPlainRecapPart(
	sender Sender,
	config Config,
	messages *[]tgbotapi.Message,
	text string,
	limit int,
	withMarkup bool,
	partIndex int,
	plainIndex int,
) error {
	plainMessage := tgbotapi.NewMessage(config.ChatID, text)
	plainMessage.DisableNotification = config.DisableNotification
	plainMessage.AllowSendingWithoutReply = config.AllowSendingWithoutReply
	plainMessage.ReplyToMessageID = continuationReplyID(*messages, config.ReplyToMessageID)
	if withMarkup && config.ReplyMarkup != nil {
		plainMessage.ReplyMarkup = *config.ReplyMarkup
	}

	invokeBeforeSend(config.BeforeSend)
	sent, sendErr := sender.Send(plainMessage)
	if sendErr == nil {
		*messages = append(*messages, sent)
		return nil
	}

	halvedLimit := limit / 2
	if !isMessageTooLongError(sendErr) || halvedLimit < 1 {
		return fmt.Errorf("send plain recap fallback part %d.%d: %w", partIndex+1, plainIndex+1, sendErr)
	}

	pieces := splitPlainTextWithLimit(text, halvedLimit)
	if len(pieces) < 2 {
		return fmt.Errorf("send plain recap fallback part %d.%d: %w", partIndex+1, plainIndex+1, sendErr)
	}
	for pieceIndex, piece := range pieces {
		err := sendPlainRecapPart(sender, config, messages, piece, halvedLimit, withMarkup && pieceIndex == 0, partIndex, plainIndex)
		if err != nil {
			return err
		}
	}

	return nil
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

func isMessageTooLongError(err error) bool {
	var telegramError *tgbotapi.Error
	if !errors.As(err, &telegramError) || telegramError.Code != 400 {
		return false
	}

	return strings.Contains(strings.ToLower(telegramError.Message), "message is too long")
}

func isRichMessageFormattingError(err error) bool {
	var telegramError *tgbotapi.Error
	if !errors.As(err, &telegramError) || telegramError.Code != 400 {
		return false
	}

	message := strings.ToLower(telegramError.Message)
	// Parse failures that point at request payload fields (for example
	// reply_markup JSON) are application bugs; degrading them to plain text
	// would hide the defect instead of surfacing it.
	for _, payloadSignature := range []string{
		"reply markup",
		"reply_markup",
		"reply parameters",
		"reply_parameters",
	} {
		if strings.Contains(message, payloadSignature) {
			return false
		}
	}

	for _, signature := range []string{
		"can't parse entities",
		"cannot parse entities",
		"can't find end of the entity",
		"unsupported start tag",
		"can't parse markdown",
		"cannot parse markdown",
		"failed to parse markdown",
		"invalid markdown",
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
	return splitPlainTextWithLimit(text, PlainTextLimit)
}

func splitPlainTextWithLimit(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return []string{}
	}

	parts := make([]string, 0, tgbot.MessageLengthInUTF16Units(text)/limit+1)
	remaining := []rune(text)
	for {
		runeLimit := tgbot.RuneCountWithinUTF16Units(remaining, limit)
		if runeLimit >= len(remaining) {
			break
		}
		if runeLimit < 1 {
			// Never stall on a single rune wider than the remaining budget.
			runeLimit = 1
		}

		cut := preferredPlainTextCut(remaining, runeLimit)
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
