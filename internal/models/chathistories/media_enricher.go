package chathistories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/samber/lo"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

const (
	mediaQueueSize    = 100
	mediaWorkers      = 2
	mediaJobTimeout   = 3 * time.Minute
	mediaFetchTimeout = time.Minute
)

// mediaMimeTypes maps Telegram file extensions to the types the media model
// accepts. TGS stickers are missing on purpose: they are Lottie JSON.
var mediaMimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
	".oga":  "audio/ogg",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".wav":  "audio/wav",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".pdf":  "application/pdf",
}

// TelegramFileSource is the part of the Bot API client that media enrichment
// needs. *tgbotapi.BotAPI satisfies it.
type TelegramFileSource interface {
	GetFileDirectURL(fileID string) (string, error)
	MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error)
}

// mediaEnricher describes media and custom emoji in the background, then
// rewrites the stored chat history text. Updates are handled one at a time,
// so model calls must not happen while a message is being recorded.
type mediaEnricher struct {
	model *Model
	queue chan string

	mu       sync.Mutex
	source   TelegramFileSource
	latest   map[string]*tgbotapi.Message
	inflight map[string]chan struct{}
	// writeMu orders the rewrite of a queued message against edits of it.
	writeMu sync.Mutex

	httpClient *http.Client
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func newMediaEnricher(model *Model) *mediaEnricher {
	return &mediaEnricher{
		model:      model,
		queue:      make(chan string, mediaQueueSize),
		latest:     make(map[string]*tgbotapi.Message),
		inflight:   make(map[string]chan struct{}),
		httpClient: &http.Client{Timeout: mediaFetchTimeout},
	}
}

func messageQueueKey(message *tgbotapi.Message) string {
	return fmt.Sprintf("%d/%d", message.Chat.ID, message.MessageID)
}

func (e *mediaEnricher) setSource(source TelegramFileSource) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.source = source
}

func (e *mediaEnricher) fileSource() TelegramFileSource {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.source
}

func (e *mediaEnricher) start() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel

	for range mediaWorkers {
		e.wg.Add(1)

		go func() {
			defer e.wg.Done()

			e.work(ctx)
		}()
	}
}

func (e *mediaEnricher) stop(ctx context.Context) error {
	if e.cancel == nil {
		return nil
	}

	e.cancel()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enqueue schedules a message for enrichment. A message that is already
// queued only has its latest version replaced.
func (e *mediaEnricher) enqueue(message *tgbotapi.Message) {
	key := messageQueueKey(message)

	e.mu.Lock()
	if _, queued := e.latest[key]; queued {
		e.latest[key] = message
		e.mu.Unlock()

		return
	}

	e.latest[key] = message
	e.mu.Unlock()

	select {
	case e.queue <- key:
	default:
		e.mu.Lock()
		delete(e.latest, key)
		e.mu.Unlock()

		e.model.logger.Warn("media enrichment queue is full, keeping the placeholder", zap.String("message", key))
	}
}

// replaceIfQueued hands an edited message to the queued job instead of
// writing it, so the job never overwrites a newer edit with an older text.
func (e *mediaEnricher) replaceIfQueued(message *tgbotapi.Message) bool {
	key := messageQueueKey(message)

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, queued := e.latest[key]; !queued {
		return false
	}

	e.latest[key] = message

	return true
}

func (e *mediaEnricher) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-e.queue:
			e.process(ctx, key)
		}
	}
}

func (e *mediaEnricher) process(ctx context.Context, key string) {
	jobCtx, cancel := context.WithTimeout(ctx, mediaJobTimeout)
	defer cancel()

	for {
		e.mu.Lock()
		message := e.latest[key]
		e.mu.Unlock()

		if message == nil {
			return
		}

		e.resolve(jobCtx, message)

		text, _, err := e.model.composeHistoryText(message, true)
		if err != nil {
			e.model.logger.Warn("failed to compose enriched chat history", zap.String("message", key), zap.Error(err))
		}

		e.writeMu.Lock()
		e.mu.Lock()

		current := e.latest[key]
		if current == message {
			delete(e.latest, key)
		}
		e.mu.Unlock()

		if current != message {
			// An edit arrived while describing; describe and write that one.
			e.writeMu.Unlock()
			continue
		}

		if err == nil && text != "" {
			if writeErr := e.model.writeHistoryText(message, text); writeErr != nil {
				e.model.logger.Warn("failed to store enriched chat history", zap.String("message", key), zap.Error(writeErr))
			}
		}

		e.writeMu.Unlock()

		return
	}
}

// resolve fills the description cache for the media and custom emoji of one
// message.
func (e *mediaEnricher) resolve(ctx context.Context, message *tgbotapi.Message) {
	if part := e.model.messageMediaPart(message); part != nil && part.describe != "" {
		e.once(ctx, part.cacheKey, part.cacheTTL, func() (string, error) {
			return e.describePart(ctx, part)
		})
	}

	chatID := messageChatID(message)
	missing := make([]string, 0)

	for _, id := range customEmojiIDs(messageTextEntities(message)) {
		if _, state := e.model.mediaCacheGet(customEmojiCacheKey(chatID, id)); state == mediaCacheMiss {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return
	}

	stickers, err := e.customEmojiStickers(missing)
	if err != nil {
		e.model.logger.Warn("failed to look up custom emoji", zap.Strings("custom_emoji_ids", missing), zap.Error(err))

		for _, id := range missing {
			e.model.mediaCacheSet(customEmojiCacheKey(chatID, id), mediaCacheFailedMark, mediaFailureTTL)
		}

		return
	}

	for _, sticker := range stickers {
		part := sticker.mediaPart(chatID)
		if part == nil {
			e.model.mediaCacheSet(customEmojiCacheKey(chatID, sticker.CustomEmojiID), mediaCacheFailedMark, mediaLongLivedTTL)
			continue
		}

		e.once(ctx, part.cacheKey, part.cacheTTL, func() (string, error) {
			return e.describePart(ctx, part)
		})
	}
}

// once runs produce for a cache key unless another worker is already doing so
// or the cache already has an answer, then stores the result. Failures are
// remembered briefly so a burst of the same sticker costs one attempt.
func (e *mediaEnricher) once(ctx context.Context, cacheKey string, ttl time.Duration, produce func() (string, error)) {
	e.mu.Lock()
	if wait, running := e.inflight[cacheKey]; running {
		e.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
		}

		return
	}

	done := make(chan struct{})
	e.inflight[cacheKey] = done
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.inflight, cacheKey)
		e.mu.Unlock()
		close(done)
	}()

	if _, state := e.model.mediaCacheGet(cacheKey); state != mediaCacheMiss {
		return
	}

	description, err := produce()
	if err != nil {
		e.model.logger.Warn("failed to describe media", zap.String("key", cacheKey), zap.Error(err))

		// Every rendering was tried, so trying again later would not help.
		failureTTL := lo.Ternary(errors.Is(err, openai.ErrMediaUnreadable), ttl, mediaFailureTTL)
		e.model.mediaCacheSet(cacheKey, mediaCacheFailedMark, failureTTL)

		return
	}

	e.model.mediaCacheSet(cacheKey, description, ttl)
}

// describePart downloads the file of a media part and describes it. Files that
// are not media are turned into text first; images go through describeImage;
// anything the model cannot read falls back on the still thumbnail.
func (e *mediaEnricher) describePart(ctx context.Context, part *mediaPart) (string, error) {
	maxBytes := e.model.mediaMaxFileBytes()
	if int64(part.fileSize) > maxBytes {
		return "", fmt.Errorf("file is %d bytes, over the %d byte limit", part.fileSize, maxBytes)
	}

	data, mimeType, err := e.download(ctx, part.fileID, maxBytes)
	if err != nil {
		return "", err
	}

	request := openai.MediaDescriptionRequest{Kind: part.describe, MimeType: mimeType, Data: data, Hint: part.hint}

	switch part.document {
	case documentAsText:
		text, err := documentText(data)
		if err != nil {
			return "", err
		}

		request.MimeType, request.Data = "text/plain", []byte(text)
	case documentAsOfficeText:
		text, err := extractOfficeText(data, part.fileName)
		if err != nil {
			return "", err
		}

		request.MimeType, request.Data = "text/plain", []byte(text)
	case documentUnsupported, documentAsFile:
		if request.MimeType == "" {
			return "", errors.New("unsupported file type")
		}
	}

	if strings.HasPrefix(request.MimeType, "image/") {
		return e.describeImage(ctx, request, part.repaint)
	}

	description, err := e.model.openAI.DescribeMedia(ctx, request)
	if err == nil && request.Kind == openai.MediaKindSticker && part.thumbnailFileID != "" {
		return e.reviewVideoSticker(ctx, request, part.thumbnailFileID, description)
	}

	if !errors.Is(err, openai.ErrMediaUnreadable) || part.thumbnailFileID == "" {
		return description, err
	}

	thumbnail, thumbnailMimeType, err := e.download(ctx, part.thumbnailFileID, maxBytes)
	if err != nil {
		return "", err
	}

	request.Data, request.MimeType = thumbnail, thumbnailMimeType
	if request.Kind != openai.MediaKindSticker {
		// A single frame of a GIF or video reads as a photo.
		request.Kind = openai.MediaKindPhoto
	}

	return e.describeImage(ctx, request, part.repaint)
}

// imageVariantsFor lists the renderings to try in order. Monochrome emoji
// start as a silhouette, since Telegram recolors them anyway.
func imageVariantsFor(repaint bool) []imageVariant {
	if repaint {
		return []imageVariant{imageVariantSilhouette, imageVariantContrast, imageVariantInverse, imageVariantEnhance}
	}

	return []imageVariant{imageVariantContrast, imageVariantInverse, imageVariantSilhouette, imageVariantEnhance}
}

// imageVariantForRendering maps the renderings the check model can ask for
// onto the image code that makes them.
var imageVariantForRendering = map[openai.MediaRendering]imageVariant{
	openai.MediaRenderingLightBackground: imageVariantLight,
	openai.MediaRenderingDarkBackground:  imageVariantDark,
	openai.MediaRenderingSilhouette:      imageVariantSilhouette,
	openai.MediaRenderingEnhanceContrast: imageVariantEnhance,
}

// imageDescriptionAttempt is what the media model said about one rendering.
type imageDescriptionAttempt struct {
	description string
	// seen is the rendering the media model saw.
	seen openai.MediaDescriptionRequest
	// rendered is true when the image was transparent or small enough to be
	// re-rendered, which is when content most often hides.
	rendered bool
}

// describeImageRenderings tries renderings of an image until the media model
// can read one: transparency flattened onto a contrasting background, the
// other background, a silhouette, and a contrast stretch. An opaque photo
// that needs none of that is sent as it is.
func (e *mediaEnricher) describeImageRenderings(ctx context.Context, request openai.MediaDescriptionRequest, repaint bool) (imageDescriptionAttempt, error) {
	result := imageDescriptionAttempt{seen: request}
	lastErr := openai.ErrMediaUnreadable
	tried := false

	for _, variant := range imageVariantsFor(repaint) {
		attempt := request

		data, mimeType, err := prepareImage(request.Data, variant)

		switch {
		case err == nil:
			attempt.Data, attempt.MimeType = data, mimeType
			result.rendered = true
		case errors.Is(err, errImageVariantNotApplicable) && variant == imageVariantContrast:
			// Opaque and large enough: the image reads as it is.
		case errors.Is(err, errImageVariantNotApplicable):
			continue
		case tried:
			return result, lastErr
		default:
			// Undecodable: the original is the only rendering there is.
			result.description, err = e.model.openAI.DescribeMedia(ctx, request)
			return result, err
		}

		tried = true
		result.seen = attempt

		description, err := e.model.openAI.DescribeMedia(ctx, attempt)
		if !errors.Is(err, openai.ErrMediaUnreadable) {
			result.description = description
			return result, err
		}

		lastErr = err
	}

	return result, lastErr
}

// describeImage describes an image, then lets the check model review the
// result where the media model is most often wrong without knowing it:
// stickers and custom emoji, transparent or tiny images, and images it could
// not read at all. The check model can run the image code itself through a
// tool before it settles on a description.
func (e *mediaEnricher) describeImage(ctx context.Context, request openai.MediaDescriptionRequest, repaint bool) (string, error) {
	attempt, err := e.describeImageRenderings(ctx, request, repaint)
	if err != nil && !errors.Is(err, openai.ErrMediaUnreadable) {
		return "", err
	}

	if err == nil && request.Kind != openai.MediaKindSticker && !attempt.rendered {
		return attempt.description, nil
	}

	return e.review(ctx, attempt.seen, request.Data, attempt.description, err)
}

// review hands a draft description to the check model together with the
// image the media model saw. original is the still image the check model's
// re-renderings start from. Without a check model, or when the review fails,
// the draft and its error stand.
func (e *mediaEnricher) review(ctx context.Context, seen openai.MediaDescriptionRequest, original []byte, draft string, draftErr error) (string, error) {
	reviewed, reviewErr := e.model.openAI.ReviewMediaDescription(ctx, openai.MediaReviewRequest{
		MediaDescriptionRequest: seen,
		Draft:                   draft,
		Render: func(rendering openai.MediaRendering) ([]byte, string, error) {
			variant, known := imageVariantForRendering[rendering]
			if !known {
				return nil, "", errImageVariantNotApplicable
			}

			return prepareImage(original, variant)
		},
	})

	switch {
	case reviewErr == nil && reviewed != "":
		return reviewed, nil
	case errors.Is(reviewErr, openai.ErrMediaUnreadable):
		return "", reviewErr
	case reviewErr != nil && !errors.Is(reviewErr, openai.ErrMediaReviewUnavailable):
		e.model.logger.Warn("check model failed to review media description, keeping the draft", zap.Error(reviewErr))
	}

	return draft, draftErr
}

// reviewVideoSticker lets the check model verify a description made from a
// video sticker or custom emoji, looking at its still thumbnail, since the
// check model reads images but not WebM.
func (e *mediaEnricher) reviewVideoSticker(ctx context.Context, request openai.MediaDescriptionRequest, thumbnailFileID string, draft string) (string, error) {
	thumbnail, mimeType, err := e.download(ctx, thumbnailFileID, e.model.mediaMaxFileBytes())
	if err != nil || !strings.HasPrefix(mimeType, "image/") {
		return draft, nil
	}

	seen := request
	seen.Data, seen.MimeType = thumbnail, mimeType

	if data, renderedMimeType, renderErr := prepareImage(thumbnail, imageVariantContrast); renderErr == nil {
		seen.Data, seen.MimeType = data, renderedMimeType
	}

	seen.Hint = strings.TrimSpace(seen.Hint + " The description was made from the animated sticker; this is its first frame.")

	return e.review(ctx, seen, thumbnail, draft, nil)
}

// download fetches a Telegram file and guesses its MIME type from the
// extension, or returns an empty type for files that are not media. The file
// URL carries the bot token, so errors never include it.
func (e *mediaEnricher) download(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, error) {
	source := e.fileSource()
	if source == nil {
		return nil, "", errors.New("telegram file source is not set")
	}

	fileURL, err := source.GetFileDirectURL(fileID)
	if err != nil {
		return nil, "", fmt.Errorf("get telegram file: %w", err)
	}

	parsed, err := url.Parse(fileURL)
	if err != nil {
		return nil, "", errors.New("telegram returned an unreadable file URL")
	}

	mimeType := mediaMimeTypes[strings.ToLower(path.Ext(parsed.Path))]

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, "", errors.New("failed to build the telegram file request")
	}

	response, err := e.httpClient.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}

		return nil, "", fmt.Errorf("download telegram file: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download telegram file: status %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read telegram file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("file is over the %d byte limit", maxBytes)
	}

	return data, mimeType, nil
}

type customEmojiPhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
}

type customEmojiSticker struct {
	CustomEmojiID   string                `json:"custom_emoji_id"`
	FileID          string                `json:"file_id"`
	FileSize        int                   `json:"file_size"`
	Emoji           string                `json:"emoji"`
	IsAnimated      bool                  `json:"is_animated"`
	NeedsRepainting bool                  `json:"needs_repainting"`
	Thumbnail       *customEmojiPhotoSize `json:"thumbnail"`
	Thumb           *customEmojiPhotoSize `json:"thumb"`
}

func (s customEmojiSticker) thumbnail() *customEmojiPhotoSize {
	if s.Thumbnail != nil {
		return s.Thumbnail
	}

	return s.Thumb
}

// mediaPart describes the custom emoji from its own file, or from its
// thumbnail when it is an animated TGS. It returns nil when neither can be
// read.
func (s customEmojiSticker) mediaPart(chatID int64) *mediaPart {
	part := &mediaPart{
		describe: openai.MediaKindSticker,
		fileID:   s.FileID,
		fileSize: s.FileSize,
		hint:     emojiHint(s.Emoji),
		repaint:  s.NeedsRepainting,
		cacheKey: customEmojiCacheKey(chatID, s.CustomEmojiID),
		cacheTTL: mediaLongLivedTTL,
	}

	thumbnail := s.thumbnail()

	switch {
	case s.IsAnimated && thumbnail == nil:
		return nil
	case s.IsAnimated:
		part.fileID, part.fileSize = thumbnail.FileID, thumbnail.FileSize
	case thumbnail != nil:
		part.thumbnailFileID = thumbnail.FileID
	}

	return part
}

func (e *mediaEnricher) customEmojiStickers(ids []string) ([]customEmojiSticker, error) {
	source := e.fileSource()
	if source == nil {
		return nil, errors.New("telegram file source is not set")
	}

	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}

	response, err := source.MakeRequest("getCustomEmojiStickers", tgbotapi.Params{"custom_emoji_ids": string(encoded)})
	if err != nil {
		return nil, err
	}

	var stickers []customEmojiSticker
	if err := json.Unmarshal(response.Result, &stickers); err != nil {
		return nil, fmt.Errorf("decode custom emoji stickers: %w", err)
	}

	return stickers, nil
}
