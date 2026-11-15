package tgbot

import (
	"encoding/json"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// customEmojiURLPrefix is Telegram's own link form for a custom emoji. The
// library predates custom emoji and drops custom_emoji_id, so PatchUpdate
// stores the id in the otherwise unused URL field of the entity.
const customEmojiURLPrefix = "tg://emoji?id="

// CustomEmojiID returns the custom emoji id that PatchUpdate stored on a
// "custom_emoji" entity, or an empty string.
func CustomEmojiID(entity tgbotapi.MessageEntity) string {
	if entity.Type != "custom_emoji" || !strings.HasPrefix(entity.URL, customEmojiURLPrefix) {
		return ""
	}

	return strings.TrimPrefix(entity.URL, customEmojiURLPrefix)
}

type rawPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size"`
}

type rawEntity struct {
	Type          string `json:"type"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type rawSticker struct {
	Thumbnail *rawPhotoSize `json:"thumbnail"`
}

type rawMessage struct {
	Entities        []rawEntity `json:"entities"`
	CaptionEntities []rawEntity `json:"caption_entities"`
	Sticker         *rawSticker `json:"sticker"`
	ReplyToMessage  *rawMessage `json:"reply_to_message"`
}

type rawUpdate struct {
	Message           *rawMessage `json:"message"`
	EditedMessage     *rawMessage `json:"edited_message"`
	ChannelPost       *rawMessage `json:"channel_post"`
	EditedChannelPost *rawMessage `json:"edited_channel_post"`
}

// PatchUpdate copies fields that the bundled Bot API library does not know
// from the raw update body into the decoded update: custom emoji ids on
// message and caption entities, and the sticker thumbnail that Bot API 6.6
// renamed from "thumb" to "thumbnail".
func PatchUpdate(update *tgbotapi.Update, body []byte) {
	if update == nil {
		return
	}

	var raw rawUpdate
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}

	patchMessage(update.Message, raw.Message)
	patchMessage(update.EditedMessage, raw.EditedMessage)
	patchMessage(update.ChannelPost, raw.ChannelPost)
	patchMessage(update.EditedChannelPost, raw.EditedChannelPost)
}

func patchMessage(message *tgbotapi.Message, raw *rawMessage) {
	if message == nil || raw == nil {
		return
	}

	patchEntities(message.Entities, raw.Entities)
	patchEntities(message.CaptionEntities, raw.CaptionEntities)

	if message.Sticker != nil && message.Sticker.Thumbnail == nil && raw.Sticker != nil && raw.Sticker.Thumbnail != nil {
		thumbnail := raw.Sticker.Thumbnail
		message.Sticker.Thumbnail = &tgbotapi.PhotoSize{
			FileID:       thumbnail.FileID,
			FileUniqueID: thumbnail.FileUniqueID,
			Width:        thumbnail.Width,
			Height:       thumbnail.Height,
			FileSize:     thumbnail.FileSize,
		}
	}

	patchMessage(message.ReplyToMessage, raw.ReplyToMessage)
}

func patchEntities(entities []tgbotapi.MessageEntity, raw []rawEntity) {
	if len(entities) != len(raw) {
		return
	}

	for i := range entities {
		if entities[i].Type != "custom_emoji" || raw[i].Type != "custom_emoji" || raw[i].CustomEmojiID == "" {
			continue
		}

		entities[i].URL = customEmojiURLPrefix + raw[i].CustomEmojiID
	}
}
