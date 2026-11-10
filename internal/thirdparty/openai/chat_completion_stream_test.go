package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

func writeStreamEvent(writer io.Writer, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(writer, "data: %s\n\n", encoded)

	return err
}

// streamChatCompletions lets a test handler keep answering with a regular chat
// completion body and replays it to the client as a server-sent event stream.
// Non-200 answers pass through untouched.
func streamChatCompletions(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := httptest.NewRecorder()
		handler(recorder, request)

		if recorder.Code != http.StatusOK {
			for key, values := range recorder.Header() {
				writer.Header()[key] = values
			}
			writer.WriteHeader(recorder.Code)
			_, _ = writer.Write(recorder.Body.Bytes())

			return
		}

		var completion goopenai.ChatCompletionResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &completion); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}

		chunk := func(choices []goopenai.ChatCompletionStreamChoice, usage *goopenai.Usage) goopenai.ChatCompletionStreamResponse {
			return goopenai.ChatCompletionStreamResponse{
				ID:      completion.ID,
				Object:  "chat.completion.chunk",
				Created: completion.Created,
				Model:   completion.Model,
				Choices: choices,
				Usage:   usage,
			}
		}

		events := make([]goopenai.ChatCompletionStreamResponse, 0, len(completion.Choices)*3+1)
		for _, choice := range completion.Choices {
			// Split in the middle of the text so the client has to join deltas.
			runes := []rune(choice.Message.Content)
			half := len(runes) / 2

			events = append(events,
				chunk([]goopenai.ChatCompletionStreamChoice{{
					Index: choice.Index,
					Delta: goopenai.ChatCompletionStreamChoiceDelta{Role: goopenai.ChatMessageRoleAssistant, Content: string(runes[:half])},
				}}, nil),
				chunk([]goopenai.ChatCompletionStreamChoice{{
					Index: choice.Index,
					Delta: goopenai.ChatCompletionStreamChoiceDelta{Content: string(runes[half:])},
				}}, nil),
				chunk([]goopenai.ChatCompletionStreamChoice{{
					Index:        choice.Index,
					FinishReason: choice.FinishReason,
				}}, nil),
			)
		}

		usage := completion.Usage
		events = append(events, chunk([]goopenai.ChatCompletionStreamChoice{}, &usage))

		writer.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			if err := writeStreamEvent(writer, event); err != nil {
				return
			}
		}

		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	})
}

func newStreamTestClient(serverURL string) *OpenAIClient {
	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = serverURL + "/v1"

	return &OpenAIClient{
		client:  goopenai.NewClientWithConfig(config),
		limiter: ratelimit.New(1000),
	}
}

func TestCreateChatCompletionAssemblesStreamedChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.True(t, payload.Stream)
		require.True(t, payload.StreamOptions.IncludeUsage)

		writer.Header().Set("Content-Type", "text/event-stream")
		_, err := io.WriteString(writer, ""+
			`data: {"id":"chatcmpl-stream","created":1730000000,"model":"resolved-model","choices":[{"index":0,"delta":{"role":"assistant","content":"## 主"},"finish_reason":null}]}`+"\n\n"+
			": keep-alive\n\n"+
			`data: {"id":"chatcmpl-stream","created":1730000000,"model":"resolved-model","choices":[{"index":0,"delta":{"content":"題\n\n- **重點** 🎉"},"finish_reason":null}]}`+"\n\n"+
			`data: {"id":"chatcmpl-stream","created":1730000000,"model":"resolved-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			`data: {"id":"chatcmpl-stream","created":1730000000,"model":"resolved-model","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`+"\n\n"+
			"data: [DONE]\n\n",
		)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	response, err := newStreamTestClient(server.URL).createChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model:    "primary-model",
		Messages: []goopenai.ChatCompletionMessage{{Role: goopenai.ChatMessageRoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Equal(t, "## 主題\n\n- **重點** 🎉", response.Choices[0].Message.Content)
	require.Equal(t, goopenai.ChatMessageRoleAssistant, response.Choices[0].Message.Role)
	require.Equal(t, goopenai.FinishReasonStop, response.Choices[0].FinishReason)
	require.Equal(t, "chatcmpl-stream", response.ID)
	require.Equal(t, "resolved-model", response.Model)
	require.Equal(t, 4, response.Usage.PromptTokens)
	require.Equal(t, 5, response.Usage.CompletionTokens)
	require.Equal(t, 9, response.Usage.TotalTokens)
}

func TestCreateChatCompletionRejectsStreamWithoutFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		// The connection closes after a partial message: no finish reason, no [DONE].
		_, err := io.WriteString(writer,
			`data: {"id":"chatcmpl-stream","model":"resolved-model","choices":[{"index":0,"delta":{"role":"assistant","content":"**半截"},"finish_reason":null}]}`+"\n\n",
		)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	response, err := newStreamTestClient(server.URL).createChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model:    "primary-model",
		Messages: []goopenai.ChatCompletionMessage{{Role: goopenai.ChatMessageRoleUser, Content: "hello"}},
	})

	require.ErrorIs(t, err, ErrIncompleteChatCompletionStream)
	require.Empty(t, response.Choices)
}

func TestCreateChatCompletionReturnsErrorSentInsideStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, err := io.WriteString(writer, ""+
			`data: {"id":"chatcmpl-stream","model":"resolved-model","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`+"\n\n"+
			`data: {"error":{"message":"upstream overloaded","type":"server_error"}}`+"\n\n",
		)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	response, err := newStreamTestClient(server.URL).createChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model:    "primary-model",
		Messages: []goopenai.ChatCompletionMessage{{Role: goopenai.ChatMessageRoleUser, Content: "hello"}},
	})

	require.ErrorContains(t, err, "upstream overloaded")
	require.Empty(t, response.Choices)
}
