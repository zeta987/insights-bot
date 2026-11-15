package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

type sseToolCall struct {
	id        string
	name      string
	arguments string
}

// writeToolCallStream answers like a streaming API that calls tools: the name
// first, then the arguments in two pieces, then a finish chunk.
func writeToolCallStream(t *testing.T, writer http.ResponseWriter, calls []sseToolCall) {
	t.Helper()

	writer.Header().Set("Content-Type", "text/event-stream")

	send := func(chunk any) {
		data, err := json.Marshal(chunk)
		require.NoError(t, err)

		_, err = fmt.Fprintf(writer, "data: %s\n\n", data)
		require.NoError(t, err)
	}

	for index, call := range calls {
		// Split on a character boundary: each chunk carries valid UTF-8.
		arguments := []rune(call.arguments)
		half := len(arguments) / 2

		send(map[string]any{"id": "chatcmpl-review", "model": "resolved-check", "choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"index": index, "id": call.id, "type": "function",
				"function": map[string]any{"name": call.name, "arguments": string(arguments[:half])},
			}}},
		}}})
		send(map[string]any{"id": "chatcmpl-review", "choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": index, "function": map[string]any{"arguments": string(arguments[half:])},
			}}},
		}}})
	}

	send(map[string]any{"id": "chatcmpl-review", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}})

	_, err := fmt.Fprint(writer, "data: [DONE]\n\n")
	require.NoError(t, err)
}

type reviewTestServer struct {
	mu       sync.Mutex
	requests []map[string]any
}

func newReviewTestClient(t *testing.T, turns [][]sseToolCall) (*OpenAIClient, *reviewTestServer) {
	t.Helper()

	recorded := &reviewTestServer{}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any

		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))

		recorded.mu.Lock()
		turn := len(recorded.requests)
		recorded.requests = append(recorded.requests, payload)
		recorded.mu.Unlock()

		require.Less(t, turn, len(turns), "unexpected extra review turn")
		writeToolCallStream(t, writer, turns[turn])
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"

	return &OpenAIClient{
		modelName:                    "primary-model",
		checkModelName:               "check-model",
		defaultSummarizationLanguage: "Traditional Chinese",
		client:                       goopenai.NewClientWithConfig(config),
		logger:                       client.logger,
		limiter:                      ratelimit.New(1000),
		mediaLimiter:                 ratelimit.New(1000),
	}, recorded
}

func TestCreateChatCompletionAssemblesStreamedToolCalls(t *testing.T) {
	reviewClient, _ := newReviewTestClient(t, [][]sseToolCall{{
		{id: "call_1", name: "rerender_image", arguments: `{"rendering":"light_background"}`},
		{id: "call_2", name: "accept_description", arguments: `{"description":"天"}`},
	}})

	resp, err := reviewClient.createChatCompletion(context.Background(), goopenai.ChatCompletionRequest{Model: "check-model"})

	require.NoError(t, err)
	require.Equal(t, goopenai.FinishReasonToolCalls, resp.Choices[0].FinishReason)

	calls := resp.Choices[0].Message.ToolCalls
	require.Len(t, calls, 2)
	require.Equal(t, "call_1", calls[0].ID)
	require.Equal(t, "rerender_image", calls[0].Function.Name)
	require.JSONEq(t, `{"rendering":"light_background"}`, calls[0].Function.Arguments)
	require.JSONEq(t, `{"description":"天"}`, calls[1].Function.Arguments)
}

func TestReviewMediaDescriptionRendersBeforeAccepting(t *testing.T) {
	reviewClient, recorded := newReviewTestClient(t, [][]sseToolCall{
		{{id: "call_1", name: "rerender_image", arguments: `{"rendering":"light_background"}`}},
		{{id: "call_2", name: "accept_description", arguments: `{"description":"漢字「天」，表示驚嘆"}`}},
	})

	rendered := make([]MediaRendering, 0)

	description, err := reviewClient.ReviewMediaDescription(context.Background(), MediaReviewRequest{
		MediaDescriptionRequest: MediaDescriptionRequest{Kind: MediaKindSticker, MimeType: "image/png", Data: []byte("raw"), Hint: "It stands for the emoji 😳."},
		Draft:                   "神秘黑色像素剪影",
		Render: func(rendering MediaRendering) ([]byte, string, error) {
			rendered = append(rendered, rendering)
			return []byte("rendered"), "image/png", nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, "漢字「天」，表示驚嘆", description)
	require.Equal(t, []MediaRendering{MediaRenderingLightBackground}, rendered)
	require.Len(t, recorded.requests, 2)
	require.Equal(t, "check-model", recorded.requests[0]["model"])
	require.Equal(t, "required", recorded.requests[0]["tool_choice"])

	messages, ok := recorded.requests[1]["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 5, "system, first image, tool call, tool result, new rendering")

	toolResult, ok := messages[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "call_1", toolResult["tool_call_id"])

	newRendering, ok := messages[4].(map[string]any)
	require.True(t, ok)
	require.Contains(t, fmt.Sprint(newRendering["content"]), "data:image/png;base64,cmVuZGVyZWQ=")
}

func TestReviewMediaDescriptionReportsUnreadableWhenAcceptedEmpty(t *testing.T) {
	reviewClient, _ := newReviewTestClient(t, [][]sseToolCall{
		{{id: "call_1", name: "accept_description", arguments: `{"description":""}`}},
	})

	_, err := reviewClient.ReviewMediaDescription(context.Background(), MediaReviewRequest{
		MediaDescriptionRequest: MediaDescriptionRequest{Kind: MediaKindPhoto, MimeType: "image/png", Data: []byte("raw")},
	})

	require.ErrorIs(t, err, ErrMediaUnreadable)
}

func TestReviewMediaDescriptionNeedsCheckModel(t *testing.T) {
	_, err := client.ReviewMediaDescription(context.Background(), MediaReviewRequest{
		MediaDescriptionRequest: MediaDescriptionRequest{Kind: MediaKindSticker, MimeType: "image/png", Data: []byte("raw")},
		Draft:                   "label",
	})

	require.ErrorIs(t, err, ErrMediaReviewUnavailable)
}
