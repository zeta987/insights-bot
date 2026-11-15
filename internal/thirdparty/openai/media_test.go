package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

type mediaTestPayload struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

type mediaTestPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// newMediaTestClient answers every request with answer and records the
// payload it received.
func newMediaTestClient(t *testing.T, answer string) (*OpenAIClient, *mediaTestPayload) {
	t.Helper()

	payload := &mediaTestPayload{}

	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		require.NoError(t, json.NewDecoder(request.Body).Decode(payload))

		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write(condensedCompletionBody("resolved-media-model", answer))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"

	return &OpenAIClient{
		modelName:                    "primary-model",
		mediaModelName:               "media-model",
		defaultSummarizationLanguage: "Traditional Chinese",
		client:                       goopenai.NewClientWithConfig(config),
		logger:                       client.logger,
		limiter:                      ratelimit.New(1000),
		mediaLimiter:                 ratelimit.New(1000),
	}, payload
}

func mediaTestParts(t *testing.T, payload *mediaTestPayload) (string, []mediaTestPart) {
	t.Helper()
	require.Len(t, payload.Messages, 2)

	var system string

	require.NoError(t, json.Unmarshal(payload.Messages[0].Content, &system))

	var parts []mediaTestPart

	require.NoError(t, json.Unmarshal(payload.Messages[1].Content, &parts))

	return system, parts
}

func TestDescribeMediaSendsFileAsDataURI(t *testing.T) {
	voice := []byte("OggS fake voice bytes")
	mediaClient, payload := newMediaTestClient(t, "  今晚十點發布\n v1.4.3 [完] ")

	description, err := mediaClient.DescribeMedia(context.Background(), MediaDescriptionRequest{
		Kind:     MediaKindVoice,
		MimeType: "audio/ogg",
		Data:     voice,
	})

	require.NoError(t, err)
	require.Equal(t, "今晚十點發布 v1.4.3 完", description)
	require.Equal(t, "media-model", payload.Model)

	system, parts := mediaTestParts(t, payload)
	require.Contains(t, system, "Traditional Chinese")
	require.Contains(t, system, "Transcribe the speech")
	require.NotContains(t, system, "{language}")
	require.Len(t, parts, 2)
	require.Equal(t, "image_url", parts[1].Type)
	require.Equal(t, "data:audio/ogg;base64,"+base64.StdEncoding.EncodeToString(voice), parts[1].ImageURL.URL)
}

func TestDescribeMediaSendsExtractedTextAndHint(t *testing.T) {
	mediaClient, payload := newMediaTestClient(t, "店面營收報表")

	description, err := mediaClient.DescribeMedia(context.Background(), MediaDescriptionRequest{
		Kind:     MediaKindDocument,
		MimeType: "text/plain",
		Data:     []byte("店名\t營收\n本店\t120"),
		Hint:     "File name: report.xlsx",
	})

	require.NoError(t, err)
	require.Equal(t, "店面營收報表", description)

	_, parts := mediaTestParts(t, payload)
	require.Len(t, parts, 2)
	require.Contains(t, parts[0].Text, "File name: report.xlsx")
	require.Equal(t, "text", parts[1].Type)
	require.Contains(t, parts[1].Text, "店名\t營收")
}

func TestDescribeMediaReportsUnreadableMedia(t *testing.T) {
	mediaClient, _ := newMediaTestClient(t, "UNREADABLE.")

	_, err := mediaClient.DescribeMedia(context.Background(), MediaDescriptionRequest{
		Kind:     MediaKindSticker,
		MimeType: "image/png",
		Data:     []byte("png"),
		Hint:     "It stands for the emoji 😳.",
	})

	require.ErrorIs(t, err, ErrMediaUnreadable)
}

func TestDescribeMediaRejectsUnknownKindAndEmptyData(t *testing.T) {
	_, err := client.DescribeMedia(context.Background(), MediaDescriptionRequest{Kind: MediaKind("hologram"), MimeType: "video/mp4", Data: []byte("x")})
	require.ErrorContains(t, err, "unsupported media kind")

	_, err = client.DescribeMedia(context.Background(), MediaDescriptionRequest{Kind: MediaKindPhoto, MimeType: "image/png"})
	require.ErrorContains(t, err, "empty")
}

func TestNormalizeMediaDescriptionCapsLength(t *testing.T) {
	require.Equal(t, "流口水的花痴少女", normalizeMediaDescription(MediaKindSticker, "「流口水的花痴少女」\n"))
	require.Equal(t, "貓咪 42 號", normalizeMediaDescription(MediaKindPhoto, "```\n[貓咪 42 號]\n```"))

	long := normalizeMediaDescription(MediaKindSticker, strings.Repeat("字", 40))
	require.Equal(t, strings.Repeat("字", 24)+"…", long)
}
