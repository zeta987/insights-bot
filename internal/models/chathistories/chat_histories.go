package chathistories

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"entgo.io/ent/dialect/sql"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/samber/lo"
	lop "github.com/samber/lo/parallel"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/ent"
	"github.com/nekomeowww/insights-bot/ent/chathistories"
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/linkprev"
	"github.com/nekomeowww/insights-bot/pkg/logger"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
)

type FromPlatform int

const (
	FromPlatformTelegram FromPlatform = iota
)

type RecapType int

const (
	RecapTypeForGroup RecapType = iota
	RecapTypeForPrivateForwarded
)

type NewModelParams struct {
	fx.In

	Lifecycle fx.Lifecycle

	Config *configs.Config
	Logger *logger.Logger
	Ent    *datastore.Ent
	OpenAI openai.Client
	Redis  *datastore.Redis
}

type Model struct {
	config   *configs.Config
	logger   *logger.Logger
	ent      *datastore.Ent
	openAI   openai.Client
	linkprev *linkprev.Client
	redis    *datastore.Redis
	// media is nil unless media understanding is enabled.
	media *mediaEnricher
}

func NewModel() func(NewModelParams) (*Model, error) {
	return func(param NewModelParams) (*Model, error) {
		model := &Model{
			config:   param.Config,
			logger:   param.Logger,
			ent:      param.Ent,
			openAI:   param.OpenAI,
			linkprev: linkprev.NewClient(),
			redis:    param.Redis,
		}

		if model.mediaEnabled() {
			model.media = newMediaEnricher(model)

			if param.Lifecycle != nil {
				param.Lifecycle.Append(fx.Hook{
					OnStart: func(context.Context) error {
						model.media.start()
						return nil
					},
					OnStop: model.media.stop,
				})
			}
		}

		return model, nil
	}
}

// SetTelegramFileSource gives media enrichment the bot client it downloads
// files and looks up custom emoji with. The bot is built after this model, so
// it cannot be injected.
func (m *Model) SetTelegramFileSource(source TelegramFileSource) {
	if m.media != nil {
		m.media.setSource(source)
	}
}

func (m *Model) ExtractTextFromMessage(message *tgbotapi.Message) string {
	text := lo.Ternary(message.Caption != "", message.Caption, message.Text)
	entities := messageTextEntities(message)
	customEmojiLabels := m.cachedCustomEmojiLabels(messageChatID(message), entities)

	type MarkdownLink struct {
		Markdown []uint16
		Start    int
		End      int
	}

	textUTF16 := utf16.Encode([]rune(text))
	links := lop.Map(entities, func(entity tgbotapi.MessageEntity, i int) MarkdownLink {
		startIndex := entity.Offset
		endIndex := startIndex + entity.Length
		var title string
		var href string

		switch entity.Type {
		case "custom_emoji":
			// Keep the fallback emoji and add what the custom emoji shows.
			label := customEmojiLabels[tgbot.CustomEmojiID(entity)]
			if label == "" || startIndex < 0 || endIndex > len(textUTF16) {
				return MarkdownLink{[]uint16{}, -1, -1}
			}

			fallback := string(utf16.Decode(textUTF16[startIndex:endIndex]))

			return MarkdownLink{utf16.Encode([]rune(fallback + "〔" + label + "〕")), startIndex, endIndex}
		case "url":
			href = string(utf16.Decode(textUTF16[startIndex:endIndex]))

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			meta, err := m.linkprev.Preview(ctx, href)
			if err != nil {
				m.logger.Error("🔗Failed to generate link preview", zap.String("url", href), zap.Error(err))
				return MarkdownLink{[]uint16{}, -1, -1}
			}

			title = lo.Ternary(meta.Title != "", meta.Title, meta.OpenGraph.Title)
		case "text_link":
			href = entity.URL
			title = string(utf16.Decode(textUTF16[startIndex:endIndex]))
		default:
			return MarkdownLink{[]uint16{}, -1, -1}
		}

		if utf8.RuneCountInString(title) > 200 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			resp, err := m.openAI.SummarizeAny(ctx, title)
			if err != nil {
				m.logger.Error("🔗Failed to summarize title", zap.String("url", href), zap.Error(err), zap.String("title", title))
				return MarkdownLink{[]uint16{}, -1, -1}
			}
			if len(resp.Choices) != 0 {
				title = resp.Choices[0].Message.Content
			}
		}

		unescaped, err := url.QueryUnescape(href)
		if err == nil {
			href = unescaped
		}

		md := fmt.Sprintf("[%s](%s)", title, href)
		mdUTF16 := utf16.Encode([]rune(md))

		return MarkdownLink{mdUTF16, startIndex, endIndex}
	})

	for i := len(links) - 1; i >= 0; i-- {
		if links[i].Start == -1 {
			continue
		}

		temp := append(links[i].Markdown, textUTF16[links[i].End:]...)
		textUTF16 = append(textUTF16[:links[i].Start], temp...)
	}

	return string(utf16.Decode(textUTF16))
}

func (m *Model) extractTextWithSummarization(message *tgbotapi.Message) (string, error) {
	text := m.ExtractTextFromMessage(message)
	if text == "" {
		return "", nil
	}
	if utf8.RuneCountInString(text) >= 300 {
		resp, err := m.openAI.SummarizeOneChatHistory(context.Background(), text)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", nil
		}

		return resp.Choices[0].Message.Content, nil
	}

	return text, nil
}

// extractTextFromMessage returns the text recorded for a message, with media
// markers and custom emoji labels that are already cached. It never schedules
// media enrichment.
func (m *Model) extractTextFromMessage(message *tgbotapi.Message) (string, error) {
	text, _, err := m.composeHistoryText(message, false)
	if err == nil && text == "" && message != nil {
		m.logger.Warn("message text is empty")
	}

	return text, err
}

func (m *Model) assignReplyMessageDataForChatHistory(entity *ent.ChatHistoriesCreate, message *tgbotapi.Message) error {
	if message.ReplyToMessage == nil {
		return nil
	}

	repliedToText, _, err := m.composeHistoryText(message.ReplyToMessage, false)
	if err != nil {
		return err
	}
	if repliedToText != "" {
		entity.
			SetRepliedToMessageID(int64(message.ReplyToMessage.MessageID)).
			SetRepliedToUserID(message.ReplyToMessage.From.ID).
			SetRepliedToFullName(tgbot.FullNameFromFirstAndLastName(message.ReplyToMessage.From.FirstName, message.ReplyToMessage.From.LastName)).
			SetRepliedToUsername(message.ReplyToMessage.From.UserName).
			SetRepliedToText(repliedToText).
			SetRepliedToChatType(message.ReplyToMessage.Chat.Type)
	}

	return nil
}

func (m *Model) SaveOneTelegramChatHistory(message *tgbotapi.Message) error {
	if message == nil {
		return nil
	}

	text, needsMedia, err := m.composeHistoryText(message, true)
	if err != nil {
		return err
	}
	if text == "" {
		m.logger.Warn("message text is empty")
		return nil
	}

	telegramChatHistoryCreate := m.ent.ChatHistories.
		Create().
		SetChatID(message.Chat.ID).
		SetChatType(message.Chat.Type).
		SetChatTitle(message.Chat.Title).
		SetMessageID(int64(message.MessageID)).
		SetUserID(message.From.ID).
		SetUsername(message.From.UserName).
		SetFullName(tgbot.FullNameFromFirstAndLastName(message.From.FirstName, message.From.LastName)).
		SetFromPlatform(int(FromPlatformTelegram)).
		SetChattedAt(time.Unix(int64(message.Date), 0).UnixMilli()).
		SetText(text)

	err = m.assignReplyMessageDataForChatHistory(telegramChatHistoryCreate, message)
	if err != nil {
		return err
	}

	telegramChatHistory, err := telegramChatHistoryCreate.Save(context.TODO())
	if err != nil {
		return err
	}

	m.logger.Debug("saved one telegram chat history",
		zap.String("id", telegramChatHistory.ID.String()),
		zap.Int64("chat_id", telegramChatHistory.ChatID),
		zap.Int64("message_id", telegramChatHistory.MessageID),
		zap.String("text", strings.ReplaceAll(telegramChatHistory.Text, "\n", " ")),
	)

	if needsMedia && m.media != nil {
		m.media.enqueue(message)
	}

	return nil
}

func (m *Model) UpdateOneTelegramChatHistory(message *tgbotapi.Message) error {
	if message == nil {
		return nil
	}

	// A message still waiting for media enrichment is written by that job.
	if m.media != nil && m.media.replaceIfQueued(message) {
		return nil
	}

	text, needsMedia, err := m.composeHistoryText(message, true)
	if err != nil {
		return err
	}
	if text == "" {
		m.logger.Warn("message text is empty")
		return nil
	}

	if m.media != nil {
		m.media.writeMu.Lock()
		err = m.writeHistoryText(message, text)
		m.media.writeMu.Unlock()
	} else {
		err = m.writeHistoryText(message, text)
	}
	if err != nil {
		return err
	}

	if needsMedia && m.media != nil {
		m.media.enqueue(message)
	}

	return nil
}

func (m *Model) writeHistoryText(message *tgbotapi.Message, text string) error {
	err := m.ent.ChatHistories.
		Update().
		Where(
			chathistories.ChatID(message.Chat.ID),
			chathistories.MessageID(int64(message.MessageID)),
		).
		SetText(text).
		Exec(context.Background())
	if err != nil {
		return err
	}

	m.logger.Debug("updated one message",
		zap.Int64("chat_id", message.Chat.ID),
		zap.Int("message_id", message.MessageID),
		zap.String("text", strings.ReplaceAll(text, "\n", " ")),
	)

	return nil
}

func (m *Model) DeleteAllChatHistoriesByChatID(chatID int64) error {
	_, err := m.ent.ChatHistories.
		Delete().
		Where(chathistories.ChatID(chatID)).
		Exec(context.Background())
	if err != nil {
		return err
	}

	return nil
}

func (m *Model) MigrateChatHistoriesOfChatFromChatIDToChatID(fromChatID, toChatID int64) error {
	affectedRows, err := m.ent.ChatHistories.
		Update().
		Where(chathistories.ChatID(fromChatID)).
		SetChatID(toChatID).
		SetChatType(string(telegram.ChatTypeSuperGroup)).
		Save(context.Background())
	if err != nil {
		return err
	}

	m.logger.Info("successfully migrated options of chat",
		zap.Int64("from_chat_id", fromChatID),
		zap.Int64("to_chat_id", toChatID),
		zap.Int("affected_rows", affectedRows),
	)

	return nil
}

func (m *Model) FindLastOneHourChatHistories(chatID int64) ([]*ent.ChatHistories, error) {
	return m.FindChatHistoriesByTimeBefore(chatID, time.Hour)
}

func (m *Model) FindLast6HourChatHistories(chatID int64) ([]*ent.ChatHistories, error) {
	return m.FindChatHistoriesByTimeBefore(chatID, 6*time.Hour)
}

func (m *Model) FindLast8HourChatHistories(chatID int64) ([]*ent.ChatHistories, error) {
	return m.FindChatHistoriesByTimeBefore(chatID, 8*time.Hour)
}

func (m *Model) FindLast12HourChatHistories(chatID int64) ([]*ent.ChatHistories, error) {
	return m.FindChatHistoriesByTimeBefore(chatID, 12*time.Hour)
}

func (m *Model) FindChatHistoriesByTimeBefore(chatID int64, before time.Duration) ([]*ent.ChatHistories, error) {
	m.logger.Info("querying chat histories", zap.Int64("chat_id", chatID))

	telegramChatHistories, err := m.ent.ChatHistories.
		Query().
		Where(
			chathistories.ChatID(chatID),
			chathistories.ChattedAtGT(time.Now().Add(-before).UnixMilli()),
		).
		Order(
			chathistories.ByMessageID(sql.OrderAsc()),
		).
		All(context.TODO())
	if err != nil {
		return make([]*ent.ChatHistories, 0), err
	}

	return telegramChatHistories, nil
}

func formatFullNameAndUsername(fullName, username string) string {
	if utf8.RuneCountInString(fullName) >= 10 && username != "" {
		return username
	}

	return strings.ReplaceAll(fullName, "#", "")
}

func buildRichRecapPrompt(histories []*ent.ChatHistories) (string, map[int64]int64) {
	virtualToReal := make(map[int64]int64, len(histories))
	prompt, _ := appendRichRecapPrompt(histories, virtualToReal, 1)

	return prompt, virtualToReal
}

// appendRichRecapPrompt formats histories for the recap model with virtual
// message IDs starting at nextVirtualID, records them in virtualToReal, and
// returns the next unused virtual ID.
func appendRichRecapPrompt(histories []*ent.ChatHistories, virtualToReal map[int64]int64, nextVirtualID int64) (string, int64) {
	lines := make([]string, 0, len(histories))

	for _, message := range histories {
		messageVirtualID := nextVirtualID
		virtualToReal[messageVirtualID] = message.MessageID
		nextVirtualID++

		if message.RepliedToMessageID == 0 {
			lines = append(lines, fmt.Sprintf(
				"msgId:%d: %s sent: %s",
				messageVirtualID,
				formatFullNameAndUsername(message.FullName, message.Username),
				message.Text,
			))
			continue
		}

		repliedToVirtualID := nextVirtualID
		virtualToReal[repliedToVirtualID] = message.RepliedToMessageID
		nextVirtualID++
		repliedToContext := fmt.Sprintf(
			"%s sent msgId:%d",
			formatFullNameAndUsername(message.RepliedToFullName, message.RepliedToUsername),
			repliedToVirtualID,
		)
		lines = append(lines, fmt.Sprintf(
			"msgId:%d: %s replying to [%s]: %s",
			messageVirtualID,
			formatFullNameAndUsername(message.FullName, message.Username),
			repliedToContext,
			message.Text,
		))
	}

	return strings.Join(lines, "\n"), nextVirtualID
}

func (m *Model) SummarizeChatHistories(chatID int64, chatType telegram.ChatType, histories []*ent.ChatHistories) (uuid.UUID, []string, *openai.RecapExecutionTrace, error) {
	chatHistories, virtualToReal := buildRichRecapPrompt(histories)
	summarizations, statusUsage, recapTrace, err := m.summarizeChatHistories(chatID, chatHistories)
	if err != nil {
		return uuid.Nil, []string{}, recapTrace, err
	}

	resolvedSummaries := make([]string, 0, len(summarizations))
	for _, summarization := range summarizations {
		sanitized := SanitizeDetailedRecapMarkdown(summarization)
		resolved := strings.TrimSpace(ResolveRichRecapReferences(sanitized, chatID, chatType, virtualToReal))
		if resolved != "" {
			resolvedSummaries = append(resolvedSummaries, resolved)
		}
	}

	modelName := m.recapModelName(recapTrace)
	firstMessageID, lastMessageID := messageIDRange(histories)

	saved, err := m.ent.LogChatHistoriesRecap.
		Create().
		SetChatID(chatID).
		SetRecapInputs(chatHistories).
		SetRecapOutputs(strings.Join(resolvedSummaries, "\n\n")).
		SetCompletionTokenUsage(statusUsage.CompletionTokens).
		SetPromptTokenUsage(statusUsage.PromptTokens).
		SetTotalTokenUsage(statusUsage.TotalTokens).
		SetFromPlatform(int(FromPlatformTelegram)).
		SetRecapType(int(RecapTypeForGroup)).
		SetModelName(modelName).
		SetFirstMessageID(firstMessageID).
		SetLastMessageID(lastMessageID).
		Save(context.Background())
	if err != nil {
		return uuid.Nil, make([]string, 0), recapTrace, err
	}

	return saved.ID, resolvedSummaries, recapTrace, nil
}

// recapModelName is the model recorded for a recap generated with trace.
func (m *Model) recapModelName(trace *openai.RecapExecutionTrace) string {
	if trace == nil {
		return m.openAI.GetModelName()
	}

	if trace.Generation.BackupUsed && trace.Generation.BackupSucceeded {
		if trace.Generation.BackupUsedModel != "" {
			return trace.Generation.BackupUsedModel
		}
		if trace.Generation.BackupModel != "" {
			return trace.Generation.BackupModel
		}
	} else if trace.Generation.PrimaryUsedModel != "" {
		return trace.Generation.PrimaryUsedModel
	}

	return m.openAI.GetModelName()
}

// messageIDRange returns the smallest and largest message IDs in histories,
// or zeros when histories is empty.
func messageIDRange(histories []*ent.ChatHistories) (int64, int64) {
	if len(histories) == 0 {
		return 0, 0
	}

	first, last := histories[0].MessageID, histories[0].MessageID
	for _, history := range histories[1:] {
		first = min(first, history.MessageID)
		last = max(last, history.MessageID)
	}

	return first, last
}

func (m *Model) GetOpenAIModelName() string {
	return m.openAI.GetModelName()
}

func (m *Model) GetSarcasticCondensedModelName() string {
	return m.openAI.GetSarcasticCondensedModelName()
}
