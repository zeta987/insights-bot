package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// TelegramChat holds the schema definition for the TelegramChat entity.
type TelegramChat struct {
	ent.Schema
}

// Fields of the TelegramChat.
func (TelegramChat) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").
			Comment("Telegram chat ID").
			Unique(),
		field.String("type").
			Comment("Telegram chat type (private, group, supergroup, channel)"),
		field.String("title").
			Optional().
			Comment("Title of the chat (for groups, supergroups and channels)"),
		field.String("username").
			Optional().
			Comment("Username of the chat"),
		field.Bool("is_forum").
			Default(false).
			Comment("True if the chat is a forum (supergroup)"),
	}
}

// Edges of the TelegramChat.
func (TelegramChat) Edges() []ent.Edge {
	return nil
}
