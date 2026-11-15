package chathistories

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/nekomeowww/insights-bot/ent/chathistories"
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai/openaimock"
)

type fakeTelegramFileSource struct {
	baseURL      string
	customEmojis string

	mu       sync.Mutex
	requests []string
}

func (s *fakeTelegramFileSource) GetFileDirectURL(fileID string) (string, error) {
	return s.baseURL + "/file/" + fileID, nil
}

func (s *fakeTelegramFileSource) MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, endpoint+" "+params["custom_emoji_ids"])
	s.mu.Unlock()

	return &tgbotapi.APIResponse{Ok: true, Result: json.RawMessage(s.customEmojis)}, nil
}

// newMediaTestModel serves every file as "fake file bytes" unless fileContents
// has the file id, in which case that content is served.
func newMediaTestModel(t *testing.T, describe func(openai.MediaDescriptionRequest) (string, error), fileContents map[string][]byte) (*Model, *openaimock.MockClient, *fakeTelegramFileSource) {
	t.Helper()

	files := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		content, ok := fileContents[strings.TrimPrefix(request.URL.Path, "/file/")]
		if !ok {
			content = []byte("fake file bytes")
		}

		_, _ = writer.Write(content)
	}))
	t.Cleanup(files.Close)

	mock := &openaimock.MockClient{}
	mock.DescribeMediaCalls(func(_ context.Context, request openai.MediaDescriptionRequest) (string, error) {
		return describe(request)
	})
	// Tests that are not about the review behave as if no check model is set.
	mock.ReviewMediaDescriptionReturns("", openai.ErrMediaReviewUnavailable)

	mediaModel := &Model{
		config: &configs.Config{MediaUnderstanding: configs.SectionMediaUnderstanding{
			Enabled:         true,
			MaxFileBytes:    10 << 20,
			MaxVoiceSeconds: 300,
			MaxVideoSeconds: 180,
		}},
		logger:   model.logger,
		ent:      model.ent,
		openAI:   mock,
		linkprev: model.linkprev,
		redis:    model.redis,
	}
	mediaModel.media = newMediaEnricher(mediaModel)

	source := &fakeTelegramFileSource{baseURL: files.URL, customEmojis: "[]"}
	mediaModel.SetTelegramFileSource(source)

	return mediaModel, mock, source
}

func newMediaTestMessage(chatID int64, messageID int) *tgbotapi.Message {
	return &tgbotapi.Message{
		MessageID: messageID,
		From:      &tgbotapi.User{ID: 42, FirstName: "Tester"},
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: "media test"},
		Date:      int(time.Now().Unix()),
	}
}

func uniqueMediaID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func storedHistoryText(t *testing.T, chatID int64, messageID int) string {
	t.Helper()

	history, err := model.ent.ChatHistories.Query().
		Where(chathistories.ChatID(chatID), chathistories.MessageID(int64(messageID))).
		Only(context.Background())
	require.NoError(t, err)

	return history.Text
}

func cleanupMediaChat(t *testing.T, chatID int64) {
	t.Cleanup(func() {
		_, _ = model.ent.ChatHistories.Delete().Where(chathistories.ChatID(chatID)).Exec(context.Background())
	})
}

type failingTelegramFileSource struct{}

func (failingTelegramFileSource) GetFileDirectURL(string) (string, error) {
	return "", &url.Error{Op: "Post", URL: "https://api.telegram.org/botSECRET-TOKEN/getFile", Err: errors.New("connection reset")}
}

func (failingTelegramFileSource) MakeRequest(string, tgbotapi.Params) (*tgbotapi.APIResponse, error) {
	return nil, &url.Error{Op: "Post", URL: "https://api.telegram.org/botSECRET-TOKEN/getCustomEmojiStickers", Err: errors.New("timeout")}
}

func TestMediaEnricherKeepsBotTokenOutOfErrors(t *testing.T) {
	mediaModel, _, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) { return "", nil }, nil)
	mediaModel.SetTelegramFileSource(failingTelegramFileSource{})

	_, _, err := mediaModel.media.download(context.Background(), "file", 1024)
	require.ErrorContains(t, err, "connection reset")
	require.NotContains(t, err.Error(), "SECRET-TOKEN")

	_, err = mediaModel.media.customEmojiStickers([]string{"1"})
	require.ErrorContains(t, err, "timeout")
	require.NotContains(t, err.Error(), "SECRET-TOKEN")
}

func TestComposeHistoryTextRecordsMediaPlaceholders(t *testing.T) {
	mediaModel, _, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) { return "", nil }, nil)

	photo := newMediaTestMessage(-1, 1)
	photo.Photo = []tgbotapi.PhotoSize{
		{FileID: "small.jpg", FileUniqueID: uniqueMediaID(), Width: 90, Height: 90},
		{FileID: "large.jpg", FileUniqueID: uniqueMediaID(), Width: 1280, Height: 960},
	}
	photo.Caption = "看這個"

	sticker := newMediaTestMessage(-1, 2)
	sticker.Sticker = &tgbotapi.Sticker{FileID: "sticker.webp", FileUniqueID: uniqueMediaID(), Emoji: "😂"}

	voice := newMediaTestMessage(-1, 3)
	voice.Voice = &tgbotapi.Voice{FileID: "voice.oga", FileUniqueID: uniqueMediaID(), Duration: 72}

	longVoice := newMediaTestMessage(-1, 4)
	longVoice.Voice = &tgbotapi.Voice{FileID: "long.oga", FileUniqueID: uniqueMediaID(), Duration: 600}

	gif := newMediaTestMessage(-1, 5)
	gif.Animation = &tgbotapi.Animation{FileID: "gif.mp4", FileUniqueID: uniqueMediaID(), Duration: 3}
	gif.Document = &tgbotapi.Document{FileID: "gif.mp4", FileName: "gif.mp4"}

	document := newMediaTestMessage(-1, 6)
	document.Document = &tgbotapi.Document{FileID: "a.pdf", FileUniqueID: uniqueMediaID(), FileName: "a.pdf", MimeType: "application/pdf", FileSize: 1536}

	archive := newMediaTestMessage(-1, 7)
	archive.Document = &tgbotapi.Document{FileID: "a.zip", FileUniqueID: uniqueMediaID(), FileName: "a.zip", MimeType: "application/zip", FileSize: 2 << 20}

	video := newMediaTestMessage(-1, 8)
	video.Video = &tgbotapi.Video{FileID: "clip.mp4", FileUniqueID: uniqueMediaID(), Duration: 30}

	longVideo := newMediaTestMessage(-1, 9)
	longVideo.Video = &tgbotapi.Video{FileID: "movie.mp4", FileUniqueID: uniqueMediaID(), Duration: 600}

	tables := []struct {
		message    *tgbotapi.Message
		text       string
		needsMedia bool
	}{
		{photo, "[photo] 看這個", true},
		{sticker, "[sticker 😂]", true},
		{voice, "[voice 1:12]", true},
		{longVoice, "[voice 10:00]", false},
		{gif, "[GIF]", true},
		{document, "[file a.pdf 1 KB]", true},
		{archive, "[file a.zip 2.0 MB]", false},
		{video, "[video 0:30]", true},
		{longVideo, "[video 10:00]", false},
	}

	for _, table := range tables {
		text, needsMedia, err := mediaModel.composeHistoryText(table.message, true)
		require.NoError(t, err)
		require.Equal(t, table.text, text)
		require.Equal(t, table.needsMedia, needsMedia, table.text)
	}

	disabledText, _, err := model.composeHistoryText(sticker, true)
	require.NoError(t, err)
	require.Empty(t, disabledText, "media without text stays unrecorded while the feature is off")
}

func TestComposeHistoryTextUsesCachedDescriptions(t *testing.T) {
	mediaModel, _, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) { return "", nil }, nil)

	photoID := uniqueMediaID()
	emojiID := uniqueMediaID()

	mediaModel.mediaCacheSet(mediaCacheKey(-1, "photo", photoID), "一隻橘貓趴在鍵盤上", time.Minute)
	mediaModel.mediaCacheSet(customEmojiCacheKey(-1, emojiID), "流口水的花痴少女", time.Minute)

	message := newMediaTestMessage(-1, 1)
	message.Photo = []tgbotapi.PhotoSize{{FileID: "cat.jpg", FileUniqueID: photoID, Width: 800, Height: 600}}
	message.Caption = "好耶👅"
	message.CaptionEntities = []tgbotapi.MessageEntity{{Type: "custom_emoji", Offset: 2, Length: 2, URL: "tg://emoji?id=" + emojiID}}

	text, needsMedia, err := mediaModel.composeHistoryText(message, true)

	require.NoError(t, err)
	require.Equal(t, "[photo: 一隻橘貓趴在鍵盤上] 好耶👅〔流口水的花痴少女〕", text)
	require.False(t, needsMedia)
}

func TestMediaEnricherDescribesPhotoAndCustomEmoji(t *testing.T) {
	mediaModel, mock, source := newMediaTestModel(t, func(request openai.MediaDescriptionRequest) (string, error) {
		if request.Kind == openai.MediaKindPhoto {
			return "一隻橘貓趴在鍵盤上", nil
		}

		return "流口水的花痴少女", nil
	}, nil)

	emojiID := uniqueMediaID()
	source.customEmojis = fmt.Sprintf(`[{"custom_emoji_id":%q,"file_id":"emoji.tgs","is_animated":true,"thumbnail":{"file_id":"emoji-thumb.webp"}}]`, emojiID)

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Photo = []tgbotapi.PhotoSize{{FileID: "cat.jpg", FileUniqueID: uniqueMediaID(), Width: 800, Height: 600}}
	message.Caption = "好耶👅"
	message.CaptionEntities = []tgbotapi.MessageEntity{{Type: "custom_emoji", Offset: 2, Length: 2, URL: "tg://emoji?id=" + emojiID}}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))
	require.Equal(t, "[photo] 好耶👅", storedHistoryText(t, chatID, 1))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[photo: 一隻橘貓趴在鍵盤上] 好耶👅〔流口水的花痴少女〕"
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 2, mock.DescribeMediaCallCount())
	_, request := mock.DescribeMediaArgsForCall(1)
	require.Equal(t, openai.MediaKindSticker, request.Kind)
	require.Equal(t, "image/webp", request.MimeType, "an animated custom emoji is described from its thumbnail")
	require.Equal(t, []string{"getCustomEmojiStickers [\"" + emojiID + "\"]"}, source.requests)
}

func TestMediaEnricherWritesTheLatestEdit(t *testing.T) {
	mediaModel, _, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) { return "一隻橘貓", nil }, nil)

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Photo = []tgbotapi.PhotoSize{{FileID: "cat.jpg", FileUniqueID: uniqueMediaID(), Width: 800, Height: 600}}
	message.Caption = "第一版"

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	edited := *message
	edited.Caption = "第二版"
	edited.EditDate = message.Date + 10

	require.NoError(t, mediaModel.UpdateOneTelegramChatHistory(&edited))
	require.Equal(t, "[photo] 第一版", storedHistoryText(t, chatID, 1), "the queued job owns the write")

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[photo: 一隻橘貓] 第二版"
	}, 10*time.Second, 50*time.Millisecond)
}

func TestMediaEnricherDescribesASharedStickerOnce(t *testing.T) {
	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) { return "大笑的貓", nil }, nil)

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	stickerID := uniqueMediaID()

	for messageID := 1; messageID <= 3; messageID++ {
		message := newMediaTestMessage(chatID, messageID)
		message.Sticker = &tgbotapi.Sticker{FileID: "cat.webp", FileUniqueID: stickerID, Emoji: "😂"}

		require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))
	}

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		for messageID := 1; messageID <= 3; messageID++ {
			if storedHistoryText(t, chatID, messageID) != "[sticker 😂: 大笑的貓]" {
				return false
			}
		}

		return true
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 1, mock.DescribeMediaCallCount())
}

func TestMediaEnricherRetriesUnreadableStickerWithAnotherRendering(t *testing.T) {
	glyph, err := base64.StdEncoding.DecodeString(transparentSquareWebP)
	require.NoError(t, err)

	attempts := 0
	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		attempts++
		if attempts == 1 {
			return "", openai.ErrMediaUnreadable
		}

		return "天", nil
	}, map[string][]byte{"glyph.webp": glyph})

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Sticker = &tgbotapi.Sticker{FileID: "glyph.webp", FileUniqueID: uniqueMediaID(), Emoji: "😳"}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[sticker 😳: 天]"
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 2, mock.DescribeMediaCallCount())

	_, first := mock.DescribeMediaArgsForCall(0)
	_, second := mock.DescribeMediaArgsForCall(1)

	require.Equal(t, "image/png", first.MimeType, "the transparent sticker is flattened before the first attempt")
	require.Contains(t, first.Hint, "😳")
	require.NotEqual(t, first.Data, second.Data, "the retry uses another rendering")
}

func TestMediaEnricherLetsCheckModelCorrectSticker(t *testing.T) {
	glyph, err := base64.StdEncoding.DecodeString(transparentSquareWebP)
	require.NoError(t, err)

	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		return "神秘黑色像素剪影", nil
	}, map[string][]byte{"glyph.webp": glyph})

	var rendered []byte

	mock.ReviewMediaDescriptionCalls(func(_ context.Context, request openai.MediaReviewRequest) (string, error) {
		data, mimeType, err := request.Render(openai.MediaRenderingLightBackground)
		if err != nil || mimeType != "image/png" {
			return "", fmt.Errorf("render failed: %w", err)
		}

		rendered = data

		return "漢字「天」", nil
	})

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Sticker = &tgbotapi.Sticker{FileID: "glyph.webp", FileUniqueID: uniqueMediaID(), Emoji: "😳"}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[sticker 😳: 漢字「天」]"
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 1, mock.ReviewMediaDescriptionCallCount())

	_, review := mock.ReviewMediaDescriptionArgsForCall(0)
	require.Equal(t, "神秘黑色像素剪影", review.Draft)
	require.Equal(t, "image/png", review.MimeType, "the check model sees the rendering the media model saw")
	require.NotEmpty(t, rendered, "the check model ran the image code")
}

func TestMediaEnricherReviewsVideoStickerThroughThumbnail(t *testing.T) {
	glyph, err := base64.StdEncoding.DecodeString(transparentSquareWebP)
	require.NoError(t, err)

	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		return "跳舞的貓", nil
	}, map[string][]byte{"dance-thumb.webp": glyph})

	mock.ReviewMediaDescriptionReturns("跳舞的橘貓", nil)

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Sticker = &tgbotapi.Sticker{
		FileID:       "dance.webm",
		FileUniqueID: uniqueMediaID(),
		Emoji:        "💃",
		Thumbnail:    &tgbotapi.PhotoSize{FileID: "dance-thumb.webp"},
	}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[sticker 💃: 跳舞的橘貓]"
	}, 10*time.Second, 50*time.Millisecond)

	_, described := mock.DescribeMediaArgsForCall(0)
	require.Equal(t, "video/webm", described.MimeType, "the media model sees the animation itself")

	_, review := mock.ReviewMediaDescriptionArgsForCall(0)
	require.Equal(t, "跳舞的貓", review.Draft)
	require.Equal(t, "image/png", review.MimeType, "the check model sees the flattened first frame")
}

func TestMediaEnricherSkipsReviewForOpaquePhoto(t *testing.T) {
	photo := solidPNG(t, 320, 240, func(x, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xff} //nolint:gosec // reduced modulo 256 first
	})

	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		return "漸層色塊", nil
	}, map[string][]byte{"gradient.png": photo})

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Photo = []tgbotapi.PhotoSize{{FileID: "gradient.png", FileUniqueID: uniqueMediaID(), Width: 320, Height: 240}}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[photo: 漸層色塊]"
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 0, mock.ReviewMediaDescriptionCallCount())
}

func TestMediaDescriptionsAreIsolatedPerChat(t *testing.T) {
	var mu sync.Mutex

	calls := 0
	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		calls++

		return fmt.Sprintf("描述%d", calls), nil
	}, nil)

	chatA := -time.Now().UnixNano()
	chatB := chatA - 1

	cleanupMediaChat(t, chatA)
	cleanupMediaChat(t, chatB)

	stickerID := uniqueMediaID()

	for _, chatID := range []int64{chatA, chatB} {
		message := newMediaTestMessage(chatID, 1)
		message.Sticker = &tgbotapi.Sticker{FileID: "cat.webp", FileUniqueID: stickerID, Emoji: "😂"}

		require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))
	}

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return strings.HasPrefix(storedHistoryText(t, chatA, 1), "[sticker 😂: 描述") &&
			strings.HasPrefix(storedHistoryText(t, chatB, 1), "[sticker 😂: 描述")
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 2, mock.DescribeMediaCallCount(), "each chat describes the sticker on its own")
	require.NotEqual(t, storedHistoryText(t, chatA, 1), storedHistoryText(t, chatB, 1))
}

func TestMediaEnricherSummarizesSpreadsheet(t *testing.T) {
	mediaModel, mock, _ := newMediaTestModel(t, func(openai.MediaDescriptionRequest) (string, error) {
		return "店面營收報表", nil
	}, map[string][]byte{"report.xlsx": testSpreadsheet(t)})

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Document = &tgbotapi.Document{
		FileID:       "report.xlsx",
		FileUniqueID: uniqueMediaID(),
		FileName:     "report.xlsx",
		MimeType:     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		FileSize:     1500,
	}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[file report.xlsx 1 KB: 店面營收報表]"
	}, 10*time.Second, 50*time.Millisecond)

	_, request := mock.DescribeMediaArgsForCall(0)
	require.Equal(t, openai.MediaKindDocument, request.Kind)
	require.Equal(t, "text/plain", request.MimeType)
	require.Contains(t, string(request.Data), "店名\t營收")
	require.Equal(t, "File name: report.xlsx", request.Hint)
}

func TestMediaEnricherFallsBackOnGIFThumbnail(t *testing.T) {
	mediaModel, mock, _ := newMediaTestModel(t, func(request openai.MediaDescriptionRequest) (string, error) {
		if request.MimeType == "video/mp4" {
			return "", openai.ErrMediaUnreadable
		}

		return "跳舞的貓", nil
	}, nil)

	chatID := -time.Now().UnixNano()
	cleanupMediaChat(t, chatID)

	message := newMediaTestMessage(chatID, 1)
	message.Animation = &tgbotapi.Animation{
		FileID:       "cat.mp4",
		FileUniqueID: uniqueMediaID(),
		Duration:     3,
		Thumbnail:    &tgbotapi.PhotoSize{FileID: "cat-thumb.jpg"},
	}

	require.NoError(t, mediaModel.SaveOneTelegramChatHistory(message))

	mediaModel.media.start()
	t.Cleanup(func() { _ = mediaModel.media.stop(context.Background()) })

	require.Eventually(t, func() bool {
		return storedHistoryText(t, chatID, 1) == "[GIF: 跳舞的貓]"
	}, 10*time.Second, 50*time.Millisecond)

	require.Equal(t, 2, mock.DescribeMediaCallCount())

	_, fallback := mock.DescribeMediaArgsForCall(1)
	require.Equal(t, openai.MediaKindPhoto, fallback.Kind, "a single frame is described as a photo")
	require.Equal(t, "image/jpeg", fallback.MimeType)
}
