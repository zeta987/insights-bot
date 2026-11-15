package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// MediaKind selects how a media message is described for chat histories.
type MediaKind string

const (
	// MediaKindPhoto describes a photo and any text that matters in it.
	MediaKindPhoto MediaKind = "photo"
	// MediaKindSticker labels a sticker or custom emoji in a few words.
	MediaKindSticker MediaKind = "sticker"
	// MediaKindVoice transcribes a voice message.
	MediaKindVoice MediaKind = "voice"
	// MediaKindAnimation describes a GIF.
	MediaKindAnimation MediaKind = "animation"
	// MediaKindVideo describes a video or round video message.
	MediaKindVideo MediaKind = "video"
	// MediaKindAudio describes an audio file such as a song or a recording.
	MediaKindAudio MediaKind = "audio"
	// MediaKindDocument summarizes a shared file.
	MediaKindDocument MediaKind = "document"
)

// ErrMediaUnreadable means the model could not make out the media, so another
// rendering of it may still work.
var ErrMediaUnreadable = errors.New("media model could not read the media")

// mediaUnreadableAnswer is what visual prompts ask the model to answer when it
// cannot make out the media.
const mediaUnreadableAnswer = "UNREADABLE"

const mediaUnreadableRule = " If you cannot make out what it shows, answer exactly " + mediaUnreadableAnswer + "."

const mediaPlainOutputRule = " Output plain text on one line: no Markdown, no brackets, no quotes, no prefix."

var mediaDescriptionSystemPrompts = map[MediaKind]string{
	MediaKindPhoto: "You describe an image that a member posted in a Telegram group chat, for a chat recap. Write in {language}. " +
		"Give one sentence of at most 60 characters about what the image shows. If it contains readable text that matters, " +
		"such as a screenshot, a sign or a meme caption, add that text after a colon, at most 120 characters." +
		mediaUnreadableRule + mediaPlainOutputRule,
	MediaKindSticker: "You label a Telegram sticker or custom emoji for a chat recap. Write in {language}. " +
		"Output a label of at most 12 characters that names what it shows and the emotion it conveys, " +
		"for example \"drooling love-struck anime girl\" written in that language. " +
		"If it is mainly text, such as a word or a character, give that text and what it means." +
		mediaUnreadableRule + " Output the label only, on one line: no brackets, no quotes, no ending punctuation.",
	MediaKindVoice: "You transcribe a voice message from a Telegram group chat for a chat recap. " +
		"Transcribe the speech word for word in the language it is spoken in. " +
		"If the transcript would be longer than 300 characters, write a faithful summary of at most 300 characters in {language} instead. " +
		"If there is no speech, describe the sound in at most 30 characters in {language}." + mediaPlainOutputRule,
	MediaKindAnimation: "You describe a GIF that a member posted in a Telegram group chat, for a chat recap. Write in {language}. " +
		"Give one sentence of at most 50 characters about what happens and the mood it conveys; " +
		"add any readable caption text after a colon." + mediaUnreadableRule + mediaPlainOutputRule,
	MediaKindVideo: "You describe a video that a member posted in a Telegram group chat, for a chat recap. Write in {language}. " +
		"Say in at most 80 characters what the video shows, then summarize any speech or important on-screen text in at most 150 characters." +
		mediaUnreadableRule + mediaPlainOutputRule,
	MediaKindAudio: "You describe an audio file that a member shared in a Telegram group chat, for a chat recap. Write in {language}. " +
		"If it is mostly speech, summarize what is said in at most 150 characters. " +
		"If it is music, name the genre and mood, and the song if you recognize it, in at most 60 characters." + mediaPlainOutputRule,
	MediaKindDocument: "You summarize a file that a member shared in a Telegram group chat, for a chat recap. Write in {language}. " +
		"Say what kind of document it is and give its key content, figures or conclusions in at most 150 characters." +
		mediaPlainOutputRule,
}

// mediaDescriptionRuneLimits caps what a description may add to one chat
// history line, whatever the model returns.
var mediaDescriptionRuneLimits = map[MediaKind]int{
	MediaKindPhoto:     200,
	MediaKindSticker:   24,
	MediaKindVoice:     400,
	MediaKindAnimation: 120,
	MediaKindVideo:     300,
	MediaKindAudio:     200,
	MediaKindDocument:  220,
}

// mediaDocumentTextLimit bounds the extracted text sent for one document.
const mediaDocumentTextLimit = 12000

// MediaDescriptionRequest is one file, or text extracted from one, to describe.
type MediaDescriptionRequest struct {
	Kind MediaKind
	// MimeType of Data. Text types are sent as text instead of as a file.
	MimeType string
	Data     []byte
	// Hint is optional context such as the emoji a sticker stands for or the
	// name of a shared file.
	Hint string
}

var mediaDescriptionStripper = strings.NewReplacer(
	"```", "",
	"[", "",
	"]", "",
	"〔", "",
	"〕", "",
)

// normalizeMediaDescription flattens a model answer into one line that can sit
// inside a bracketed chat history marker.
func normalizeMediaDescription(kind MediaKind, content string) string {
	content = mediaDescriptionStripper.Replace(content)
	content = strings.Join(strings.Fields(content), " ")
	content = strings.Trim(content, " \"'“”「」『』")

	limit := mediaDescriptionRuneLimits[kind]
	if limit > 0 && utf8.RuneCountInString(content) > limit {
		content = string([]rune(content)[:limit]) + "…"
	}

	return content
}

func isMediaUnreadableAnswer(content string) bool {
	return strings.EqualFold(strings.Trim(strings.TrimSpace(content), ".。!！"), mediaUnreadableAnswer)
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}

	return string([]rune(text)[:limit]) + "\n…(truncated)"
}

// mediaUserContent builds the user message: an instruction, the optional hint
// and either the extracted text or the file as a data URI. The gateway passes
// data URIs to Gemini for images, audio, video and PDF alike.
func mediaUserContent(request MediaDescriptionRequest) []openai.ChatMessagePart {
	instruction := "Describe this " + string(request.Kind) + "."
	if hint := strings.TrimSpace(request.Hint); hint != "" {
		instruction += " Context: " + hint
	}

	parts := []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: instruction}}

	if strings.HasPrefix(request.MimeType, "text/") {
		return append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: "File content:\n" + truncateRunes(string(request.Data), mediaDocumentTextLimit),
		})
	}

	return append(parts, openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL: "data:" + request.MimeType + ";base64," + base64.StdEncoding.EncodeToString(request.Data),
		},
	})
}

// DescribeMedia asks the media model to describe one piece of media. It
// returns ErrMediaUnreadable when the model says it cannot make it out.
func (c *OpenAIClient) DescribeMedia(ctx context.Context, request MediaDescriptionRequest) (string, error) {
	promptTemplate, ok := mediaDescriptionSystemPrompts[request.Kind]
	if !ok {
		return "", fmt.Errorf("unsupported media kind %q", request.Kind)
	}
	if len(request.Data) == 0 || request.MimeType == "" {
		return "", errors.New("media data or mime type is empty")
	}

	language := c.defaultSummarizationLanguage
	if language == "" {
		language = "Simplified Chinese"
	}

	systemPrompt := strings.ReplaceAll(promptTemplate, "{language}", language)

	model := c.mediaModelName
	if model == "" {
		model = c.modelName
	}

	if c.mediaLimiter != nil {
		c.mediaLimiter.Take()
	}

	resp, err := c.createChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:       model,
		Temperature: 0.2,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, MultiContent: mediaUserContent(request)},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("media model returned no choices")
	}

	content := resp.Choices[0].Message.Content
	c.logInfo("described media for chat histories",
		zap.String("kind", string(request.Kind)),
		zap.String("mime_type", request.MimeType),
		zap.Int("bytes", len(request.Data)),
		zap.String("model", model),
		zap.String("used_model", resp.Model),
		zap.Int("prompt_tokens", resp.Usage.PromptTokens),
		zap.Bool("unreadable", isMediaUnreadableAnswer(content)),
	)

	if isMediaUnreadableAnswer(content) {
		return "", ErrMediaUnreadable
	}

	description := normalizeMediaDescription(request.Kind, content)
	if description == "" {
		return "", errors.New("media model returned an empty description")
	}

	return description, nil
}
