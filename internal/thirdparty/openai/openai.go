package openai

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pkoukk/tiktoken-go"
	"github.com/samber/lo"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/fx"
	"go.uber.org/ratelimit"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/pkg/logger"
)

//counterfeiter:generate -o openaimock/mock_client.go --fake-name MockClient . Client
type Client interface {
	GetModelName() string
	GetSarcasticCondensedModelName() string
	SplitContentBasedByTokenLimitations(textContent string, limits int) []string
	SummarizeAny(ctx context.Context, content string) (*openai.ChatCompletionResponse, error)
	SummarizeChatHistories(ctx context.Context, llmFriendlyChatHistories string, language string) (*openai.ChatCompletionResponse, error)
	SummarizeOneChatHistory(ctx context.Context, llmFriendlyChatHistory string) (*openai.ChatCompletionResponse, error)
	SummarizeWithQuestionsAsSimplifiedChinese(ctx context.Context, title string, by string, content string) (*openai.ChatCompletionResponse, error)
	TruncateContentBasedOnTokens(textContent string, limits int) string
	SarcasticCondense(ctx context.Context, chatHistory string) (string, error)
}

var _ Client = (*OpenAIClient)(nil)

type OpenAIClient struct {
	modelName                           string
	modelNameBackup                     string
	sarcasticCondensedModelName         string
	sarcasticCondensedModelBackup       string
	checkModelName                      string
	checkModelNameBackup                string
	forceInvalidRecapJSONForTest        bool
	forceCondensedPrimaryFailureForTest bool
	forceCheckModelFailure              bool
	enableVerbosePayloadLogs            bool
	defaultSummarizationLanguage        string

	tiktokenEncoding            *tiktoken.Tiktoken
	client                      *openai.Client
	ent                         *datastore.Ent
	logger                      *logger.Logger
	limiter                     ratelimit.Limiter
	enableMetricRecordForTokens bool
}

var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

const forcedInvalidRecapJSONForTestPayload = "```json,\n\n&nbsp;   \"discussion\":\n\n&nbsp;     },\n\n&nbsp;     {\n\n&nbsp;       \"point\": \"The AI requires a dialogue transcript or document to analyze and extract the requested discussion topics.\",\n\n&nbsp;       \"keyIds\":\n\n&nbsp;     }\n\n&nbsp;   ],\n\n&nbsp;   \"conclusion\": \"Please provide the chat history so that I can process it and generate the summarized outline according to your JSON schema.\"\n\n&nbsp; }\n\n]\n\n```"

func parseOpenAIAPIHost(apiHost string) (string, error) {
	if !strings.HasPrefix(apiHost, "https://") && !strings.HasPrefix(apiHost, "http://") {
		apiHost = "http://" + apiHost
	}

	parsedURL, err := url.Parse(apiHost)
	if err != nil {
		return "", err
	}

	host := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	if host != "" {
		return host, nil
	}

	return "", fmt.Errorf("invalid API host: %s", apiHost)
}

type NewClientParams struct {
	fx.In

	Config *configs.Config
	Logger *logger.Logger
	Ent    *datastore.Ent
}

func NewClient(enableMetricRecordForTokens bool) func(NewClientParams) (Client, error) {
	return func(params NewClientParams) (Client, error) {
		tokenizer, err := tiktoken.EncodingForModel(openai.GPT3Dot5Turbo)
		if err != nil {
			return nil, err
		}

		apiHost := params.Config.OpenAI.Host

		config := openai.DefaultConfig(params.Config.OpenAI.Secret)
		if apiHost != "" {
			apiHost, err = parseOpenAIAPIHost(apiHost)
			if err != nil {
				return nil, err
			}

			config.BaseURL = fmt.Sprintf("%s/v1", apiHost)
		}

		client := openai.NewClientWithConfig(config)

		limiter := ratelimit.New(1)
		limiter.Take()

		primaryModel := strings.TrimSpace(lo.Ternary(params.Config.OpenAI.ModelName == "", openai.GPT3Dot5Turbo, params.Config.OpenAI.ModelName))
		recapBackupModels := normalizeModelList(lo.Ternary(params.Config.OpenAI.ModelNameBackup == "", primaryModel, params.Config.OpenAI.ModelNameBackup))
		condensedPrimaryModel := strings.TrimSpace(lo.Ternary(params.Config.OpenAI.SarcasticCondensedModelName == "", primaryModel, params.Config.OpenAI.SarcasticCondensedModelName))
		condensedBackupModels := normalizeModelList(lo.Ternary(params.Config.OpenAI.SarcasticCondensedModelNameBackup == "", condensedPrimaryModel, params.Config.OpenAI.SarcasticCondensedModelNameBackup))
		checkPrimaryModel := strings.TrimSpace(params.Config.OpenAI.CheckModelName)
		checkBackupModels := normalizeModelList(params.Config.OpenAI.CheckModelNameBackup)

		return &OpenAIClient{
			modelName:                           primaryModel,
			modelNameBackup:                     recapBackupModels,
			sarcasticCondensedModelName:         condensedPrimaryModel,
			sarcasticCondensedModelBackup:       condensedBackupModels,
			checkModelName:                      checkPrimaryModel,
			checkModelNameBackup:                checkBackupModels,
			forceInvalidRecapJSONForTest:        params.Config.OpenAI.ForceInvalidRecapJSONForTest,
			forceCondensedPrimaryFailureForTest: params.Config.OpenAI.ForceCondensedPrimaryFailureForTest,
			forceCheckModelFailure:              params.Config.OpenAI.ForceCheckModelFailure,
			enableVerbosePayloadLogs:            params.Config.OpenAI.EnableVerbosePayloadLogs,
			defaultSummarizationLanguage:        lo.Ternary(params.Config.OpenAI.ChatHistoriesSummarizationLanguage == "", "Simplified Chinese", params.Config.OpenAI.ChatHistoriesSummarizationLanguage),
			client:                              client,
			tiktokenEncoding:                    tokenizer,
			ent:                                 params.Ent,
			logger:                              params.Logger,
			limiter:                             limiter,
			enableMetricRecordForTokens:         enableMetricRecordForTokens,
		}, nil
	}
}

func (c *OpenAIClient) GetModelName() string {
	return c.modelName
}

func (c *OpenAIClient) GetSarcasticCondensedModelName() string {
	return c.sarcasticCondensedModelName
}

func shouldFallbackByError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return true
	}

	return true
}

func parseModelList(raw string) []string {
	if raw == "" {
		return []string{}
	}

	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	models := make([]string, 0, len(parts))
	for _, part := range parts {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}

	return models
}

func normalizeModelList(raw string) string {
	return strings.Join(parseModelList(raw), ", ")
}

func filterBackupModels(raw string, excludes ...string) []string {
	excludeSet := make(map[string]struct{}, len(excludes))
	for _, exclude := range excludes {
		model := strings.TrimSpace(exclude)
		if model == "" {
			continue
		}
		excludeSet[model] = struct{}{}
	}

	filtered := make([]string, 0)
	for _, model := range parseModelList(raw) {
		if _, excluded := excludeSet[model]; excluded {
			continue
		}
		filtered = append(filtered, model)
	}

	return filtered
}

func (c *OpenAIClient) recapBackupModels() []string {
	return filterBackupModels(c.modelNameBackup, c.modelName)
}

func (c *OpenAIClient) condensedBackupModels() []string {
	return filterBackupModels(c.sarcasticCondensedModelBackup, c.sarcasticCondensedModelName)
}

func (c *OpenAIClient) checkBackupModels() []string {
	return filterBackupModels(c.checkModelNameBackup, c.checkModelName)
}

func cleanJSONResponseContent(content string) string {
	cleaned := strings.TrimSpace(content)
	cleaned = strings.TrimPrefix(cleaned, "\ufeff")

	if strings.HasPrefix(cleaned, "```json") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
	}
	if strings.HasPrefix(cleaned, "```JSON") {
		cleaned = strings.TrimPrefix(cleaned, "```JSON")
	}
	if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```")
	}
	cleaned = strings.TrimSpace(cleaned)
	if strings.HasSuffix(cleaned, "```") {
		cleaned = strings.TrimSuffix(cleaned, "```")
	}
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.ReplaceAll(cleaned, "&nbsp;", " ")

	start := strings.Index(cleaned, "[")
	end := strings.LastIndex(cleaned, "]")
	if start >= 0 && end > start {
		cleaned = cleaned[start : end+1]
	}

	return strings.TrimSpace(cleaned)
}

func forceRepairSummaryJSON(content string) string {
	repaired := cleanJSONResponseContent(content)
	repaired = trailingCommaPattern.ReplaceAllString(repaired, "$1")
	repaired = strings.TrimSpace(strings.TrimPrefix(repaired, ","))
	repaired = strings.TrimSpace(strings.TrimSuffix(repaired, ","))

	return repaired
}

func validateAndCompactSummaryJSON(content string) (string, error) {
	candidate := strings.TrimSpace(content)
	if candidate == "" {
		return "", errors.New("empty json content")
	}

	var outputs []*ChatHistorySummarizationOutputs
	if err := json.Unmarshal([]byte(candidate), &outputs); err != nil {
		return "", err
	}

	compact := new(bytes.Buffer)
	if err := json.Compact(compact, []byte(candidate)); err != nil {
		return "", err
	}

	return compact.String(), nil
}

func normalizeCondensedDisplayText(content string) string {
	normalized := strings.TrimSpace(content)
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	cleanedLines := make([]string, 0, len(lines))
	previousEmpty := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if previousEmpty {
				continue
			}
			previousEmpty = true
			cleanedLines = append(cleanedLines, "")
			continue
		}

		previousEmpty = false
		cleanedLines = append(cleanedLines, trimmed)
	}

	return strings.TrimSpace(strings.Join(cleanedLines, "\n"))
}

func isJSONLikeCondensedOutput(content string) bool {
	candidate := strings.TrimSpace(content)
	if candidate == "" {
		return false
	}

	var decoded any
	if err := json.Unmarshal([]byte(candidate), &decoded); err == nil {
		switch decoded.(type) {
		case map[string]any, []any:
			return true
		}
	}

	if strings.HasPrefix(candidate, "{") && strings.HasSuffix(candidate, "}") {
		return true
	}

	if strings.HasPrefix(candidate, "[") && strings.HasSuffix(candidate, "]") {
		return true
	}

	return false
}

func invalidCondensedOutputReason(content string) string {
	candidate := strings.TrimSpace(content)
	if candidate == "" {
		return "no content generated"
	}

	if strings.Contains(candidate, "```") {
		return "condensed output contains code fence"
	}

	if isJSONLikeCondensedOutput(candidate) {
		return "condensed output is json-like"
	}

	return ""
}

func normalizeCondensedOutputContent(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if reason := invalidCondensedOutputReason(trimmed); reason != "" {
		return "", errors.New(reason)
	}

	return normalizeCondensedDisplayText(trimmed), nil
}

func (c *OpenAIClient) callChatCompletionWithModel(
	ctx context.Context,
	model string,
	messages []openai.ChatCompletionMessage,
	temperature *float32,
) (openai.ChatCompletionResponse, error) {
	c.limiter.Take()

	request := openai.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	}
	if temperature != nil {
		request.Temperature = *temperature
	}

	return c.client.CreateChatCompletion(ctx, request)
}

func (c *OpenAIClient) logWarn(message string, fields ...zap.Field) {
	if c.logger == nil {
		return
	}

	c.logger.Warn(message, fields...)
}

func (c *OpenAIClient) logInfo(message string, fields ...zap.Field) {
	if c.logger == nil {
		return
	}

	c.logger.Info(message, fields...)
}

func (c *OpenAIClient) logChatCompletionPayload(
	stage string,
	operation string,
	model string,
	body any,
) {
	if !c.enableVerbosePayloadLogs {
		return
	}

	b, err := json.Marshal(body)
	if err != nil {
		c.logWarn("failed to marshal openai payload body for verbose logs",
			zap.String("stage", stage),
			zap.String("operation", operation),
			zap.String("model", model),
			zap.Error(err),
		)
		return
	}

	c.logInfo("openai verbose payload",
		zap.String("stage", stage),
		zap.String("operation", operation),
		zap.String("model", model),
		zap.String("body", string(b)),
	)
}

func (c *OpenAIClient) logVerboseJSONBody(stage string, content string) {
	if !c.enableVerbosePayloadLogs {
		return
	}

	c.logInfo("openai verbose json content",
		zap.String("stage", stage),
		zap.String("content", content),
	)
}

func (c *OpenAIClient) callChatCompletionWithModelAndVerboseLog(
	ctx context.Context,
	operation string,
	model string,
	messages []openai.ChatCompletionMessage,
	temperature *float32,
) (openai.ChatCompletionResponse, error) {
	request := openai.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	}
	if temperature != nil {
		request.Temperature = *temperature
	}

	c.logChatCompletionPayload("request", operation, model, request)

	resp, err := c.callChatCompletionWithModel(ctx, model, messages, temperature)
	if err != nil {
		c.logWarn("openai chat completion call failed",
			zap.String("operation", operation),
			zap.String("model", model),
			zap.Error(err),
		)
		return resp, err
	}

	c.logChatCompletionPayload("response", operation, model, resp)

	return resp, nil
}

func (c *OpenAIClient) callCheckModelRepairOnce(
	ctx context.Context,
	model string,
	messages []openai.ChatCompletionMessage,
	operation string,
) (string, error) {
	resp, err := c.callChatCompletionWithModelAndVerboseLog(
		ctx,
		operation,
		model,
		messages,
		nil,
	)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", errors.New("check model returned empty choices")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func (c *OpenAIClient) repairSummaryJSONByCheckModel(ctx context.Context, rawJSON string) (string, string, bool, error) {
	if c.checkModelName == "" {
		return "", "", false, errors.New("check model is not configured")
	}

	if c.forceCheckModelFailure {
		c.logWarn("check model repair forced to fail for local validation",
			zap.String("check_model", c.checkModelName),
		)
		return "", "", false, errors.New("check model forced failure via env switch")
	}

	sb := new(strings.Builder)
	if err := CheckSummaryJSONUserPrompt.Execute(sb, CheckSummaryJSONInputs{RawJSON: rawJSON}); err != nil {
		return "", "", false, err
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: CheckSummaryJSONSystemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: sb.String(),
		},
	}
	checked, err := c.callCheckModelRepairOnce(
		ctx,
		c.checkModelName,
		messages,
		"check_model_repair_primary",
	)
	if err == nil {
		return checked, c.checkModelName, false, nil
	}

	backupModels := c.checkBackupModels()
	if len(backupModels) == 0 {
		return "", "", false, err
	}

	lastErr := err
	for idx, backupModel := range backupModels {
		checked, backupErr := c.callCheckModelRepairOnce(
			ctx,
			backupModel,
			messages,
			fmt.Sprintf("check_model_repair_backup_%d", idx+1),
		)
		if backupErr != nil {
			lastErr = backupErr
			continue
		}

		return checked, backupModel, true, nil
	}

	return "", "", true, lastErr
}

func (c *OpenAIClient) repairCondensedOutputByCheckModel(ctx context.Context, rawOutput string) (string, string, bool, error) {
	if c.checkModelName == "" {
		return "", "", false, errors.New("check model is not configured")
	}

	if c.forceCheckModelFailure {
		c.logWarn("check model repair forced to fail for local validation",
			zap.String("check_model", c.checkModelName),
		)
		return "", "", false, errors.New("check model forced failure via env switch")
	}

	sb := new(strings.Builder)
	if err := CheckCondensedOutputUserPrompt.Execute(sb, CheckCondensedOutputInputs{RawOutput: rawOutput}); err != nil {
		return "", "", false, err
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: CheckCondensedOutputSystemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: sb.String(),
		},
	}

	checked, err := c.callCheckModelRepairOnce(
		ctx,
		c.checkModelName,
		messages,
		"check_model_condensed_repair_primary",
	)
	if err == nil {
		return checked, c.checkModelName, false, nil
	}

	backupModels := c.checkBackupModels()
	if len(backupModels) == 0 {
		return "", "", false, err
	}

	lastErr := err
	for idx, backupModel := range backupModels {
		checked, backupErr := c.callCheckModelRepairOnce(
			ctx,
			backupModel,
			messages,
			fmt.Sprintf("check_model_condensed_repair_backup_%d", idx+1),
		)
		if backupErr != nil {
			lastErr = backupErr
			continue
		}

		return checked, backupModel, true, nil
	}

	return "", "", true, lastErr
}

func (c *OpenAIClient) normalizeSummaryJSONContent(ctx context.Context, content string, trace *RecapExecutionTrace) (string, error) {
	candidate := cleanJSONResponseContent(content)
	c.logVerboseJSONBody("normalize.raw_candidate", candidate)
	compacted, rawErr := validateAndCompactSummaryJSON(candidate)
	if rawErr == nil {
		c.logVerboseJSONBody("normalize.raw_compacted", compacted)
		return compacted, nil
	}
	c.logWarn("summary json is invalid, trying force repair",
		zap.Error(rawErr),
	)

	repaired := forceRepairSummaryJSON(candidate)
	c.logVerboseJSONBody("normalize.local_repaired", repaired)
	compacted, repairedErr := validateAndCompactSummaryJSON(repaired)
	if repairedErr == nil {
		c.logInfo("summary json fixed by local force repair")
		c.logVerboseJSONBody("normalize.local_repaired_compacted", compacted)
		return compacted, nil
	}
	c.logWarn("local force repair failed, trying check model",
		zap.Error(repairedErr),
		zap.String("check_model", c.checkModelName),
	)

	if c.checkModelName != "" {
		if trace != nil {
			trace.Check.Model = c.checkModelName
			trace.Check.BackupModel = c.checkModelNameBackup
			trace.Check.Attempted = true
		}

		var checkValidationErr error

		checked, checkUsedModel, checkBackupTried, checkErr := c.repairSummaryJSONByCheckModel(ctx, repaired)
		checkBackupUsed := checkUsedModel != "" && checkUsedModel != c.checkModelName
		if trace != nil && checkBackupTried {
			trace.Check.BackupUsed = true
		}
		if trace != nil && checkBackupUsed {
			trace.Check.BackupUsedModel = checkUsedModel
		}

		if checkErr == nil {
			c.logInfo("check model returned repaired summary json",
				zap.String("check_model", checkUsedModel),
			)
			c.logVerboseJSONBody("normalize.check_model_raw", checked)
			checked = cleanJSONResponseContent(checked)
			c.logVerboseJSONBody("normalize.check_model_cleaned", checked)
			if compacted, err := validateAndCompactSummaryJSON(checked); err == nil {
				c.logVerboseJSONBody("normalize.check_model_compacted", compacted)
				if trace != nil && !trace.Check.Failed {
					trace.Check.Succeeded = true
					if checkBackupUsed {
						trace.Check.BackupSucceeded = true
					}
				}
				return compacted, nil
			} else {
				checkValidationErr = err
			}

			checked = forceRepairSummaryJSON(checked)
			c.logVerboseJSONBody("normalize.check_model_force_repaired", checked)
			if compacted, err := validateAndCompactSummaryJSON(checked); err == nil {
				c.logInfo("summary json fixed after check model + local repair",
					zap.String("check_model", checkUsedModel),
				)
				c.logVerboseJSONBody("normalize.check_model_force_repaired_compacted", compacted)
				if trace != nil && !trace.Check.Failed {
					trace.Check.Succeeded = true
					if checkBackupUsed {
						trace.Check.BackupSucceeded = true
					}
				}
				return compacted, nil
			} else {
				checkValidationErr = err
			}
		} else {
			c.logWarn("check model failed to repair summary json",
				zap.Error(checkErr),
				zap.String("check_model", c.checkModelName),
			)
			if trace != nil {
				trace.Check.Failed = true
				if trace.Check.FailureReason == "" {
					trace.Check.FailureReason = checkErr.Error()
				}
				if checkBackupTried {
					trace.Check.BackupUsed = true
					trace.Check.BackupSucceeded = false
					trace.Check.BackupFailureReason = checkErr.Error()
				}
			}
		}

		if checkErr == nil && checkValidationErr != nil && trace != nil {
			trace.Check.Failed = true
			if trace.Check.FailureReason == "" {
				trace.Check.FailureReason = checkValidationErr.Error()
			}
			if checkBackupUsed {
				trace.Check.BackupSucceeded = false
				trace.Check.BackupFailureReason = checkValidationErr.Error()
			}
		}
	} else {
		c.logWarn("check model is not configured, skip check-model repair")
		if trace != nil {
			trace.Check.Model = ""
		}
	}

	return "", fmt.Errorf("failed to normalize summary json")
}

// truncateContentBasedOnTokens 基于 token 计算的方式截断文本。
func (c *OpenAIClient) TruncateContentBasedOnTokens(textContent string, limits int) string {
	tokens := c.tiktokenEncoding.Encode(textContent, nil, nil)
	if len(tokens) <= limits {
		return textContent
	}

	truncated := c.tiktokenEncoding.Decode(tokens[:limits])

	for len(truncated) > 0 {
		// 假设 textContent = "小溪河水清澈见底", Encode 结果为 "[31809,36117,103,31106,111,53610,80866,162,122,230,90070,11795,243]"
		// 当 limits = 4, 那么 tokens[:limits] = "[31809,36117,103,31106]", Decode 结果为 "小溪\xe6\xb2"
		// 这里的 \xe6\xb2 是一个不完整的 UTF-8 编码，无法正确解析为一个完整的字符。下面得代码处理这种情况把它去掉。
		r, size := utf8.DecodeLastRuneInString(truncated)
		if r != utf8.RuneError {
			break
		}
		truncated = truncated[:len(truncated)-size]
	}

	return truncated
}

// SplitContentBasedByTokenLimitations 基于 token 计算的方式分割文本。
func (c *OpenAIClient) SplitContentBasedByTokenLimitations(textContent string, limits int) []string {
	slices := make([]string, 0)

	for {
		s := c.TruncateContentBasedOnTokens(textContent, limits)
		slices = append(slices, s)
		textContent = textContent[len(s):]

		if textContent == "" {
			break
		}
	}

	return slices
}

// SummarizeWithQuestionsAsSimplifiedChinese 通过 OpenAI 的 Chat API 来为文章生成摘要和联想问题。
func (c *OpenAIClient) SummarizeWithQuestionsAsSimplifiedChinese(ctx context.Context, title, by, content string) (*openai.ChatCompletionResponse, error) {
	c.limiter.Take()

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleSystem,
					Content: "" +
						"你是我的网页文章阅读助理。我将为你提供文章的标题、作" +
						"者、所抓取的网页中的正文等信息，然后你将对文章做出总结。\n请你在总结时满足以下要求：" +
						"1. 首先如果文章的标题不是中文的请依据上下文将标题信达雅的翻译为简体中文并放在第一行" +
						"2. 然后从我提供的文章信息中总结出一个三百字以内的文章的摘要" +
						"3. 最后，你将利用你已有的知识和经验，对我提供的文章信息提出 3 个具有创造性和发散思维的问题" +
						"4. 请用简体中文进行回复" +
						"最终你回复的消息格式应像这个例句一样（例句中的双花括号为需要替换的内容）：\n" +
						"{{简体中文标题，可省略}}\n\n摘要：{{文章的摘要}}\n\n关联提问：\n1. {{关联提问 1}}\n2. {{关联提问 2}}\n3. {{关联提问 3}}",
				},
				{
					Role: openai.ChatMessageRoleUser,
					Content: "" +
						"我的第一个要求相关的信息如下：" +
						fmt.Sprintf("文章标题：%s；", title) +
						fmt.Sprintf("文章作者：%s；", by) +
						fmt.Sprintf("文章正文：%s；", content) +
						"接下来请你完成我所要求的任务。",
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if c.enableMetricRecordForTokens {
		err = c.ent.MetricOpenAIChatCompletionTokenUsage.
			Create().
			SetPromptOperation("Summarize With Questions As Simplified Chinese").
			SetPromptTokenUsage(resp.Usage.PromptTokens).
			SetCompletionTokenUsage(resp.Usage.CompletionTokens).
			SetTotalTokenUsage(resp.Usage.TotalTokens).
			SetModelName(c.modelName).
			Exec(ctx)
		if err != nil {
			c.logger.Error("failed to create metric openai chat completion token usage", zap.Error(err),
				zap.String("prompt_operation", "Summarize With Questions As Simplified Chinese"),
				zap.Int("prompt_token_usage", resp.Usage.PromptTokens),
				zap.Int("completion_token_usage", resp.Usage.CompletionTokens),
				zap.Int("total_token_usage", resp.Usage.TotalTokens),
				zap.String("model_name", c.modelName),
			)
		}
	}

	return &resp, nil
}

func (c *OpenAIClient) SummarizeOneChatHistory(ctx context.Context, llmFriendlyChatHistory string) (*openai.ChatCompletionResponse, error) {
	c.limiter.Take()

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleSystem,
					Content: "" +
						"你是我的聊天消息总结助手。我将为你提供一条包含了人物名称、人物用户名、消息" +
						"发送时间、消息内容等信息的消息，因为这条聊天消息有些过长了，我需要你帮我总" +
						"结一下这条消息说了什么。最好一句话概括，如果这条消息有标题的话你可以直接返" +
						"回标题。" +
						"",
				},
				{
					Role: openai.ChatMessageRoleUser,
					Content: "" +
						"消息：\n" +
						llmFriendlyChatHistory + "\n" +
						"请你帮我总结一下。",
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if c.enableMetricRecordForTokens {
		err = c.ent.MetricOpenAIChatCompletionTokenUsage.
			Create().
			SetPromptOperation("Summarize One Chat History").
			SetPromptTokenUsage(resp.Usage.PromptTokens).
			SetCompletionTokenUsage(resp.Usage.CompletionTokens).
			SetTotalTokenUsage(resp.Usage.TotalTokens).
			SetModelName(c.modelName).
			Exec(ctx)
		if err != nil {
			c.logger.Error("failed to create metric openai chat completion token usage",
				zap.Error(err),
				zap.String("prompt_operation", "Summarize One Chat History"),
				zap.Int("prompt_token_usage", resp.Usage.PromptTokens),
				zap.Int("completion_token_usage", resp.Usage.CompletionTokens),
				zap.Int("total_token_usage", resp.Usage.TotalTokens),
				zap.String("model_name", c.modelName),
			)
		}
	}

	return &resp, nil
}

// SummarizeAny 通过 OpenAI 的 Chat API 来为任意内容生成摘要。
func (c *OpenAIClient) SummarizeAny(ctx context.Context, content string) (*openai.ChatCompletionResponse, error) {
	c.limiter.Take()

	sb := new(strings.Builder)

	err := AnySummarizationUserPrompt.Execute(sb, AnySummarizationInputs{
		Content: content,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: AnySummarizationSystemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: sb.String(),
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if c.enableMetricRecordForTokens {
		err = c.ent.MetricOpenAIChatCompletionTokenUsage.
			Create().
			SetPromptOperation("Summarize Any").
			SetPromptTokenUsage(resp.Usage.PromptTokens).
			SetCompletionTokenUsage(resp.Usage.CompletionTokens).
			SetTotalTokenUsage(resp.Usage.TotalTokens).
			SetModelName(c.modelName).
			Exec(ctx)
		if err != nil {
			c.logger.Error("failed to create metric openai chat completion token usage",
				zap.Error(err),
				zap.String("prompt_operation", "Summarize Any"),
				zap.Int("prompt_token_usage", resp.Usage.PromptTokens),
				zap.Int("completion_token_usage", resp.Usage.CompletionTokens),
				zap.Int("total_token_usage", resp.Usage.TotalTokens),
				zap.String("model_name", c.modelName),
			)
		}
	}

	return &resp, nil
}

func (c *OpenAIClient) SummarizeChatHistories(ctx context.Context, llmFriendlyChatHistories string, language string) (*openai.ChatCompletionResponse, error) {
	trace := recapExecutionTraceFromContext(ctx)
	if trace != nil {
		trace.Generation.PrimaryModel = c.modelName
		trace.Generation.BackupModel = c.modelNameBackup
		trace.Check.Model = c.checkModelName
		trace.Check.BackupModel = c.checkModelNameBackup
	}

	if language == "" {
		language = c.defaultSummarizationLanguage
	}

	sb := new(strings.Builder)

	err := ChatHistorySummarizationUserPrompt.Execute(
		sb,
		NewChatHistorySummarizationPromptInputs(
			llmFriendlyChatHistories,
			language,
		),
	)
	if err != nil {
		return nil, err
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: ChatHistorySummarizationSystemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: sb.String(),
		},
	}

	tryRecapBackupModels := func(operationPrefix string, normalize bool) (openai.ChatCompletionResponse, string, string, error) {
		var lastErr error
		for idx, backupModel := range c.recapBackupModels() {
			backupResp, backupErr := c.callChatCompletionWithModelAndVerboseLog(
				ctx,
				fmt.Sprintf("%s_%d", operationPrefix, idx+1),
				backupModel,
				messages,
				nil,
			)
			if backupErr != nil {
				lastErr = backupErr
				continue
			}
			if len(backupResp.Choices) == 0 || strings.TrimSpace(backupResp.Choices[0].Message.Content) == "" {
				lastErr = errors.New("backup model returned empty recap content")
				continue
			}

			if normalize {
				normalizedContent, normalizeErr := c.normalizeSummaryJSONContent(ctx, backupResp.Choices[0].Message.Content, trace)
				if normalizeErr != nil {
					lastErr = normalizeErr
					continue
				}
				backupResp.Choices[0].Message.Content = normalizedContent
			}

			usedModel := backupModel
			if backupResp.Model != "" {
				usedModel = backupResp.Model
			}

			return backupResp, backupModel, usedModel, nil
		}

		if lastErr == nil {
			lastErr = errors.New("all backup recap models failed")
		}
		return openai.ChatCompletionResponse{}, "", "", lastErr
	}

	usedModel := c.modelName

	resp, err := c.callChatCompletionWithModelAndVerboseLog(
		ctx,
		"recap_primary",
		c.modelName,
		messages,
		nil,
	)
	if err != nil {
		if shouldFallbackByError(err) && len(c.recapBackupModels()) > 0 {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = err.Error()
				trace.Generation.BackupUsed = true
			}

			c.logger.Warn("primary model failed, switching to backup model for recap",
				zap.String("primary_model", c.modelName),
				zap.String("backup_models", c.modelNameBackup),
				zap.Error(err),
			)

			backupResp, backupRequestedModel, backupUsedModel, backupErr := tryRecapBackupModels("recap_backup_after_primary_error", true)
			if backupErr != nil {
				if trace != nil {
					trace.Generation.BackupSucceeded = false
					trace.Generation.BackupFailureReason = backupErr.Error()
				}
				return nil, backupErr
			}

			resp = backupResp
			usedModel = backupUsedModel
			if trace != nil {
				trace.Generation.BackupSucceeded = true
				trace.Generation.BackupUsedModel = backupRequestedModel
			}
		} else {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = err.Error()
			}
			return nil, err
		}
	}

	if resp.Model != "" {
		usedModel = resp.Model
	}
	if trace != nil {
		if !trace.Generation.BackupUsed {
			trace.Generation.PrimaryUsedModel = usedModel
		}
	}
	c.logVerboseJSONBody("recap.raw_model_output", lo.Ternary(len(resp.Choices) > 0, resp.Choices[0].Message.Content, ""))

	if c.forceInvalidRecapJSONForTest && len(resp.Choices) > 0 {
		c.logWarn("force invalid recap json test mode is enabled, overriding model output",
			zap.String("used_model", usedModel),
		)
		resp.Choices[0].Message.Content = forcedInvalidRecapJSONForTestPayload
		c.logVerboseJSONBody("recap.forced_invalid_output", resp.Choices[0].Message.Content)
	}

	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		if len(c.recapBackupModels()) > 0 && (trace == nil || !trace.Generation.BackupUsed) {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = "primary model returned empty recap content"
				trace.Generation.BackupUsed = true
			}

			c.logger.Warn("primary model returned empty recap content, retrying with backup model",
				zap.String("primary_model", c.modelName),
				zap.String("backup_models", c.modelNameBackup),
			)

			backupResp, backupRequestedModel, backupUsedModel, backupErr := tryRecapBackupModels("recap_backup_after_empty_content", true)
			if backupErr != nil {
				if trace != nil {
					trace.Generation.BackupSucceeded = false
					trace.Generation.BackupFailureReason = backupErr.Error()
				}
				return nil, backupErr
			}

			resp = backupResp
			usedModel = backupUsedModel
			if trace != nil {
				trace.Generation.BackupSucceeded = true
				trace.Generation.BackupUsedModel = backupRequestedModel
			}
		} else {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = "primary model returned empty recap content"
			}
			return nil, errors.New("primary model returned empty recap content")
		}
	}

	if len(resp.Choices) != 0 && strings.TrimSpace(resp.Choices[0].Message.Content) != "" {
		normalizedContent, normalizeErr := c.normalizeSummaryJSONContent(ctx, resp.Choices[0].Message.Content, trace)
		if normalizeErr != nil {
			if len(c.recapBackupModels()) > 0 && (trace == nil || !trace.Generation.BackupUsed) {
				if trace != nil {
					trace.Generation.PrimaryFailed = true
					trace.Generation.PrimaryFailureReason = normalizeErr.Error()
					trace.Generation.BackupUsed = true
				}

				c.logger.Warn("failed to normalize summary json on primary model, retrying backup model",
					zap.String("primary_model", c.modelName),
					zap.String("backup_models", c.modelNameBackup),
					zap.String("used_model", usedModel),
					zap.Error(normalizeErr),
				)

				backupResp, backupRequestedModel, backupUsedModel, backupErr := tryRecapBackupModels("recap_backup_after_normalize_failure", true)
				if backupErr != nil {
					if trace != nil {
						trace.Generation.BackupSucceeded = false
						trace.Generation.BackupFailureReason = backupErr.Error()
					}
					return nil, backupErr
				}

				resp = backupResp
				usedModel = backupUsedModel
				if trace != nil {
					trace.Generation.BackupSucceeded = true
					trace.Generation.BackupUsedModel = backupRequestedModel
				}
			} else {
				if trace != nil {
					trace.Generation.PrimaryFailed = true
					trace.Generation.PrimaryFailureReason = normalizeErr.Error()
				}
				return nil, normalizeErr
			}
		} else {
			resp.Choices[0].Message.Content = normalizedContent
			c.logVerboseJSONBody("recap.normalized_output", normalizedContent)
		}
	}

	if trace != nil && trace.Generation.PrimaryUsedModel == "" && !trace.Generation.BackupUsed {
		trace.Generation.PrimaryUsedModel = usedModel
	}

	if c.enableMetricRecordForTokens {
		err = c.ent.MetricOpenAIChatCompletionTokenUsage.
			Create().
			SetPromptOperation("Summarize Chat Histories").
			SetPromptTokenUsage(resp.Usage.PromptTokens).
			SetCompletionTokenUsage(resp.Usage.CompletionTokens).
			SetTotalTokenUsage(resp.Usage.TotalTokens).
			SetModelName(usedModel).
			Exec(ctx)
		if err != nil {
			c.logger.Error("failed to create metric openai chat completion token usage",
				zap.Error(err),
				zap.String("prompt_operation", "Summarize Chat Histories"),
				zap.Int("prompt_token_usage", resp.Usage.PromptTokens),
				zap.Int("completion_token_usage", resp.Usage.CompletionTokens),
				zap.Int("total_token_usage", resp.Usage.TotalTokens),
				zap.String("model_name", usedModel),
			)
		}
	}

	return &resp, nil
}

func (c *OpenAIClient) SarcasticCondense(ctx context.Context, chatHistory string) (string, error) {
	trace := condensedExecutionTraceFromContext(ctx)
	if trace != nil {
		trace.Generation.PrimaryModel = c.sarcasticCondensedModelName
		trace.Generation.BackupModel = c.sarcasticCondensedModelBackup
	}

	if chatHistory == "" {
		return "", nil
	}

	var userPrompt bytes.Buffer
	err := SarcasticCondensedUserPrompt.Execute(&userPrompt, SarcasticCondensedSummaryInputs{
		ChatHistory: chatHistory,
	})
	if err != nil {
		return "", err
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: SarcasticCondensedSystemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: userPrompt.String(),
		},
	}

	temperature := float32(0.7)
	usedModel := c.sarcasticCondensedModelName
	tryRepairInvalidCondensedOutput := func(rawOutput string) (string, bool, error) {
		if strings.TrimSpace(rawOutput) == "" || c.checkModelName == "" {
			return "", false, nil
		}

		checked, checkUsedModel, checkBackupTried, checkErr := c.repairCondensedOutputByCheckModel(ctx, rawOutput)
		if checkErr != nil {
			c.logWarn("check model failed to repair condensed output",
				zap.Error(checkErr),
				zap.String("check_model", c.checkModelName),
			)
			return "", true, checkErr
		}

		normalized, normalizeErr := normalizeCondensedOutputContent(checked)
		if normalizeErr != nil {
			c.logWarn("check model repaired condensed output is still invalid",
				zap.Error(normalizeErr),
				zap.String("check_model", checkUsedModel),
			)
			return "", true, normalizeErr
		}

		c.logInfo("check model repaired condensed output",
			zap.String("check_model", checkUsedModel),
			zap.Bool("check_backup_used", checkBackupTried),
		)

		return normalized, true, nil
	}

	tryCondensedBackupModels := func() (openai.ChatCompletionResponse, string, string, string, error) {
		var lastErr error
		var lastInvalidOutput string
		var lastInvalidResp openai.ChatCompletionResponse
		var lastInvalidRequestedModel string
		var lastInvalidUsedModel string

		for _, backupModel := range c.condensedBackupModels() {
			backupResp, backupErr := c.callChatCompletionWithModel(
				ctx,
				backupModel,
				messages,
				&temperature,
			)
			if backupErr != nil {
				lastErr = backupErr
				continue
			}
			if len(backupResp.Choices) == 0 || strings.TrimSpace(backupResp.Choices[0].Message.Content) == "" {
				lastErr = errors.New("no content generated from backup model")
				continue
			}

			normalizedContent, normalizeErr := normalizeCondensedOutputContent(backupResp.Choices[0].Message.Content)
			if normalizeErr != nil {
				usedBackupModel := backupModel
				if backupResp.Model != "" {
					usedBackupModel = backupResp.Model
				}

				lastErr = normalizeErr
				lastInvalidOutput = backupResp.Choices[0].Message.Content
				lastInvalidResp = backupResp
				lastInvalidRequestedModel = backupModel
				lastInvalidUsedModel = usedBackupModel

				c.logger.Warn("backup model generated invalid condensed output",
					zap.String("backup_model", backupModel),
					zap.String("used_model", usedBackupModel),
					zap.Error(normalizeErr),
				)
				continue
			}

			backupResp.Choices[0].Message.Content = normalizedContent
			usedBackupModel := backupModel
			if backupResp.Model != "" {
				usedBackupModel = backupResp.Model
			}

			return backupResp, backupModel, usedBackupModel, "", nil
		}

		if lastErr == nil {
			lastErr = errors.New("all backup condensed models failed")
		}

		if strings.TrimSpace(lastInvalidOutput) != "" {
			return lastInvalidResp, lastInvalidRequestedModel, lastInvalidUsedModel, lastInvalidOutput, lastErr
		}

		return openai.ChatCompletionResponse{}, "", "", "", lastErr
	}

	resp, err := c.callChatCompletionWithModel(
		ctx,
		c.sarcasticCondensedModelName,
		messages,
		&temperature,
	)
	if err != nil {
		if shouldFallbackByError(err) && len(c.condensedBackupModels()) > 0 {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = err.Error()
				trace.Generation.BackupUsed = true
			}

			c.logger.Warn("primary model failed, switching to backup model for sarcastic condense",
				zap.String("primary_model", c.sarcasticCondensedModelName),
				zap.String("backup_models", c.sarcasticCondensedModelBackup),
				zap.Error(err),
			)

			backupResp, backupRequestedModel, backupUsedModel, backupInvalidOutput, backupErr := tryCondensedBackupModels()
			if backupErr != nil {
				if trace != nil {
					trace.Generation.BackupSucceeded = false
					trace.Generation.BackupFailureReason = backupErr.Error()
					if backupRequestedModel != "" {
						trace.Generation.BackupUsedModel = backupRequestedModel
					}
				}

				if strings.TrimSpace(backupInvalidOutput) != "" {
					resp = backupResp
					usedModel = backupUsedModel
				} else {
					return "", backupErr
				}
			}

			if backupErr == nil {
				resp = backupResp
				usedModel = backupUsedModel
				if trace != nil {
					trace.Generation.BackupSucceeded = true
					trace.Generation.BackupUsedModel = backupRequestedModel
				}
			}
		} else {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = err.Error()
			}
			return "", err
		}
	}

	if c.forceCondensedPrimaryFailureForTest &&
		len(c.condensedBackupModels()) > 0 &&
		(trace == nil || !trace.Generation.BackupUsed) {
		forcedErr := errors.New("forced condensed primary failure via env switch")
		c.logger.Warn("force condensed primary failure test mode is enabled, switching to backup model",
			zap.String("primary_model", c.sarcasticCondensedModelName),
			zap.String("backup_models", c.sarcasticCondensedModelBackup),
		)

		if trace != nil {
			trace.Generation.PrimaryFailed = true
			trace.Generation.PrimaryFailureReason = forcedErr.Error()
			trace.Generation.BackupUsed = true
		}

		backupResp, backupRequestedModel, backupUsedModel, backupInvalidOutput, backupErr := tryCondensedBackupModels()
		if backupErr != nil {
			if trace != nil {
				trace.Generation.BackupSucceeded = false
				trace.Generation.BackupFailureReason = backupErr.Error()
				if backupRequestedModel != "" {
					trace.Generation.BackupUsedModel = backupRequestedModel
				}
			}

			if strings.TrimSpace(backupInvalidOutput) != "" {
				resp = backupResp
				usedModel = backupUsedModel
			} else {
				return "", backupErr
			}
		}

		if backupErr == nil {
			resp = backupResp
			usedModel = backupUsedModel
			if trace != nil {
				trace.Generation.BackupSucceeded = true
				trace.Generation.BackupUsedModel = backupRequestedModel
			}
		}
	}

	if resp.Model != "" {
		usedModel = resp.Model
	}
	if trace != nil && !trace.Generation.BackupUsed {
		trace.Generation.PrimaryUsedModel = usedModel
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		if len(c.condensedBackupModels()) > 0 && (trace == nil || !trace.Generation.BackupUsed) {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = "no content generated from primary model"
				trace.Generation.BackupUsed = true
			}

			backupResp, backupRequestedModel, backupUsedModel, backupInvalidOutput, backupErr := tryCondensedBackupModels()
			if backupErr != nil {
				if trace != nil {
					trace.Generation.BackupSucceeded = false
					trace.Generation.BackupFailureReason = backupErr.Error()
					if backupRequestedModel != "" {
						trace.Generation.BackupUsedModel = backupRequestedModel
					}
				}

				if strings.TrimSpace(backupInvalidOutput) != "" {
					resp = backupResp
					usedModel = backupUsedModel
				} else {
					return "", backupErr
				}
			}

			if backupErr == nil {
				resp = backupResp
				usedModel = backupUsedModel
				if trace != nil {
					trace.Generation.BackupSucceeded = true
					trace.Generation.BackupUsedModel = backupRequestedModel
				}
			}
		} else {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = "no content generated from primary model"
			}
			return "", fmt.Errorf("no content generated")
		}
	}

	var invalidOutputForRepair string
	normalizedContent, normalizeErr := normalizeCondensedOutputContent(resp.Choices[0].Message.Content)
	if normalizeErr == nil {
		resp.Choices[0].Message.Content = normalizedContent
	} else {
		invalidOutputForRepair = resp.Choices[0].Message.Content
		c.logger.Warn("condensed output is invalid",
			zap.String("used_model", usedModel),
			zap.Error(normalizeErr),
		)

		if len(c.condensedBackupModels()) > 0 && (trace == nil || !trace.Generation.BackupUsed) {
			if trace != nil {
				trace.Generation.PrimaryFailed = true
				trace.Generation.PrimaryFailureReason = normalizeErr.Error()
				trace.Generation.BackupUsed = true
			}

			c.logger.Warn("primary model generated invalid condensed output, retrying with backup model",
				zap.String("primary_model", c.sarcasticCondensedModelName),
				zap.String("backup_models", c.sarcasticCondensedModelBackup),
				zap.String("used_model", usedModel),
				zap.Error(normalizeErr),
			)

			backupResp, backupRequestedModel, backupUsedModel, backupInvalidOutput, backupErr := tryCondensedBackupModels()
			if backupErr == nil {
				resp = backupResp
				usedModel = backupUsedModel
				if trace != nil {
					trace.Generation.BackupSucceeded = true
					trace.Generation.BackupUsedModel = backupRequestedModel
				}
				normalizeErr = nil
			} else {
				if trace != nil {
					trace.Generation.BackupSucceeded = false
					trace.Generation.BackupFailureReason = backupErr.Error()
					if backupRequestedModel != "" {
						trace.Generation.BackupUsedModel = backupRequestedModel
					}
				}
				if strings.TrimSpace(backupInvalidOutput) != "" {
					invalidOutputForRepair = backupInvalidOutput
					resp = backupResp
					usedModel = backupUsedModel
				}
			}
		}

		if normalizeErr != nil {
			repairedOutput, attempted, repairErr := tryRepairInvalidCondensedOutput(invalidOutputForRepair)
			if attempted {
				if repairErr != nil {
					return "", repairErr
				}

				resp.Choices[0].Message.Content = repairedOutput
				normalizeErr = nil
			}
		}

		if normalizeErr != nil {
			return "", normalizeErr
		}
	}

	if trace != nil && trace.Generation.PrimaryUsedModel == "" && !trace.Generation.BackupUsed {
		trace.Generation.PrimaryUsedModel = usedModel
	}

	if c.enableMetricRecordForTokens {
		err = c.ent.MetricOpenAIChatCompletionTokenUsage.
			Create().
			SetPromptOperation("Sarcastic Condense").
			SetPromptTokenUsage(resp.Usage.PromptTokens).
			SetCompletionTokenUsage(resp.Usage.CompletionTokens).
			SetTotalTokenUsage(resp.Usage.TotalTokens).
			SetModelName(usedModel).
			Exec(ctx)
		if err != nil {
			c.logger.Error("failed to record token usage", zap.Error(err))
		}
	}

	return resp.Choices[0].Message.Content, nil
}
