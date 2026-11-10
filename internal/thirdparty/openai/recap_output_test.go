package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

func recapResponse(content string, finishReason goopenai.FinishReason) goopenai.ChatCompletionResponse {
	return goopenai.ChatCompletionResponse{
		Choices: []goopenai.ChatCompletionChoice{{
			Message:      goopenai.ChatCompletionMessage{Role: goopenai.ChatMessageRoleAssistant, Content: content},
			FinishReason: finishReason,
		}},
	}
}

func TestRecapOutputProblem(t *testing.T) {
	leakedScratch := "chat_ids = [ 1,2,3 ]\nprint(\"Total messages:\", len(chat_ids))\nTotal messages: 3\nimport re\nvalid_ids = { 1,2,3 }\ndraft = \"\"\"## 主題\n- 重點 {{tg-ref:1}}"

	testCases := []struct {
		name     string
		response goopenai.ChatCompletionResponse
		expected string
	}{
		{
			name:     "UsableRecap",
			response: recapResponse("## 主題\n\n- 群友討論 `print()` 與 a = b 的寫法 {{tg-ref:1,2}}", goopenai.FinishReasonStop),
		},
		{
			name:     "ParagraphOnlyRecap",
			response: recapResponse("群友主要聊了額度重置。", goopenai.FinishReasonStop),
		},
		{
			name:     "NoChoices",
			response: goopenai.ChatCompletionResponse{},
			expected: "empty recap content",
		},
		{
			name:     "BlankContent",
			response: recapResponse("  \n ", goopenai.FinishReasonStop),
			expected: "empty recap content",
		},
		{
			name:     "CodeFence",
			response: recapResponse("```python\nids = [1]\n```\n## 主題\n- 重點", goopenai.FinishReasonStop),
			expected: "recap content with a code fence",
		},
		{
			name:     "LeakedWorkingNotes",
			response: recapResponse(leakedScratch, goopenai.FinishReasonStop),
			expected: "recap content with code-like working notes",
		},
		{
			name:     "CutOffByOutputLimit",
			response: recapResponse("## 主題\n\n- 重點 {{tg-ref:1}}\n\n## 下一個主題", goopenai.FinishReasonLength),
			expected: "recap content cut off by the output limit",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, recapOutputProblem(testCase.response))
		})
	}
}

func TestSummarizeChatHistoriesSwitchesToBackupWhenPrimaryLeaksWorkingNotes(t *testing.T) {
	requestedModels := make([]string, 0, 2)
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}

		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestedModels = append(requestedModels, payload.Model)

		content := "chat_ids = [ 1,2 ]\nimport re\ndraft = \"\"\"## 主題\n- 草稿 {{tg-ref:1}}"
		responseModel := "resolved-detail-primary"

		if payload.Model == "detailBackup" {
			content = "## 主題\n\n- 備援詳細摘要 {{tg-ref:1}}"
			responseModel = "resolved-detail-backup"
		}

		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(writer,
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			responseModel,
			content,
		)
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
	require.Equal(t, "## 主題\n\n- 備援詳細摘要 {{tg-ref:1}}", response.Choices[0].Message.Content)
	require.Equal(t, []string{"detailPrimary", "detailBackup"}, requestedModels)
	require.True(t, trace.Generation.PrimaryFailed)
	require.Equal(t, "primary model returned recap content with code-like working notes", trace.Generation.PrimaryFailureReason)
	require.True(t, trace.Generation.BackupSucceeded)
	require.Equal(t, "resolved-detail-backup", trace.Generation.BackupUsedModel)
}

func TestSummarizeChatHistoriesFailsWhenEveryModelLeaksWorkingNotes(t *testing.T) {
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(writer,
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":"resolved","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			"valid_ids = { 1,2 }\n## 主題\n- 草稿",
		)
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

	response, err := richClient.SummarizeChatHistories(context.Background(), "msgId:1: Zeta sent: hello", "Traditional Chinese")

	require.ErrorContains(t, err, "backup model returned recap content with code-like working notes")
	require.Nil(t, response)
}
