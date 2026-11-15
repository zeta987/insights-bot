package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// LogChatHistoriesRecap holds the schema definition for the LogChatHistoriesRecap entity.
type LogChatHistoriesRecap struct {
	ent.Schema
}

// Fields of the LogChatHistoriesRecap.
func (LogChatHistoriesRecap) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique().Immutable(),
		field.Int64("chat_id").Default(0),
		field.Text("recap_inputs").Default(""),
		field.Text("recap_outputs").Default(""),
		field.Int("from_platform").Default(0),
		field.Int("prompt_token_usage").Default(0),
		field.Int("completion_token_usage").Default(0),
		field.Int("total_token_usage").Default(0),
		field.Int("recap_type").Default(0),
		field.String("model_name").Default(""),
		// first_message_id and last_message_id are the first and last chat
		// message IDs given to the model. Rows written before these fields
		// existed keep 0 and are never reused.
		field.Int64("first_message_id").Default(0),
		field.Int64("last_message_id").Default(0),
		// reused_recap_count is how many earlier recaps were merged into this
		// one; 0 means it was summarized from the messages themselves.
		field.Int("reused_recap_count").Default(0),
		field.Int64("created_at").DefaultFunc(func() int64 { return time.Now().UnixMilli() }),
		field.Int64("updated_at").DefaultFunc(func() int64 { return time.Now().UnixMilli() }),
	}
}

// Edges of the LogChatHistoriesRecap.
func (LogChatHistoriesRecap) Edges() []ent.Edge {
	return nil
}
