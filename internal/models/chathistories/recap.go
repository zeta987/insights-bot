package chathistories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"
	goopenai "github.com/sashabaranov/go-openai"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/ent"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

func (m *Model) summarizeChatHistoriesSlice(chatID int64, content string) (string, goopenai.Usage, *openai.RecapExecutionTrace, error) {
	if strings.TrimSpace(content) == "" {
		return "", goopenai.Usage{}, &openai.RecapExecutionTrace{}, nil
	}

	m.logger.Info(fmt.Sprintf("✍️ summarizing chat histories:\n%s", content),
		zap.Int64("chat_id", chatID),
		zap.String("model_name", m.openAI.GetModelName()),
	)

	language := m.config.OpenAI.ChatHistoriesSummarizationLanguage
	recapTrace := &openai.RecapExecutionTrace{}
	recapCtx := openai.WithRecapExecutionTrace(context.Background(), recapTrace)

	response, err := m.openAI.SummarizeChatHistories(recapCtx, content, language)
	if err != nil {
		return "", goopenai.Usage{}, recapTrace, err
	}
	if len(response.Choices) == 0 {
		return "", response.Usage, recapTrace, nil
	}

	usedModel := m.openAI.GetModelName()
	if response.Model != "" {
		usedModel = response.Model
	}
	m.logger.Info("✅ summarized chat histories as Rich Markdown",
		zap.Int64("chat_id", chatID),
		zap.String("model_name", usedModel),
	)

	return strings.TrimSpace(response.Choices[0].Message.Content), response.Usage, recapTrace, nil
}

func mergeUniqueTraceText(current, incoming string) string {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return incoming
	}

	for _, existing := range strings.Split(current, ", ") {
		if existing == incoming {
			return current
		}
	}
	return current + ", " + incoming
}

func mergeGenerationModelExecutionTrace(dst *openai.GenerationModelExecutionTrace, src openai.GenerationModelExecutionTrace) {
	dst.PrimaryModel = mergeUniqueTraceText(dst.PrimaryModel, src.PrimaryModel)
	dst.PrimaryUsedModel = mergeUniqueTraceText(dst.PrimaryUsedModel, src.PrimaryUsedModel)
	dst.PrimaryFailed = dst.PrimaryFailed || src.PrimaryFailed
	dst.PrimaryFailureReason = mergeUniqueTraceText(dst.PrimaryFailureReason, src.PrimaryFailureReason)
	dst.BackupModel = mergeUniqueTraceText(dst.BackupModel, src.BackupModel)
	dst.BackupUsedModel = mergeUniqueTraceText(dst.BackupUsedModel, src.BackupUsedModel)
	dst.BackupUsed = dst.BackupUsed || src.BackupUsed
	dst.BackupSucceeded = dst.BackupSucceeded || src.BackupSucceeded
	dst.BackupFailureReason = mergeUniqueTraceText(dst.BackupFailureReason, src.BackupFailureReason)
}

func mergeRecapExecutionTrace(dst, src *openai.RecapExecutionTrace) {
	if dst == nil || src == nil {
		return
	}
	mergeGenerationModelExecutionTrace(&dst.Generation, src.Generation)
}

func (m *Model) summarizeChatHistories(chatID int64, llmFriendlyChatHistories string) ([]string, goopenai.Usage, *openai.RecapExecutionTrace, error) {
	tokenLimit := m.config.OpenAI.TokenLimit - m.config.OpenAI.ChatHistoriesRecapTokenLimit
	chatHistoriesSlices := m.openAI.SplitContentBasedByTokenLimitations(llmFriendlyChatHistories, int(tokenLimit))
	summaries := make([]string, 0, len(chatHistoriesSlices))
	aggregatedTrace := &openai.RecapExecutionTrace{}

	var totalUsage goopenai.Usage
	for _, content := range chatHistoriesSlices {
		var summary string
		var lastTrace *openai.RecapExecutionTrace
		var successfulTrace *openai.RecapExecutionTrace

		_, _, err := lo.AttemptWithDelay(5, time.Second, func(tried int, delay time.Duration) error {
			generated, usage, recapTrace, summarizeErr := m.summarizeChatHistoriesSlice(chatID, content)
			lastTrace = recapTrace
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.TotalTokens += usage.TotalTokens

			if summarizeErr != nil {
				m.logger.Error(fmt.Sprintf("failed to summarize chat histories slice: %s, tried %d...", content, tried),
					zap.Int64("chat_id", chatID),
					zap.String("model_name", m.openAI.GetModelName()),
					zap.Int64("configured_chat_histories_recap_token_limit", m.config.OpenAI.ChatHistoriesRecapTokenLimit),
					zap.Int64("configured_token_limit", m.config.OpenAI.TokenLimit),
					zap.Int64("calculated_token_limit", tokenLimit),
				)
				return summarizeErr
			}

			if strings.TrimSpace(generated) == "" {
				return errors.New("recap model returned empty Rich Markdown")
			}

			summary = generated
			successfulTrace = recapTrace
			return nil
		})
		if err != nil {
			mergeRecapExecutionTrace(aggregatedTrace, lastTrace)
			return []string{}, totalUsage, aggregatedTrace, err
		}
		mergeRecapExecutionTrace(aggregatedTrace, successfulTrace)
		if summary != "" {
			summaries = append(summaries, summary)
		}
	}

	return summaries, totalUsage, aggregatedTrace, nil
}

// GenSarcasticCondensed generates the first-stage condensed recap text.
func (m *Model) GenSarcasticCondensed(chatID int64, histories []*ent.ChatHistories) (string, *openai.CondensedExecutionTrace, error) {
	if len(histories) == 0 {
		return "", &openai.CondensedExecutionTrace{}, fmt.Errorf("no chat histories")
	}

	builder := strings.Builder{}
	for i, history := range histories {
		userName := history.FullName
		if userName == "" {
			userName = history.Username
		}
		if userName == "" {
			userName = "未知用戶"
		}

		messageText := history.Text
		if messageText == "" {
			continue
		}

		builder.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, userName, messageText))
	}

	return m.GenSarcasticCondensedFromText(chatID, builder.String())
}

// GenSarcasticCondensedFromText runs the independent condensed-summary stage
// for recap sources that are not stored as Ent chat histories.
func (m *Model) GenSarcasticCondensedFromText(chatID int64, chatHistory string) (string, *openai.CondensedExecutionTrace, error) {
	if strings.TrimSpace(chatHistory) == "" {
		return "", &openai.CondensedExecutionTrace{}, fmt.Errorf("no chat history text")
	}

	condensedTrace := &openai.CondensedExecutionTrace{}
	condensedCtx := openai.WithCondensedExecutionTrace(context.Background(), condensedTrace)
	result, err := m.openAI.SarcasticCondense(condensedCtx, chatHistory)
	if err != nil {
		m.logger.Error("failed to generate sarcastic condensed summary",
			zap.Int64("chat_id", chatID),
			zap.Error(err),
		)
		return "", condensedTrace, err
	}

	return result, condensedTrace, nil
}
