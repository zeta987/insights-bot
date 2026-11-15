package chathistories

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/rueidis"
	"github.com/samber/lo"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/types/redis"
)

const (
	// mediaPhotoMaxSide keeps photo descriptions affordable while screenshots
	// stay readable: Telegram offers 90, 320, 800 and 1280 pixel sizes.
	mediaPhotoMaxSide = 1280

	mediaShortLivedTTL   = 14 * 24 * time.Hour
	mediaLongLivedTTL    = 180 * 24 * time.Hour
	mediaFailureTTL      = 30 * time.Minute
	mediaCacheFailedMark = "\x00failed"
)

type mediaCacheState int

const (
	mediaCacheMiss mediaCacheState = iota
	mediaCacheHit
	mediaCacheFailed
)

// mediaPart is the media of one message as it appears in chat histories.
type mediaPart struct {
	// marker names the media inside the brackets, such as "photo" or
	// "voice 0:12".
	marker string
	// describe is empty for media that only gets a placeholder.
	describe openai.MediaKind
	fileID   string
	fileSize int
	// thumbnailFileID is a still image to fall back on when the model cannot
	// read the file itself, such as the first frame of a video sticker.
	thumbnailFileID string
	// hint gives the model context, such as the emoji a sticker stands for.
	hint string
	// repaint marks monochrome custom emoji that Telegram recolors.
	repaint  bool
	document documentHandling
	fileName string
	cacheKey string
	cacheTTL time.Duration
}

func formatFileSize(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%d KB", size/(1<<10))
	case size > 0:
		return fmt.Sprintf("%d B", size)
	default:
		return ""
	}
}

func thumbnailID(thumbnail *tgbotapi.PhotoSize) string {
	if thumbnail == nil {
		return ""
	}

	return thumbnail.FileID
}

func emojiHint(emoji string) string {
	if emoji == "" {
		return ""
	}

	return "It stands for the emoji " + emoji + "."
}

func (p *mediaPart) render(description string) string {
	if description == "" {
		return "[" + p.marker + "]"
	}

	return "[" + p.marker + ": " + description + "]"
}

func formatMediaDuration(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds%3600/60, seconds%60)
	}

	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// mediaCacheKey scopes every description to one chat, so no group ever reads
// a description made for another group, even of the same file.
func mediaCacheKey(chatID int64, kind string, uniqueID string) string {
	return fmt.Sprintf("%d/%s/%s", chatID, kind, uniqueID)
}

func customEmojiCacheKey(chatID int64, id string) string {
	return mediaCacheKey(chatID, "custom_emoji", id)
}

func messageChatID(message *tgbotapi.Message) int64 {
	if message == nil || message.Chat == nil {
		return 0
	}

	return message.Chat.ID
}

func (m *Model) mediaEnabled() bool {
	return m.config != nil && m.config.MediaUnderstanding.Enabled
}

func (m *Model) mediaMaxFileBytes() int64 {
	if m.config == nil || m.config.MediaUnderstanding.MaxFileBytes <= 0 {
		return 10 << 20
	}

	return m.config.MediaUnderstanding.MaxFileBytes
}

// pickPhotoSize returns the largest size that fits mediaPhotoMaxSide and the
// file limit, or the smallest size when none fits.
func pickPhotoSize(sizes []tgbotapi.PhotoSize, maxBytes int64) tgbotapi.PhotoSize {
	best := sizes[0]
	found := false

	for _, size := range sizes {
		if size.Width > mediaPhotoMaxSide || size.Height > mediaPhotoMaxSide || int64(size.FileSize) > maxBytes {
			continue
		}
		if !found || size.Width*size.Height > best.Width*best.Height {
			best = size
			found = true
		}
	}

	if found {
		return best
	}

	for _, size := range sizes {
		if size.Width*size.Height < best.Width*best.Height {
			best = size
		}
	}

	return best
}

// messageMediaPart describes the media a message carries, or returns nil for a
// plain text message.
func (m *Model) messageMediaPart(message *tgbotapi.Message) *mediaPart {
	chatID := messageChatID(message)

	switch {
	case len(message.Photo) > 0:
		photo := pickPhotoSize(message.Photo, m.mediaMaxFileBytes())

		return &mediaPart{
			marker:   "photo",
			describe: openai.MediaKindPhoto,
			fileID:   photo.FileID,
			fileSize: photo.FileSize,
			cacheKey: mediaCacheKey(chatID, "photo", photo.FileUniqueID),
			cacheTTL: mediaShortLivedTTL,
		}
	case message.Sticker != nil:
		return stickerMediaPart(chatID, message.Sticker)
	case message.Voice != nil:
		part := &mediaPart{marker: "voice " + formatMediaDuration(message.Voice.Duration)}
		if m.config != nil && message.Voice.Duration <= m.config.MediaUnderstanding.MaxVoiceSeconds {
			part.describe = openai.MediaKindVoice
			part.fileID = message.Voice.FileID
			part.fileSize = message.Voice.FileSize
			part.cacheKey = mediaCacheKey(chatID, "voice", message.Voice.FileUniqueID)
			part.cacheTTL = mediaShortLivedTTL
		}

		return part
	case message.VideoNote != nil:
		note := message.VideoNote
		part := &mediaPart{marker: "round video " + formatMediaDuration(note.Duration)}
		m.describeVideoPart(part, chatID, openai.MediaKindVideo, note.FileID, note.FileUniqueID, note.FileSize, note.Duration, note.Thumbnail)

		return part
	case message.Animation != nil:
		animation := message.Animation
		part := &mediaPart{marker: "GIF"}
		m.describeVideoPart(part, chatID, openai.MediaKindAnimation, animation.FileID, animation.FileUniqueID, animation.FileSize, animation.Duration, animation.Thumbnail)
		part.cacheTTL = mediaLongLivedTTL // GIFs get reposted

		return part
	case message.Video != nil:
		video := message.Video
		part := &mediaPart{marker: "video " + formatMediaDuration(video.Duration)}
		m.describeVideoPart(part, chatID, openai.MediaKindVideo, video.FileID, video.FileUniqueID, video.FileSize, video.Duration, video.Thumbnail)

		return part
	case message.Audio != nil:
		return m.audioMediaPart(chatID, message.Audio)
	case message.Document != nil:
		return m.documentMediaPart(chatID, message.Document)
	default:
		return nil
	}
}

// describeVideoPart makes a GIF or video describable when it is short and
// small enough to download.
func (m *Model) describeVideoPart(part *mediaPart, chatID int64, kind openai.MediaKind, fileID, uniqueID string, fileSize, duration int, thumbnail *tgbotapi.PhotoSize) {
	if m.config == nil || duration > m.config.MediaUnderstanding.MaxVideoSeconds || int64(fileSize) > m.mediaMaxFileBytes() {
		return
	}

	part.describe = kind
	part.fileID = fileID
	part.fileSize = fileSize
	part.thumbnailFileID = thumbnailID(thumbnail)
	part.cacheKey = mediaCacheKey(chatID, string(kind), uniqueID)
	part.cacheTTL = mediaShortLivedTTL
}

func (m *Model) audioMediaPart(chatID int64, audio *tgbotapi.Audio) *mediaPart {
	name := strings.Join(lo.Compact([]string{audio.Performer, audio.Title}), " - ")
	if name == "" {
		name = audio.FileName
	}

	part := &mediaPart{marker: strings.TrimSpace("audio " + name + " " + formatMediaDuration(audio.Duration))}
	if m.config == nil || audio.Duration > m.config.MediaUnderstanding.MaxVoiceSeconds || int64(audio.FileSize) > m.mediaMaxFileBytes() {
		return part
	}

	part.describe = openai.MediaKindAudio
	part.fileID = audio.FileID
	part.fileSize = audio.FileSize
	part.hint = strings.TrimSpace("File name: " + audio.FileName)
	part.cacheKey = mediaCacheKey(chatID, "audio", audio.FileUniqueID)
	part.cacheTTL = mediaShortLivedTTL

	return part
}

func (m *Model) documentMediaPart(chatID int64, document *tgbotapi.Document) *mediaPart {
	part := &mediaPart{marker: strings.TrimSpace(strings.Join(lo.Compact([]string{"file", document.FileName, formatFileSize(document.FileSize)}), " "))}

	kind, handling := documentKindAndHandling(document.FileName, document.MimeType)
	if handling == documentUnsupported || int64(document.FileSize) > m.mediaMaxFileBytes() {
		return part
	}

	part.describe = kind
	part.document = handling
	part.fileID = document.FileID
	part.fileSize = document.FileSize
	part.fileName = document.FileName
	part.thumbnailFileID = thumbnailID(document.Thumbnail)
	part.hint = "File name: " + document.FileName
	part.cacheKey = mediaCacheKey(chatID, "document", document.FileUniqueID)
	part.cacheTTL = mediaShortLivedTTL

	return part
}

// stickerMediaPart describes a sticker from its own file, or from its
// thumbnail when the sticker is an animated TGS that models cannot read.
func stickerMediaPart(chatID int64, sticker *tgbotapi.Sticker) *mediaPart {
	part := &mediaPart{marker: strings.TrimSpace("sticker " + sticker.Emoji)}

	fileID, fileSize := sticker.FileID, sticker.FileSize
	if sticker.IsAnimated {
		if sticker.Thumbnail == nil {
			return part
		}

		fileID, fileSize = sticker.Thumbnail.FileID, sticker.Thumbnail.FileSize
	} else {
		part.thumbnailFileID = thumbnailID(sticker.Thumbnail)
	}

	part.describe = openai.MediaKindSticker
	part.fileID = fileID
	part.fileSize = fileSize
	part.hint = emojiHint(sticker.Emoji)
	part.cacheKey = mediaCacheKey(chatID, "sticker", sticker.FileUniqueID)
	part.cacheTTL = mediaLongLivedTTL

	return part
}

func messageTextEntities(message *tgbotapi.Message) []tgbotapi.MessageEntity {
	if message.Caption != "" {
		return message.CaptionEntities
	}

	return message.Entities
}

func customEmojiIDs(entities []tgbotapi.MessageEntity) []string {
	ids := make([]string, 0)

	for _, entity := range entities {
		if id := tgbot.CustomEmojiID(entity); id != "" {
			ids = append(ids, id)
		}
	}

	return lo.Uniq(ids)
}

func (m *Model) mediaCacheGet(key string) (string, mediaCacheState) {
	if m.redis == nil || key == "" {
		return "", mediaCacheMiss
	}

	value, err := m.redis.Do(context.Background(), m.redis.B().Get().Key(redis.MediaDescription1.Format(key)).Build()).ToString()
	if err != nil {
		if !rueidis.IsRedisNil(err) {
			m.logger.Warn("failed to read media description cache", zap.String("key", key), zap.Error(err))
		}

		return "", mediaCacheMiss
	}
	if value == mediaCacheFailedMark {
		return "", mediaCacheFailed
	}

	return value, mediaCacheHit
}

func (m *Model) mediaCacheSet(key string, value string, ttl time.Duration) {
	if m.redis == nil || key == "" {
		return
	}

	err := m.redis.Do(context.Background(), m.redis.B().Set().Key(redis.MediaDescription1.Format(key)).Value(value).Ex(ttl).Build()).Error()
	if err != nil {
		m.logger.Warn("failed to write media description cache", zap.String("key", key), zap.Error(err))
	}
}

// cachedCustomEmojiLabels returns the cached label of every custom emoji in
// the entities that has one.
func (m *Model) cachedCustomEmojiLabels(chatID int64, entities []tgbotapi.MessageEntity) map[string]string {
	if !m.mediaEnabled() {
		return nil
	}

	labels := make(map[string]string)

	for _, id := range customEmojiIDs(entities) {
		if label, state := m.mediaCacheGet(customEmojiCacheKey(chatID, id)); state == mediaCacheHit {
			labels[id] = label
		}
	}

	return labels
}

// composeHistoryText builds the text stored for one message: the media marker,
// the message text with link titles and custom emoji labels, and, when asked,
// the forwarded-from prefix. needsMedia reports media or custom emoji that
// have no cached description yet.
func (m *Model) composeHistoryText(message *tgbotapi.Message, withForwardPrefix bool) (string, bool, error) {
	if message == nil {
		return "", false, nil
	}

	var media *mediaPart
	if m.mediaEnabled() {
		media = m.messageMediaPart(message)
	}

	if message.Text == "" && message.Caption == "" && media == nil {
		return "", false, nil
	}

	parts := make([]string, 0, 2)
	needsMedia := false

	if media != nil {
		description, state := m.mediaCacheGet(media.cacheKey)
		parts = append(parts, media.render(description))
		needsMedia = media.describe != "" && state == mediaCacheMiss
	}

	if message.Text != "" || message.Caption != "" {
		text, err := m.extractTextWithSummarization(message)
		if err != nil {
			return "", false, err
		}

		parts = append(parts, text)
	}

	if m.mediaEnabled() && !needsMedia {
		for _, id := range customEmojiIDs(messageTextEntities(message)) {
			if _, state := m.mediaCacheGet(customEmojiCacheKey(messageChatID(message), id)); state == mediaCacheMiss {
				needsMedia = true
				break
			}
		}
	}

	text := strings.TrimSpace(strings.Join(lo.Compact(parts), " "))
	if text == "" {
		return "", false, nil
	}

	if withForwardPrefix {
		if message.ForwardFrom != nil {
			text = fmt.Sprintf("[forwarded from %s]: %s", tgbot.FullNameFromFirstAndLastName(message.ForwardFrom.FirstName, message.ForwardFrom.LastName), text)
		} else if message.ForwardFromChat != nil {
			text = fmt.Sprintf("[forwarded from %s]: %s", message.ForwardFromChat.Title, text)
		}
	}

	return text, needsMedia, nil
}
