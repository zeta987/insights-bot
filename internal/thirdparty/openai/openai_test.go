package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/lib"
	"github.com/nekomeowww/insights-bot/pkg/tutils"
	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

var client *OpenAIClient

func TestDefaultCondensedPromptsRequestOneLinerRoast(t *testing.T) {
	require.Contains(t, SarcasticCondensedSystemPrompt, "一句话")
	require.Contains(t, SarcasticCondensedSystemPrompt, "歇后语")
	require.Contains(t, SarcasticCondensedSystemPrompt, "不允许分行")
	require.Contains(t, CheckCondensedOutputSystemPrompt, "ONE line")
}

func TestNormalizeStructuredCondensedOutputAcceptsSingleRoastLine(t *testing.T) {
	roast := "**群友**在*深夜*把部署聊成了玄學——`CHECK_MODEL` 都看不下去了。"

	normalized, err := normalizeStructuredCondensedOutputContent(roast)

	require.NoError(t, err)
	require.Equal(t, roast, normalized)
}

func TestNormalizeStructuredCondensedOutputRejectsHeadingsListsAndMultiline(t *testing.T) {
	_, err := normalizeStructuredCondensedOutputContent("### 主題\n\n* **重點**：內容")
	require.Error(t, err)

	_, err = normalizeStructuredCondensedOutputContent("- 只有一個清單項")
	require.Error(t, err)

	_, err = normalizeStructuredCondensedOutputContent("第一句。\n第二句。")
	require.Error(t, err)
}

func TestMain(m *testing.M) {
	logger, err := lib.NewLogger()(lib.NewLoggerParams{
		Configs: configs.NewTestConfig()(),
	})
	if err != nil {
		panic(err)
	}

	ent, err := datastore.NewEnt()(datastore.NewEntParams{
		Lifecycle: tutils.NewEmtpyLifecycle(),
		Configs:   configs.NewTestConfig()(),
	})
	if err != nil {
		panic(err)
	}

	c, err := NewClient(false)(NewClientParams{
		Logger: logger,
		Config: &configs.Config{
			OpenAI: configs.SectionOpenAI{
				Host:   "",
				Secret: "",
			},
		},
		Ent: ent,
	})
	if err != nil {
		panic(err)
	}

	var ok bool

	client, ok = c.(*OpenAIClient)
	if !ok {
		panic("failed to cast to OpenAIClient")
	}

	os.Exit(m.Run())
}

func TestTruncateContentBasedOnTokens(t *testing.T) {
	tables := []struct {
		textContent string
		limits      int
		expected    string
	}{
		{
			textContent: "小溪河水清澈见底",
			limits:      3,
			expected:    "小溪",
		},
		{
			textContent: "小溪河水清澈见底",
			limits:      4,
			expected:    "小溪",
		},
		{
			textContent: "小溪河水清澈见底",
			limits:      5,
			expected:    "小溪河",
		},
	}

	for _, table := range tables {
		t.Run(table.textContent, func(t *testing.T) {
			actual := client.TruncateContentBasedOnTokens(table.textContent, table.limits)
			require.Equal(t, table.expected, actual)
		})
	}
}

func TestSplitContentBasedOnTokenLimitations(t *testing.T) {
	tables := []struct {
		textContent string
		limits      int
		expected    []string
	}{
		{
			textContent: strings.Repeat("a", 40000),
			limits:      3900,
			expected:    []string{strings.Repeat("a", 31200), strings.Repeat("a", 8800)},
		},
		{
			textContent: "小溪河水清澈见底，沿岸芦苇丛生。远处山峰耸立，白云飘渺。一只黄鹂停在枝头，唱起了优美的歌曲，引来了不少路人驻足欣赏。",
			limits:      20,
			expected:    []string{"小溪河水清澈见底，沿岸芦", "苇丛生。远处山峰耸立，白", "云飘渺。一只黄鹂停在枝头，", "唱起了优美的歌曲，引来了不少路人", "驻足欣赏。"},
		},
	}

	for i, table := range tables {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			actual := client.SplitContentBasedByTokenLimitations(table.textContent, table.limits)
			require.Equal(t, table.expected, actual)
		})
	}
}

func TestShouldFallbackByError(t *testing.T) {
	require.True(t, shouldFallbackByError(errors.New("mock error")))
	require.False(t, shouldFallbackByError(context.Canceled))
}

func TestNewClientBackupModelDefaults(t *testing.T) {
	logger, err := lib.NewLogger()(lib.NewLoggerParams{
		Configs: configs.NewTestConfig()(),
	})
	require.NoError(t, err)

	ent, err := datastore.NewEnt()(datastore.NewEntParams{
		Lifecycle: tutils.NewEmtpyLifecycle(),
		Configs:   configs.NewTestConfig()(),
	})
	require.NoError(t, err)

	c, err := NewClient(false)(NewClientParams{
		Logger: logger,
		Config: &configs.Config{
			OpenAI: configs.SectionOpenAI{
				ModelName:                   "primary-model",
				ModelNameBackup:             "",
				SarcasticCondensedModelName: "",
			},
		},
		Ent: ent,
	})
	require.NoError(t, err)

	impl, ok := c.(*OpenAIClient)
	require.True(t, ok)
	require.Equal(t, "primary-model", impl.modelName)
	require.Equal(t, "primary-model", impl.modelNameBackup)
	require.Equal(t, "primary-model", impl.sarcasticCondensedModelName)
	require.Equal(t, "primary-model", impl.sarcasticCondensedModelBackup)
}

func TestChatHistorySummarizationPromptRequestsRichMarkdown(t *testing.T) {
	var userPrompt strings.Builder
	err := ChatHistorySummarizationUserPrompt.Execute(&userPrompt, NewChatHistorySummarizationPromptInputs(
		"msgId:1: Zeta sent: hello",
		"Traditional Chinese",
	))
	require.NoError(t, err)

	require.Contains(t, ChatHistorySummarizationSystemPrompt, "Rich Markdown")
	require.Contains(t, ChatHistorySummarizationSystemPrompt, "{{tg-ref:1,2}}")
	require.Contains(t, ChatHistorySummarizationSystemPrompt, "Do not output JSON")
	require.NotContains(t, ChatHistorySummarizationSystemPrompt, "JSON Schema")
	require.Contains(t, userPrompt.String(), "Traditional Chinese")
	require.Contains(t, userPrompt.String(), "msgId:1")
}

func TestSummarizeChatHistoriesReturnsRawRichMarkdown(t *testing.T) {
	expected := "## Topic\n\n- Decision reached {{tg-ref:1,2}}"
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/chat/completions", request.URL.Path)
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "Rich Markdown")
		require.NotContains(t, string(body), "JSON Schema")

		writer.Header().Set("Content-Type", "application/json")
		_, err = writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":"resolved-primary-model","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			expected,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		modelName:                    "primary-model",
		modelNameBackup:              "primary-model",
		defaultSummarizationLanguage: "Traditional Chinese",
		client:                       goopenai.NewClientWithConfig(config),
		limiter:                      ratelimit.New(1000),
	}

	trace := &RecapExecutionTrace{}
	response, err := richClient.SummarizeChatHistories(
		WithRecapExecutionTrace(context.Background(), trace),
		"msgId:1: Zeta sent: hello",
		"Traditional Chinese",
	)
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Equal(t, expected, response.Choices[0].Message.Content)
	require.Equal(t, 9, response.Usage.TotalTokens)
	require.Equal(t, "primary-model", trace.Generation.PrimaryModel)
	require.Equal(t, "resolved-primary-model", trace.Generation.PrimaryUsedModel)
}

func TestSummarizeChatHistoriesRecordsResolvedBackupModel(t *testing.T) {
	requestedModels := make([]string, 0, 2)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)

		content := ""
		responseModel := "resolved-detail-primary"
		if payload.Model == "detailBackup" {
			content = "## 主題\n\n- 備援詳細摘要"
			responseModel = "resolved-detail-backup"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		modelName:                    "detailPrimary",
		modelNameBackup:              "detailBackup",
		defaultSummarizationLanguage: "Traditional Chinese",
		client:                       goopenai.NewClientWithConfig(config),
		logger:                       client.logger,
		limiter:                      ratelimit.New(1000),
	}
	trace := &RecapExecutionTrace{}

	response, err := richClient.SummarizeChatHistories(
		WithRecapExecutionTrace(context.Background(), trace),
		"msgId:1: Zeta sent: hello",
		"Traditional Chinese",
	)

	require.NoError(t, err)
	require.Equal(t, "## 主題\n\n- 備援詳細摘要", response.Choices[0].Message.Content)
	require.Equal(t, []string{"detailPrimary", "detailBackup"}, requestedModels)
	require.True(t, trace.Generation.BackupUsed)
	require.True(t, trace.Generation.BackupSucceeded)
	require.Empty(t, trace.Generation.PrimaryUsedModel)
	require.Equal(t, "resolved-detail-backup", trace.Generation.BackupUsedModel)
}

func TestSarcasticCondenseRecordsConfiguredCheckModelWithoutCallingIt(t *testing.T) {
	requestedModels := make([]string, 0, 1)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)
		writer.Header().Set("Content-Type", "application/json")
		content := "**正常濃縮摘要**——一句話銳評把 Rich Markdown 玩明白了。"
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			"resolved-condensed-model",
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedModel",
		sarcasticCondensedModelBackup: "condensedModel",
		checkModelName:                "checkModel",
		checkModelNameBackup:          "checkBackup",
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "正常濃縮摘要")
	require.Equal(t, []string{"condensedModel"}, requestedModels)
	require.Equal(t, "resolved-condensed-model", trace.Generation.PrimaryUsedModel)
	require.Equal(t, "checkModel", trace.Check.Generation.PrimaryModel)
	require.Equal(t, "checkBackup", trace.Check.Generation.BackupModel)
	require.False(t, trace.Check.Attempted)
}

func TestSarcasticCondenseRecordsSuccessfulCheckRepair(t *testing.T) {
	requestedModels := make([]string, 0, 2)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)
		content := `{"summary":"invalid"}`
		if payload.Model == "checkModel" {
			content = "**修復後摘要**——Check 出手把爛攤子縫成一句話。"
		}
		writer.Header().Set("Content-Type", "application/json")
		responseModel := payload.Model
		if payload.Model == "checkModel" {
			responseModel = "resolved-check-model"
		}
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedModel",
		sarcasticCondensedModelBackup: "condensedModel",
		checkModelName:                "checkModel",
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "修復後摘要")
	require.Equal(t, []string{"condensedModel", "checkModel"}, requestedModels)
	require.True(t, trace.Generation.PrimaryFailed)
	require.True(t, trace.Check.Attempted)
	require.True(t, trace.Check.Succeeded)
	require.Equal(t, "resolved-check-model", trace.Check.Generation.PrimaryUsedModel)
}

func TestSarcasticCondenseRecordsResolvedBackupModel(t *testing.T) {
	requestedModels := make([]string, 0, 2)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)

		responseModel := "resolved-condensed-primary"
		content := "**主摘要**——主模型一句話全包。"
		if payload.Model == "condensedBackup" {
			responseModel = "resolved-condensed-backup"
			content = "**備援摘要**——備援模型接鍋成功，一句話收工。"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:         "condensedPrimary",
		sarcasticCondensedModelBackup:       "condensedBackup",
		forceCondensedPrimaryFailureForTest: true,
		client:                              goopenai.NewClientWithConfig(config),
		logger:                              client.logger,
		limiter:                             ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "備援摘要")
	require.Equal(t, []string{"condensedPrimary", "condensedBackup"}, requestedModels)
	require.True(t, trace.Generation.BackupUsed)
	require.True(t, trace.Generation.BackupSucceeded)
	require.Empty(t, trace.Generation.PrimaryUsedModel)
	require.Equal(t, "resolved-condensed-backup", trace.Generation.BackupUsedModel)
}

func TestSarcasticCondenseDoesNotRecordStructurallyInvalidPrimaryAsUsed(t *testing.T) {
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":"resolved-invalid-primary","choices":[{"index":0,"message":{"role":"assistant","content":"### 主題\n- 這仍是舊的清單結構"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName: "condensedPrimary",
		client:                      goopenai.NewClientWithConfig(config),
		logger:                      client.logger,
		limiter:                     ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.Error(t, err)
	require.Empty(t, result)
	require.True(t, trace.Generation.PrimaryFailed)
	require.Empty(t, trace.Generation.PrimaryUsedModel)
}

func TestSarcasticCondenseCheckRepairsResolvedBackupSourceOnly(t *testing.T) {
	requestedModels := make([]string, 0, 3)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)

		responseModel := "resolved-condensed-primary"
		content := "- 主模型只回了一個清單項"
		switch payload.Model {
		case "condensedBackup":
			responseModel = "resolved-condensed-backup"
			content = "- 備援模型也只回了一個清單項"
		case "checkModel":
			responseModel = "resolved-check-model"
			content = "**修復後摘要**——Check 把備援內容縫成一句話。"
		}

		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedPrimary",
		sarcasticCondensedModelBackup: "condensedBackup",
		checkModelName:                "checkModel",
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "修復後摘要")
	require.Equal(t, []string{"condensedPrimary", "condensedBackup", "checkModel"}, requestedModels)
	require.Empty(t, trace.Generation.PrimaryUsedModel)
	require.True(t, trace.Generation.BackupUsed)
	require.False(t, trace.Generation.BackupSucceeded)
	require.Equal(t, "resolved-condensed-backup", trace.Generation.BackupUsedModel)
	require.True(t, trace.Check.Succeeded)
	require.Equal(t, "resolved-check-model", trace.Check.Generation.PrimaryUsedModel)
}

func TestSarcasticCondenseCheckRepairsPrimaryAfterBackupTransportFailure(t *testing.T) {
	requestedModels := make([]string, 0, 3)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)

		if payload.Model == "condensedBackup" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadGateway)
			_, err := writer.Write([]byte(`{"error":{"message":"backup unavailable","type":"server_error"}}`))
			require.NoError(t, err)
			return
		}

		responseModel := "resolved-condensed-primary"
		content := "- 主模型只回了一個清單項"
		if payload.Model == "checkModel" {
			responseModel = "resolved-check-model"
			content = "**修復後摘要**——Check 把主模型內容縫成一句話。"
		}

		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedPrimary",
		sarcasticCondensedModelBackup: "condensedBackup",
		checkModelName:                "checkModel",
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "修復後摘要")
	require.Equal(t, []string{"condensedPrimary", "condensedBackup", "checkModel"}, requestedModels)
	require.Equal(t, "resolved-condensed-primary", trace.Generation.PrimaryUsedModel)
	require.True(t, trace.Generation.BackupUsed)
	require.False(t, trace.Generation.BackupSucceeded)
	require.Empty(t, trace.Generation.BackupUsedModel)
	require.True(t, trace.Check.Succeeded)
	require.Equal(t, "resolved-check-model", trace.Check.Generation.PrimaryUsedModel)
}

func TestSarcasticCondenseUsesCheckBackupWhenPrimaryCheckOutputIsInvalid(t *testing.T) {
	requestedModels := make([]string, 0, 3)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		model, _ := payload["model"].(string)
		requestedModels = append(requestedModels, model)

		content := "{\"summary\":\"invalid\"}"
		if model == "checkPrimary" {
			content = "### 修復標題\n\n- 主 Check 仍回舊的清單結構"
		}
		if model == "checkBackup" {
			content = "**修復後摘要**——備援 Check 出手就是一句話。"
		}
		responseModel := model
		if model == "checkBackup" {
			responseModel = "resolved-check-backup"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			"{\"id\":\"chatcmpl-test\",\"object\":\"chat.completion\",\"created\":1730000000,\"model\":%q,\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":5,\"total_tokens\":9}}",
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedModel",
		sarcasticCondensedModelBackup: "condensedModel",
		checkModelName:                "checkPrimary",
		checkModelNameBackup:          "checkBackup",
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "修復後摘要")
	require.Equal(t, []string{"condensedModel", "checkPrimary", "checkBackup"}, requestedModels)
	require.True(t, trace.Check.Attempted)
	require.True(t, trace.Check.Succeeded)
	require.True(t, trace.Check.Generation.PrimaryFailed)
	require.Contains(t, trace.Check.Generation.PrimaryFailureReason, "not a single line")
	require.True(t, trace.Check.Generation.BackupUsed)
	require.True(t, trace.Check.Generation.BackupSucceeded)
	require.Equal(t, "resolved-check-backup", trace.Check.Generation.BackupUsedModel)
}

func TestSarcasticCondenseForcedCheckFailureSwitchesToCheckBackup(t *testing.T) {
	requestedModels := make([]string, 0, 2)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		model, _ := payload["model"].(string)
		requestedModels = append(requestedModels, model)

		content := "{\"summary\":\"invalid\"}"
		if model == "checkBackup" {
			content = "**強制切換後摘要**——備援 Check 順利接管全場。"
		}
		responseModel := model
		if model == "checkBackup" {
			responseModel = "resolved-forced-check-backup"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(fmt.Sprintf(
			"{\"id\":\"chatcmpl-test\",\"object\":\"chat.completion\",\"created\":1730000000,\"model\":%q,\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":5,\"total_tokens\":9}}",
			responseModel,
			content,
		)))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	richClient := &OpenAIClient{
		sarcasticCondensedModelName:   "condensedModel",
		sarcasticCondensedModelBackup: "condensedModel",
		checkModelName:                "checkPrimary",
		checkModelNameBackup:          "checkBackup",
		forceCheckModelFailure:        true,
		client:                        goopenai.NewClientWithConfig(config),
		logger:                        client.logger,
		limiter:                       ratelimit.New(1000),
	}
	trace := &CondensedExecutionTrace{}

	result, err := richClient.SarcasticCondense(WithCondensedExecutionTrace(context.Background(), trace), "TestUser: 測試")

	require.NoError(t, err)
	require.Contains(t, result, "強制切換後摘要")
	require.Equal(t, []string{"condensedModel", "checkBackup"}, requestedModels)
	require.True(t, trace.Check.Succeeded)
	require.True(t, trace.Check.Generation.PrimaryFailed)
	require.Contains(t, trace.Check.Generation.PrimaryFailureReason, "forced failure")
	require.True(t, trace.Check.Generation.BackupUsed)
	require.True(t, trace.Check.Generation.BackupSucceeded)
	require.Equal(t, "resolved-forced-check-backup", trace.Check.Generation.BackupUsedModel)
}
