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

func TestFormatRecapMergePartsNumbersNonEmptyRecaps(t *testing.T) {
	parts := formatRecapMergeParts([]string{" ## A\n\n- a {{tg-ref:1}} ", "", "  ", "## B"})

	require.Equal(t, "Part 1 of 2:\n## A\n\n- a {{tg-ref:1}}\n\nPart 2 of 2:\n## B", parts)
}

func TestMergeChatHistoriesRecapsSendsPartsAndKeepsMarkers(t *testing.T) {
	expected := "## Topic\n\n- Merged decision {{tg-ref:1,3}}"
	requestCount := 0
	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++

		var payload struct {
			Model    string                           `json:"model"`
			Messages []goopenai.ChatCompletionMessage `json:"messages"`
		}

		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.Equal(t, "primary-model", payload.Model)
		require.Len(t, payload.Messages, 2)
		require.Equal(t, ChatHistoriesRecapMergeSystemPrompt, payload.Messages[0].Content)
		require.Contains(t, payload.Messages[1].Content, "Traditional Chinese")
		require.Contains(t, payload.Messages[1].Content, "Part 1 of 2:\n## Earlier\n\n- Old decision {{tg-ref:1}}")
		require.Contains(t, payload.Messages[1].Content, "Part 2 of 2:\n## Newer\n\n- New decision {{tg-ref:3}}")

		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(writer,
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":"resolved-primary-model","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			expected,
		)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"
	client := &OpenAIClient{
		modelName:       "primary-model",
		modelNameBackup: "primary-model",
		client:          goopenai.NewClientWithConfig(config),
		limiter:         ratelimit.New(1000),
	}

	trace := &RecapExecutionTrace{}
	response, err := client.MergeChatHistoriesRecaps(
		WithRecapExecutionTrace(context.Background(), trace),
		[]string{"## Earlier\n\n- Old decision {{tg-ref:1}}", "## Newer\n\n- New decision {{tg-ref:3}}"},
		"Traditional Chinese",
	)
	require.NoError(t, err)
	require.Equal(t, 1, requestCount)
	require.Len(t, response.Choices, 1)
	require.Equal(t, expected, response.Choices[0].Message.Content)
	require.Equal(t, "primary-model", trace.Generation.PrimaryModel)
	require.Equal(t, "resolved-primary-model", trace.Generation.PrimaryUsedModel)
}

func TestMergeChatHistoriesRecapsRejectsEmptyInput(t *testing.T) {
	client := &OpenAIClient{modelName: "primary-model"}

	response, err := client.MergeChatHistoriesRecaps(context.Background(), []string{"", "  "}, "")
	require.Error(t, err)
	require.Nil(t, response)
}
