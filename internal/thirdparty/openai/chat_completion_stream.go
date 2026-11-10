package openai

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/sashabaranov/go-openai"
)

// ErrIncompleteChatCompletionStream is returned when a stream ends before every
// choice reported a finish reason, which means the content is truncated.
var ErrIncompleteChatCompletionStream = errors.New("chat completion stream ended before a finish reason was received")

// streamEmptyMessagesLimit replaces the library default of 300 non-data lines
// allowed between two chunks.
const streamEmptyMessagesLimit = 10000

type streamedChoice struct {
	role         string
	content      strings.Builder
	finishReason openai.FinishReason
}

// createChatCompletion sends the request as a stream and assembles the chunks
// back into a regular response, so callers keep working with a complete
// message. Receiving bytes while the model generates keeps reverse proxies
// with a read timeout (Cloudflare, for example) from cutting long requests.
func (c *OpenAIClient) createChatCompletion(
	ctx context.Context,
	request openai.ChatCompletionRequest,
) (openai.ChatCompletionResponse, error) {
	request.Stream = true
	// Without this option the stream carries no token usage at all.
	request.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

	stream, err := c.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	defer func() {
		_ = stream.Close()
	}()

	resp := openai.ChatCompletionResponse{Object: "chat.completion"}
	choices := make([]*streamedChoice, 0, 1)

	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return openai.ChatCompletionResponse{}, recvErr
		}

		if chunk.ID != "" {
			resp.ID = chunk.ID
		}
		if chunk.Model != "" {
			resp.Model = chunk.Model
		}
		if chunk.Created != 0 {
			resp.Created = chunk.Created
		}
		if chunk.SystemFingerprint != "" {
			resp.SystemFingerprint = chunk.SystemFingerprint
		}
		if chunk.Usage != nil {
			resp.Usage = *chunk.Usage
		}

		for _, delta := range chunk.Choices {
			if delta.Index < 0 {
				continue
			}
			for len(choices) <= delta.Index {
				choices = append(choices, &streamedChoice{})
			}

			choice := choices[delta.Index]
			if delta.Delta.Role != "" {
				choice.role = delta.Delta.Role
			}
			choice.content.WriteString(delta.Delta.Content)
			if delta.FinishReason != "" {
				choice.finishReason = delta.FinishReason
			}
		}
	}

	// The reader reports io.EOF both for the [DONE] marker and for a connection
	// that closed early, so a finish reason is the only proof of a full message.
	if len(choices) == 0 {
		return openai.ChatCompletionResponse{}, ErrIncompleteChatCompletionStream
	}

	resp.Choices = make([]openai.ChatCompletionChoice, 0, len(choices))
	for index, choice := range choices {
		if choice.finishReason == "" {
			return openai.ChatCompletionResponse{}, ErrIncompleteChatCompletionStream
		}

		role := choice.role
		if role == "" {
			role = openai.ChatMessageRoleAssistant
		}

		resp.Choices = append(resp.Choices, openai.ChatCompletionChoice{
			Index: index,
			Message: openai.ChatCompletionMessage{
				Role:    role,
				Content: choice.content.String(),
			},
			FinishReason: choice.finishReason,
		})
	}

	return resp, nil
}
