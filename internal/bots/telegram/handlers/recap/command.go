package recap

import (
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/internal/models/tgchats"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/logger"
	"github.com/nekomeowww/insights-bot/pkg/types/bot/handlers/recap"
	"github.com/nekomeowww/insights-bot/pkg/types/tgchat"
	"github.com/samber/lo"
	"go.uber.org/fx"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Define the route constant for selecting hour callback
const SelectHourAction = "recap/select-hour"

type NewCommandHandlerParams struct {
	fx.In

	Config        *configs.Config
	Logger        *logger.Logger
	TgChats       *tgchats.Model
	ChatHistories *chathistories.Model
	Redis         *datastore.Redis
}

type CommandHandler struct {
	config        *configs.Config
	logger        *logger.Logger
	tgchats       *tgchats.Model
	chathistories *chathistories.Model
	redis         *datastore.Redis
}

func NewRecapCommandHandler() func(NewCommandHandlerParams) *CommandHandler {
	return func(param NewCommandHandlerParams) *CommandHandler {
		return &CommandHandler{
			config:        param.Config,
			logger:        param.Logger,
			tgchats:       param.TgChats,
			chathistories: param.ChatHistories,
			redis:         param.Redis,
		}
	}
}

// newRecapSelectHoursInlineKeyboardButtons creates the hours selection buttons for recap
func newRecapSelectHoursInlineKeyboardButtons(c *tgbot.Context, chatID int64, chatTitle string, recapMode tgchat.AutoRecapSendMode) (tgbotapi.InlineKeyboardMarkup, error) {
	buttons := make([][]tgbotapi.InlineKeyboardButton, 0)
	buttonRow := make([]tgbotapi.InlineKeyboardButton, 0)

	for i, hour := range RecapSelectHourAvailable {
		callbackData, marshalErr := c.Bot.AssignOneCallbackQueryData(SelectHourAction, recap.SelectHourCallbackQueryData{
			ChatID:    chatID,
			ChatTitle: chatTitle,
			Hour:      hour,
			RecapMode: recapMode,
		})
		buttonRow = append(buttonRow, tgbotapi.NewInlineKeyboardButtonData(
			RecapSelectHourAvailableText[hour],
			lo.Must(callbackData, marshalErr),
		))

		if (i+1)%3 == 0 || i == len(RecapSelectHourAvailable)-1 {
			buttons = append(buttons, buttonRow)
			buttonRow = make([]tgbotapi.InlineKeyboardButton, 0)
		}
	}

	return tgbotapi.NewInlineKeyboardMarkup(buttons...), nil
}
