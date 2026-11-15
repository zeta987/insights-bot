package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// MediaRendering names image code the check model can run on a file it
// reviews.
type MediaRendering string

const (
	// MediaRenderingLightBackground flattens transparency onto white.
	MediaRenderingLightBackground MediaRendering = "light_background"
	// MediaRenderingDarkBackground flattens transparency onto dark gray.
	MediaRenderingDarkBackground MediaRendering = "dark_background"
	// MediaRenderingSilhouette draws every visible pixel black on white.
	MediaRenderingSilhouette MediaRendering = "silhouette"
	// MediaRenderingEnhanceContrast stretches the contrast of the image.
	MediaRenderingEnhanceContrast MediaRendering = "enhance_contrast"
)

// MediaRenderings lists every rendering the check model may ask for.
var MediaRenderings = []MediaRendering{
	MediaRenderingLightBackground,
	MediaRenderingDarkBackground,
	MediaRenderingSilhouette,
	MediaRenderingEnhanceContrast,
}

// ErrMediaReviewUnavailable means no check model is configured.
var ErrMediaReviewUnavailable = errors.New("no check model is configured to review media")

// MediaReviewRequest asks the check model to confirm or correct what the media
// model said about an image.
type MediaReviewRequest struct {
	// MediaDescriptionRequest holds the image exactly as the media model saw it.
	MediaDescriptionRequest
	// Draft is the media model's description, or empty when it could not read
	// the image.
	Draft string
	// Render runs image code on the original file. The check model calls it
	// through the rerender_image tool.
	Render func(MediaRendering) ([]byte, string, error)
}

const (
	mediaReviewMaxTurns   = 4
	mediaReviewToolRender = "rerender_image"
	mediaReviewToolAccept = "accept_description"
)

const mediaReviewSystemPrompt = "You check descriptions of media posted in a Telegram group chat before they go into a chat recap. " +
	"You see the image exactly as the describer saw it, together with the describer's description. " +
	"If the description fits what the image shows, call " + mediaReviewToolAccept + " with it unchanged. " +
	"If it is wrong, vague, or only mentions shapes, blackness or emptiness, the content may be hidden, " +
	"for example dark glyphs on a transparent background that renders as black. Then call " + mediaReviewToolRender +
	" to run image code on the original file; you will see the new rendering. " +
	"When you can see the content, call " + mediaReviewToolAccept + " with a corrected description. " +
	"If no rendering shows anything, call " + mediaReviewToolAccept + " with an empty description. " +
	"The describer followed these instructions, and your description must follow them too:\n\n"

func mediaReviewTools(canRender bool) []openai.Tool {
	renderings := make([]string, 0, len(MediaRenderings))
	for _, rendering := range MediaRenderings {
		renderings = append(renderings, string(rendering))
	}

	enum, _ := json.Marshal(renderings)

	tools := []openai.Tool{{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        mediaReviewToolAccept,
			Description: "Finish the review with a description that matches the image, or an empty one if nothing can be made out.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`),
		},
	}}

	if canRender {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name: mediaReviewToolRender,
				Description: "Re-render the original image with code and look at it again: light_background or dark_background flatten transparency, " +
					"silhouette draws every visible pixel black on white, enhance_contrast stretches the contrast.",
				Parameters: json.RawMessage(`{"type":"object","properties":{"rendering":{"type":"string","enum":` + string(enum) + `}},"required":["rendering"]}`),
			},
		})
	}

	return tools
}

func mediaImagePart(mimeType string, data []byte) openai.ChatMessagePart {
	return openai.ChatMessagePart{
		Type:     openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)},
	}
}

func isKnownMediaRendering(rendering MediaRendering) bool {
	for _, known := range MediaRenderings {
		if rendering == known {
			return true
		}
	}

	return false
}

// runMediaReviewTool executes one tool call. It returns the tool result text,
// a new rendering to show when one was made, and the final description when
// the review is done.
func runMediaReviewTool(request MediaReviewRequest, call openai.ToolCall) (string, *openai.ChatMessagePart, *string) {
	switch call.Function.Name {
	case mediaReviewToolAccept:
		var args struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return "The arguments were not valid JSON: " + err.Error(), nil, nil
		}

		return "Done.", nil, &args.Description
	case mediaReviewToolRender:
		var args struct {
			Rendering MediaRendering `json:"rendering"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || !isKnownMediaRendering(args.Rendering) {
			return "Unknown rendering. Use one of the listed values.", nil, nil
		}
		if request.Render == nil {
			return "This media cannot be re-rendered.", nil, nil
		}

		data, mimeType, err := request.Render(args.Rendering)
		if err != nil {
			return fmt.Sprintf("The %s rendering is not available: %v", args.Rendering, err), nil, nil
		}

		part := mediaImagePart(mimeType, data)

		return fmt.Sprintf("Rendered with %s; the new rendering follows.", args.Rendering), &part, nil
	default:
		return "Unknown tool.", nil, nil
	}
}

// ReviewMediaDescription lets the check model confirm the media model's
// description of an image or correct it, running image code through a tool
// when the content is hidden. It returns ErrMediaUnreadable when neither model
// can make the image out, and ErrMediaReviewUnavailable without a check model.
func (c *OpenAIClient) ReviewMediaDescription(ctx context.Context, request MediaReviewRequest) (string, error) {
	if c.checkModelName == "" {
		return "", ErrMediaReviewUnavailable
	}

	describerPrompt, ok := mediaDescriptionSystemPrompts[request.Kind]
	if !ok {
		return "", fmt.Errorf("unsupported media kind %q", request.Kind)
	}

	language := c.defaultSummarizationLanguage
	if language == "" {
		language = "Simplified Chinese"
	}

	draft := request.Draft
	if draft == "" {
		draft = "(the describer could not make it out)"
	}

	intro := "Describer's description: " + draft
	if hint := strings.TrimSpace(request.Hint); hint != "" {
		intro += "\nContext: " + hint
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: mediaReviewSystemPrompt + strings.ReplaceAll(describerPrompt, "{language}", language)},
		{Role: openai.ChatMessageRoleUser, MultiContent: []openai.ChatMessagePart{
			{Type: openai.ChatMessagePartTypeText, Text: intro},
			mediaImagePart(request.MimeType, request.Data),
		}},
	}
	tools := mediaReviewTools(request.Render != nil)
	renderings := 0

	for range mediaReviewMaxTurns {
		if c.mediaLimiter != nil {
			c.mediaLimiter.Take()
		}

		resp, err := c.createChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:      c.checkModelName,
			Messages:   messages,
			Tools:      tools,
			ToolChoice: "required",
		})
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("check model returned no choices")
		}

		message := resp.Choices[0].Message
		if len(message.ToolCalls) == 0 {
			// A plain answer instead of a tool call is taken as the description.
			return c.finishMediaReview(request, message.Content, renderings)
		}

		messages = append(messages, openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   message.Content,
			ToolCalls: message.ToolCalls,
		})

		newImages := make([]openai.ChatMessagePart, 0)

		for _, call := range message.ToolCalls {
			result, image, final := runMediaReviewTool(request, call)
			if final != nil {
				return c.finishMediaReview(request, *final, renderings)
			}
			if image != nil {
				newImages = append(newImages, *image)
				renderings++
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Content:    result,
			})
		}

		if len(newImages) > 0 {
			parts := append([]openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "New rendering:"}}, newImages...)
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})
		}
	}

	return c.finishMediaReview(request, request.Draft, renderings)
}

func (c *OpenAIClient) finishMediaReview(request MediaReviewRequest, description string, renderings int) (string, error) {
	normalized := normalizeMediaDescription(request.Kind, description)
	if isMediaUnreadableAnswer(normalized) {
		normalized = ""
	}

	fields := []zap.Field{
		zap.String("kind", string(request.Kind)),
		zap.String("check_model", c.checkModelName),
		zap.Bool("changed", normalized != request.Draft),
		zap.Int("draft_runes", utf8.RuneCountInString(request.Draft)),
		zap.Int("reviewed_runes", utf8.RuneCountInString(normalized)),
		zap.Int("renderings", renderings),
	}
	// Descriptions retell what members posted, so they only reach the logs
	// when verbose payload logging is switched on.
	if c.enableVerbosePayloadLogs {
		fields = append(fields, zap.String("draft", request.Draft), zap.String("reviewed", normalized))
	}

	c.logInfo("check model reviewed media description", fields...)

	if normalized == "" {
		return "", ErrMediaUnreadable
	}

	return normalized, nil
}
