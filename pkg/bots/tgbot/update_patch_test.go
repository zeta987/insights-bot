package tgbot

import (
	"encoding/json"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/require"
)

const patchUpdateFixture = `{
  "update_id": 1,
  "message": {
    "message_id": 10,
    "date": 1763251200,
    "chat": {"id": -100123, "type": "supergroup", "title": "test"},
    "from": {"id": 42, "is_bot": false, "first_name": "Tester"},
    "text": "好耶 👅 和 👍",
    "entities": [
      {"type": "bold", "offset": 0, "length": 2},
      {"type": "custom_emoji", "offset": 3, "length": 2, "custom_emoji_id": "5368324170671202286"},
      {"type": "custom_emoji", "offset": 8, "length": 2, "custom_emoji_id": "5370869711888194012"}
    ],
    "reply_to_message": {
      "message_id": 9,
      "date": 1763251100,
      "chat": {"id": -100123, "type": "supergroup", "title": "test"},
      "from": {"id": 43, "is_bot": false, "first_name": "Other"},
      "sticker": {
        "file_id": "sticker-file", "file_unique_id": "sticker-unique",
        "type": "regular", "width": 512, "height": 512,
        "is_animated": true, "is_video": false, "emoji": "😂", "set_name": "pack",
        "thumbnail": {"file_id": "thumb-file", "file_unique_id": "thumb-unique", "width": 128, "height": 128, "file_size": 4000}
      }
    }
  }
}`

func decodeFixture(t *testing.T, body string) *tgbotapi.Update {
	t.Helper()

	var update tgbotapi.Update

	require.NoError(t, json.Unmarshal([]byte(body), &update))

	return &update
}

func TestPatchUpdateRecoversCustomEmojiIDs(t *testing.T) {
	update := decodeFixture(t, patchUpdateFixture)
	require.Empty(t, CustomEmojiID(update.Message.Entities[1]), "the library drops custom_emoji_id on its own")

	PatchUpdate(update, []byte(patchUpdateFixture))

	require.Empty(t, CustomEmojiID(update.Message.Entities[0]))
	require.Equal(t, "5368324170671202286", CustomEmojiID(update.Message.Entities[1]))
	require.Equal(t, "5370869711888194012", CustomEmojiID(update.Message.Entities[2]))
}

func TestPatchUpdateRecoversStickerThumbnailInReply(t *testing.T) {
	update := decodeFixture(t, patchUpdateFixture)
	require.Nil(t, update.Message.ReplyToMessage.Sticker.Thumbnail, "the library reads the old thumb field")

	PatchUpdate(update, []byte(patchUpdateFixture))

	thumbnail := update.Message.ReplyToMessage.Sticker.Thumbnail
	require.NotNil(t, thumbnail)
	require.Equal(t, "thumb-file", thumbnail.FileID)
	require.Equal(t, "thumb-unique", thumbnail.FileUniqueID)
}

func TestPatchUpdatePatchesCaptionEntitiesOfEditedMessage(t *testing.T) {
	body := `{
  "update_id": 2,
  "edited_message": {
    "message_id": 11, "date": 1763251200, "edit_date": 1763251300,
    "chat": {"id": -100123, "type": "supergroup"},
    "caption": "👅",
    "caption_entities": [{"type": "custom_emoji", "offset": 0, "length": 2, "custom_emoji_id": "777"}]
  }
}`
	update := decodeFixture(t, body)

	PatchUpdate(update, []byte(body))

	require.Equal(t, "777", CustomEmojiID(update.EditedMessage.CaptionEntities[0]))
}

func TestPatchUpdateIgnoresInvalidBody(t *testing.T) {
	update := decodeFixture(t, patchUpdateFixture)

	PatchUpdate(update, []byte("not json"))
	PatchUpdate(nil, []byte(patchUpdateFixture))

	require.Empty(t, CustomEmojiID(update.Message.Entities[1]))
}
