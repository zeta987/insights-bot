package recap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/samber/lo"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/internal/models/tgchats"
	TelegraphService "github.com/nekomeowww/insights-bot/internal/services/telegraph"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/logger"
	"github.com/nekomeowww/insights-bot/pkg/types/bot/handlers/recap"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/nekomeowww/insights-bot/pkg/types/tgchat"
)

func formatToHTML(text string) string {
	var html strings.Builder
	inBold := false
	inItalic := false

	for i := 0; i < len(text); i++ {
		char := text[i]
		switch char {
		case '*':
			if inBold {
				html.WriteString("</b>")
			} else {
				html.WriteString("<b>")
			}
			inBold = !inBold
		case '_':
			if inItalic {
				html.WriteString("</i>")
			} else {
				html.WriteString("<i>")
			}
			inItalic = !inItalic
		default:
			html.WriteByte(char)
		}
	}
	// Close any unclosed tags
	if inBold {
		html.WriteString("</b>")
	}
	if inItalic {
		html.WriteString("</i>")
	}
	return html.String()
}

type NewCallbackQueryHandlerParams struct {
	fx.In

	Logger        *logger.Logger
	ChatHistories *chathistories.Model
	TgChats       *tgchats.Model
	Telegraph     *TelegraphService.Service // Inject Telegraph Service
}

type CallbackQueryHandler struct {
	logger        *logger.Logger
	chatHistories *chathistories.Model
	tgchats       *tgchats.Model
	telegraph     *TelegraphService.Service // Store Telegraph service
}

func NewCallbackQueryHandler() func(NewCallbackQueryHandlerParams) *CallbackQueryHandler {
	return func(param NewCallbackQueryHandlerParams) *CallbackQueryHandler {
		return &CallbackQueryHandler{
			logger:        param.Logger,
			chatHistories: param.ChatHistories,
			tgchats:       param.TgChats,
			telegraph:     param.Telegraph, // Initialize telegraph field
		}
	}
}

func shouldSkipCallbackQueryHandlingByCheckingActionData[
	D recap.ConfigureRecapToggleActionData | recap.ConfigureRecapAssignModeActionData | recap.ConfigureRecapCompleteActionData | recap.ConfigureAutoRecapRatesPerDayActionData,
](c *tgbot.Context, actionData D, chatID, fromID int64) bool {
	var actionDataChatID int64
	var actionDataFromID int64

	switch val := any(actionData).(type) {
	case recap.ConfigureRecapToggleActionData:
		actionDataChatID = val.ChatID
		actionDataFromID = val.FromID
	case recap.ConfigureRecapAssignModeActionData:
		actionDataChatID = val.ChatID
		actionDataFromID = val.FromID
	case recap.ConfigureRecapCompleteActionData:
		actionDataChatID = val.ChatID
		actionDataFromID = val.FromID
	case recap.ConfigureAutoRecapRatesPerDayActionData:
		actionDataChatID = val.ChatID
		actionDataFromID = val.FromID
	}

	// same chat
	if actionDataChatID != chatID {
		c.Logger.Debug("callback query is not from the same chat",
			zap.Int64("chat_id", chatID),
			zap.Int64("action_data_chat_id", actionDataChatID),
		)

		return true
	}
	// same actor or the original command should be sent by Group Anonymous Bot
	callbackQueryMessageFromGroupAnonymousBot := c.Update.CallbackQuery.Message.ReplyToMessage != nil && c.Bot.IsGroupAnonymousBot(c.Update.CallbackQuery.Message.ReplyToMessage.From)
	if !(actionDataFromID == fromID || callbackQueryMessageFromGroupAnonymousBot) {
		c.Logger.Debug("action skipped, because callback query is neither from the same actor nor the original command should sent by Group Anonymous Bot",
			zap.Int64("from_id", fromID),
			zap.Int64("action_data_from_id", actionDataFromID),
			zap.Bool("has_reply_to_message", c.Update.CallbackQuery.Message.ReplyToMessage != nil),
			zap.Bool("is_group_anonymous_bot", c.Update.CallbackQuery.Message.ReplyToMessage != nil && c.Bot.IsGroupAnonymousBot(c.Update.CallbackQuery.Message.ReplyToMessage.From)),
		)

		return true
	}

	return false
}

func (h *CallbackQueryHandler) handleCallbackQueryToggle(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	generalErrorMessage := configureRecapGeneralInstructionMessage + "\n\n" + "应用聊天记录回顾功能的配置时出现了问题，请稍后再试！"

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID
	chatTitle := msg.Chat.Title
	chatType := msg.Chat.Type
	messageID := msg.MessageID

	var actionData recap.ConfigureRecapToggleActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	shouldSkip := shouldSkipCallbackQueryHandlingByCheckingActionData(c, actionData, chatID, fromID)
	if shouldSkip {
		return nil, nil
	}

	// check whether the actor is admin or creator, and whether the bot is admin
	err = checkToggle(c, chatID, c.Update.CallbackQuery.From)
	if err != nil {
		if errors.Is(err, errAdministratorPermissionRequired) {
			h.logger.Debug("action, skipped, callback query is not from an admin or creator",
				zap.Int64("from_id", fromID),
				zap.Int64("chat_id", chatID),
				zap.String("permission_check_result", err.Error()),
			)

			return nil, nil
		}
		if errors.Is(err, errOperationCanNotBeDone) {
			return nil, tgbot.
				NewMessageError(configureRecapGeneralInstructionMessage + "\n\n" + err.Error()).
				WithEdit(msg).
				WithParseModeHTML().
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}

		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	options, err := h.tgchats.FindOneRecapsOption(chatID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾功能，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	if actionData.Status {
		errMessage := configureRecapGeneralInstructionMessage + "\n\n" + "聊天记录回顾功能开启失败，请稍后再试！"

		err = h.tgchats.EnableChatHistoriesRecapForGroups(chatID, telegram.ChatType(chatType), chatTitle)
		if err != nil {
			return nil, tgbot.
				NewExceptionError(err).
				WithMessage(errMessage).
				WithEdit(msg).
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}

		err = h.tgchats.QueueOneSendChatHistoriesRecapTaskForChatID(chatID, options)
		if err != nil {
			return nil, tgbot.
				NewExceptionError(err).
				WithMessage(errMessage).
				WithEdit(msg).
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}
	} else {
		errMessage := configureRecapGeneralInstructionMessage + "\n\n" + "聊天记录回顾功能关闭失败，请稍后再试！"

		err = h.tgchats.DisableChatHistoriesRecapForGroups(chatID, telegram.ChatType(chatType), chatTitle)
		if err != nil {
			return nil, tgbot.
				NewExceptionError(err).
				WithMessage(errMessage).
				WithEdit(msg).
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}
	}

	markup, err := newRecapInlineKeyboardMarkup(
		c,
		chatID,
		fromID,
		actionData.Status,
		tgchat.AutoRecapSendMode(options.AutoRecapSendMode),
		lo.Ternary(options.AutoRecapRatesPerDay == 0, 4, options.AutoRecapRatesPerDay),
		options.PinAutoRecapMessage,
	)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾功能，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	return c.NewEditMessageTextAndReplyMarkup(messageID,
		lo.Ternary(
			actionData.Status,
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾功能已开启，开启后将会自动收集群组中的聊天记录并定时发送聊天回顾快报。",
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾功能已关闭，关闭后将不会再收集群组中的聊天记录了。",
		),
		markup,
	), nil
}

func (h *CallbackQueryHandler) handleCallbackQueryAssignMode(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	generalErrorMessage := configureRecapGeneralInstructionMessage + "\n\n" + "应用聊天记录回顾功能的配置时出现了问题，请稍后再试！"

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID
	chatTitle := msg.Chat.Title
	messageID := msg.MessageID

	var actionData recap.ConfigureRecapAssignModeActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	shouldSkip := shouldSkipCallbackQueryHandlingByCheckingActionData(c, actionData, chatID, fromID)
	if shouldSkip {
		return nil, nil
	}

	// check whether the actor is admin or creator, and whether the bot is admin
	err = checkAssignMode(c, chatID, c.Update.CallbackQuery.From)
	if err != nil {
		if errors.Is(err, errAdministratorPermissionRequired) {
			h.logger.Debug("action skipped, callback query is not from an admin or creator",
				zap.Int64("from_id", fromID),
				zap.Int64("chat_id", chatID),
				zap.String("permission_check_result", err.Error()),
			)

			return nil, nil
		}
		if errors.Is(err, errOperationCanNotBeDone) || errors.Is(err, errCreatorPermissionRequired) {
			return nil, tgbot.
				NewMessageError(configureRecapGeneralInstructionMessage + "\n\n" + err.Error()).
				WithEdit(msg).
				WithParseModeHTML().
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}

		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	err = h.tgchats.SetRecapsRecapMode(chatID, actionData.Mode)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	h.logger.Info("assigned recap mode for chat", zap.String("recap_mode", actionData.Mode.String()))

	has, err := h.tgchats.HasChatHistoriesRecapEnabledForGroups(chatID, chatTitle)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "聊天记录回顾模式设定失败，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	options, err := h.tgchats.FindOneRecapsOption(chatID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾功能，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	markup, err := newRecapInlineKeyboardMarkup(
		c,
		chatID,
		fromID,
		has,
		actionData.Mode,
		lo.Ternary(options.AutoRecapRatesPerDay == 0, 4, options.AutoRecapRatesPerDay),
		options.PinAutoRecapMessage,
	)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾功能，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	return c.NewEditMessageTextAndReplyMarkup(messageID,
		lo.Ternary(
			actionData.Mode == tgchat.AutoRecapSendModePublicly,
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾模式已切换为<b>"+tgchat.AutoRecapSendModePublicly.String()+"</b>，将会自动收集群组中的聊天记录并定时发送聊天回顾快报。",
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾模式已切换为<b>"+tgchat.AutoRecapSendModeOnlyPrivateSubscriptions.String()+"</b>，将会自动收集群组中的聊天记录并定时发送聊天回顾快报给通过 /subscribe_recap 命令订阅了本群组聊天回顾用户。",
		),
		markup,
	).WithParseModeHTML(), nil
}

func (h *CallbackQueryHandler) handleCallbackQueryComplete(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	generalErrorMessage := configureRecapGeneralInstructionMessage + "\n\n" + "应用聊天记录回顾功能的配置时出现了问题，请稍后再试！"

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID
	messageID := msg.MessageID

	var actionData recap.ConfigureRecapCompleteActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	shouldSkip := shouldSkipCallbackQueryHandlingByCheckingActionData(c, actionData, chatID, fromID)
	if shouldSkip {
		return nil, nil
	}

	// check actor is admin or creator, bot is admin
	is, err := c.IsUserMemberStatus(fromID, []telegram.MemberStatus{telegram.MemberStatusCreator, telegram.MemberStatusAdministrator})
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾功能，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}
	if !is && !c.Bot.IsGroupAnonymousBot(c.Update.CallbackQuery.From) {
		return nil, nil
	}

	_ = c.Bot.MayRequest(tgbotapi.NewDeleteMessage(chatID, messageID))
	if c.Update.CallbackQuery.Message.ReplyToMessage != nil {
		_ = c.Bot.MayRequest(tgbotapi.NewDeleteMessage(chatID, c.Update.CallbackQuery.Message.ReplyToMessage.MessageID))
	}

	return nil, nil
}

func (h *CallbackQueryHandler) handleCallbackQueryUnsubscribe(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID

	var actionData recap.UnsubscribeRecapActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("取消订阅时出现了问题，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}
	if actionData.FromID != fromID {
		h.logger.Warn("action skipped, callback query is not from the same actor or the same chat", zap.Int64("from_id", fromID), zap.Int64("chat_id", chatID))
		return nil, nil
	}

	err = h.tgchats.UnsubscribeToAutoRecaps(actionData.ChatID, fromID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("取消订阅时出现了问题，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	var inlineKeyboardMarkup tgbotapi.InlineKeyboardMarkup
	if msg.ReplyMarkup == nil {
		inlineKeyboardMarkup = tgbotapi.NewInlineKeyboardMarkup()
	} else {
		inlineKeyboardMarkup = *msg.ReplyMarkup
		inlineKeyboardMarkup = c.Bot.RemoveInlineKeyboardButtonFromInlineKeyboardMarkupThatMatchesDataWith(inlineKeyboardMarkup, c.Update.CallbackQuery.Data)
	}

	c.Bot.MayRequest(tgbotapi.NewEditMessageReplyMarkup(chatID, msg.MessageID, inlineKeyboardMarkup))

	return c.NewMessage(fmt.Sprintf("已成功取消订阅群组 <b>%s</b> 的定时聊天回顾。", tgbot.EscapeHTMLSymbols(actionData.ChatTitle))).WithParseModeHTML(), nil
}

func (h *CallbackQueryHandler) handleAutoRecapRatesPerDaySelect(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	generalErrorMessage := configureRecapGeneralInstructionMessage + "\n\n" + "应用聊天记录回顾功能的配置时出现了问题，请稍后再试！"

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID
	chatTitle := msg.Chat.Title
	messageID := msg.MessageID

	var actionData recap.ConfigureAutoRecapRatesPerDayActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	shouldSkip := shouldSkipCallbackQueryHandlingByCheckingActionData(c, actionData, chatID, fromID)
	if shouldSkip {
		return nil, nil
	}

	// check whether the actor is admin or creator, and whether the bot is admin
	err = checkAssignMode(c, chatID, c.Update.CallbackQuery.From)
	if err != nil {
		if errors.Is(err, errAdministratorPermissionRequired) {
			h.logger.Debug("action skipped, callback query is not from an admin or creator",
				zap.Int64("from_id", fromID),
				zap.Int64("chat_id", chatID),
				zap.String("permission_check_result", err.Error()),
			)

			return nil, nil
		}
		if errors.Is(err, errOperationCanNotBeDone) || errors.Is(err, errCreatorPermissionRequired) {
			return nil, tgbot.
				NewMessageError(configureRecapGeneralInstructionMessage + "\n\n" + err.Error()).
				WithEdit(msg).
				WithParseModeHTML().
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}

		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	err = h.tgchats.SetAutoRecapRatesPerDay(chatID, actionData.Rates)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "每天自动创建回顾频率次数设定失败，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	options, err := h.tgchats.FindOneRecapsOption(chatID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "每天自动创建回顾频率次数设定失败，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	err = h.tgchats.QueueOneSendChatHistoriesRecapTaskForChatID(chatID, options)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "每天自动创建回顾频率次数设定失败，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	has, err := h.tgchats.HasChatHistoriesRecapEnabledForGroups(chatID, chatTitle)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "每天自动创建回顾频率次数设定失败，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	markup, err := newRecapInlineKeyboardMarkup(
		c,
		chatID,
		fromID,
		has,
		tgchat.AutoRecapSendMode(options.AutoRecapSendMode),
		actionData.Rates,
		options.PinAutoRecapMessage,
	)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(configureRecapGeneralInstructionMessage + "\n\n" + "每天自动创建回顾频率次数设定失败，请稍后再试！").
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	return c.NewEditMessageTextAndReplyMarkup(messageID,
		configureRecapGeneralInstructionMessage+"\n\n"+"每天自动创建聊天回顾的频率次数已设定为 <b>"+strconv.FormatInt(int64(actionData.Rates), 10)+"</b>，将会自动收集群组中的聊天记录并在 "+strings.Join(lo.Map(tgchats.MapScheduleHours[actionData.Rates], func(item int64, _ int) string {
			return fmt.Sprintf("<b>%02d:00</b>", item)
		}), "，")+" 发送聊天回顾快报。",
		markup,
	).WithParseModeHTML(), nil
}

func (h *CallbackQueryHandler) handleCallbackQueryPin(c *tgbot.Context) (tgbot.Response, error) {
	msg := c.Update.CallbackQuery.Message

	generalErrorMessage := configureRecapGeneralInstructionMessage + "\n\n" + "应用聊天记录回顾消息置顶功能的配置时出现了问题，请稍后再试！"

	fromID := c.Update.CallbackQuery.From.ID
	chatID := msg.Chat.ID
	messageID := msg.MessageID

	var actionData recap.ConfigureRecapToggleActionData

	err := c.BindFromCallbackQueryData(&actionData)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	// todo: Is this necessary for pin message?
	//shouldSkip := shouldSkipCallbackQueryHandlingByCheckingActionData(c, actionData, chatID, fromID)
	//if shouldSkip {
	//	return nil, nil
	//}

	// check whether the actor is admin or creator, and whether the bot is admin
	err = checkAssignMode(c, chatID, c.Update.CallbackQuery.From)
	if err != nil {
		if errors.Is(err, errAdministratorPermissionRequired) {
			h.logger.Debug("action skipped, callback query is not from an admin or creator",
				zap.Int64("from_id", fromID),
				zap.Int64("chat_id", chatID),
				zap.String("permission_check_result", err.Error()),
			)

			return nil, nil
		}
		if errors.Is(err, errOperationCanNotBeDone) || errors.Is(err, errCreatorPermissionRequired) {
			return nil, tgbot.
				NewMessageError(configureRecapGeneralInstructionMessage + "\n\n" + err.Error()).
				WithEdit(msg).
				WithParseModeHTML().
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}

		return nil, tgbot.
			NewExceptionError(err).
			WithMessage(generalErrorMessage).
			WithEdit(msg).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	options, err := h.tgchats.FindOneRecapsOption(chatID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾消息置顶功能，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	if actionData.Status {
		errMessage := configureRecapGeneralInstructionMessage + "\n\n" + "聊天记录回顾消息置顶功能开启失败，请稍后再试！"

		err = h.tgchats.EnablePinAutoRecapMessage(chatID)
		if err != nil {
			return nil, tgbot.
				NewExceptionError(err).
				WithMessage(errMessage).
				WithEdit(msg).
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}
	} else {
		errMessage := configureRecapGeneralInstructionMessage + "\n\n" + "聊天记录回顾消息置顶功能关闭失败，请稍后再试！"

		err = h.tgchats.DisablePinAutoRecapMessage(chatID)
		if err != nil {
			return nil, tgbot.
				NewExceptionError(err).
				WithMessage(errMessage).
				WithEdit(msg).
				WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
		}
	}

	markup, err := newRecapInlineKeyboardMarkup(
		c,
		chatID,
		fromID,
		actionData.Status,
		tgchat.AutoRecapSendMode(options.AutoRecapSendMode),
		lo.Ternary(options.AutoRecapRatesPerDay == 0, 4, options.AutoRecapRatesPerDay),
		actionData.Status,
	)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("暂时无法配置聊天记录回顾消息置顶功能，请稍后再试！").
			WithEdit(c.Update.Message).
			WithReplyMarkup(tgbotapi.NewInlineKeyboardMarkup(msg.ReplyMarkup.InlineKeyboard...))
	}

	return c.NewEditMessageTextAndReplyMarkup(messageID,
		lo.Ternary(
			actionData.Status,
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾消息置顶功能已开启，开启后将会自动收集群组中的聊天记录并定时发送聊天回顾快报。",
			configureRecapGeneralInstructionMessage+"\n\n"+"聊天记录回顾消息置顶功能已关闭，关闭后将不会再收集群组中的聊天记录了。",
		),
		markup,
	), nil
}

// handleCallbackQuerySelectHours handles the callback query for selecting hours and generates a Telegraph page with the summary
func (h *CallbackQueryHandler) handleCallbackQuerySelectHours(c *tgbot.Context) (tgbot.Response, error) {
	messageID := c.Update.CallbackQuery.Message.MessageID
	replyToMessage := c.Update.CallbackQuery.Message.ReplyToMessage

	var data recap.SelectHourCallbackQueryData
	err := c.BindFromCallbackQueryData(&data)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}
	if !lo.Contains(RecapSelectHourAvailable, data.Hour) {
		return nil, tgbot.NewExceptionError(fmt.Errorf("invalid hour: %d")).WithReply(replyToMessage)
	}

	var inProgressText string
	switch data.RecapMode {
	case tgchat.AutoRecapSendModePublicly:
		inProgressText = fmt.Sprintf("正在為過去 %d 個小時的聊天記錄生成回顧，請稍等...", data.Hour)
	case tgchat.AutoRecapSendModeOnlyPrivateSubscriptions:
		inProgressText = fmt.Sprintf("正在為 <b>%s</b> 過去 %d 個小時的聊天記錄生成回顧，請稍等...", tgbot.EscapeHTMLSymbols(data.ChatTitle), data.Hour)
	default:
		inProgressText = fmt.Sprintf("正在為過去 %d 個小時的聊天記錄生成回顧，請稍等...", data.Hour)
	}

	editConfig := tgbotapi.NewEditMessageTextAndMarkup(
		c.Update.CallbackQuery.Message.Chat.ID,
		messageID,
		inProgressText,
		tgbotapi.NewInlineKeyboardMarkup([]tgbotapi.InlineKeyboardButton{}),
	)
	editConfig.ParseMode = tgbotapi.ModeHTML

	_, err = c.Bot.Request(editConfig)
	if err != nil {
		h.logger.Error("failed to edit message", zap.Error(err))
	}

	// Convert hour (int64) to time.Duration
	hourDuration := time.Duration(data.Hour) * time.Hour
	histories, err := h.chatHistories.FindChatHistoriesByTimeBefore(data.ChatID, hourDuration)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}
	if len(histories) <= 5 {
		var errMessage string
		switch data.RecapMode {
		case tgchat.AutoRecapSendModePublicly:
			errMessage = fmt.Sprintf("最近 %d 小時內暫時沒有超過 5 條的聊天記錄可以生成聊天回顧哦，要再多聊點之後再試試嗎？", data.Hour)
		case tgchat.AutoRecapSendModeOnlyPrivateSubscriptions:
			errMessage = fmt.Sprintf("最近 %d 小時內暫時沒有超過 5 條的聊天記錄可以生成聊天回顧哦，要再等待群內成員多聊點之後再試試嗎？", data.Hour)
		default:
			errMessage = fmt.Sprintf("最近 %d 小時內暫時沒有超過 5 條的聊天記錄可以生成聊天回顧哦，要再多聊點之後再試試嗎？", data.Hour)
		}
		return nil, tgbot.NewMessageError(errMessage).WithReply(replyToMessage)
	}

	chatType := telegram.ChatType(c.Update.CallbackQuery.Message.Chat.Type)

	logID, summarizations, err := h.chatHistories.SummarizeChatHistories(data.ChatID, chatType, histories)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}

	summarizations = lo.Filter(summarizations, func(item string, _ int) bool { return item != "" })
	if len(summarizations) == 0 {
		return nil, tgbot.NewMessageError("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}

	// Find counts for voting buttons BEFORE sending the message
	counts, err := h.chatHistories.FindFeedbackRecapsReactionCountsForChatIDAndLogID(data.ChatID, logID)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}

	inlineKeyboardMarkup, err := h.chatHistories.NewVoteRecapInlineKeyboardMarkup(c.Bot, data.ChatID, logID, counts.UpVotes, counts.DownVotes, counts.Lmao)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}

	// 修改標題格式
	// 原始格式: "{群組名} 用戶 {用戶名} 於 {時間} 總結範圍 {小時} 個小時"
	// 新格式: "【群組 {群組名}】用戶 {用戶名} 發起 {小時} 個小時總結"
	actorName := tgbot.EscapeHTMLSymbols(c.Update.CallbackQuery.From.FirstName)
	if c.Update.CallbackQuery.From.LastName != "" {
		actorName += " " + tgbot.EscapeHTMLSymbols(c.Update.CallbackQuery.From.LastName)
	}
	groupName := tgbot.EscapeHTMLSymbols(data.ChatTitle)
	if groupName == "" {
		groupName = "當前聊天"
	}
	// 新格式的頁面標題
	pageTitle := fmt.Sprintf("【群組 %s】用戶 %s 發起 %d 個小時總結", groupName, actorName, data.Hour)

	// 格式化 HTML 內容（不使用 <article> 以免產生空 tag）
	var htmlContent strings.Builder

	htmlContent.WriteString("<hr>")

	// 添加摘要內容
	for _, summaryBlock := range summarizations {
		lines := strings.Split(summaryBlock, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if strings.HasPrefix(line, "## ") {
				titleContent := strings.TrimPrefix(line, "## ")
				// The content from template may already contain HTML tags like <a>, so no escaping here.
				htmlContent.WriteString("<h2>" + titleContent + "</h2>")
			} else {
				// For other lines, just wrap them in <p> tags.
				// The content inside (participants, discussion points) is already escaped or formatted by the template.
				htmlContent.WriteString("<p>" + line + "</p>")
			}
		}
	}

	// 新增頁腳
	htmlContent.WriteString("<hr>")
	htmlContent.WriteString(fmt.Sprintf("<p><em>🤖️ 由 %s 生成 分段總結</em></p>", h.chatHistories.GetOpenAIModelName()))

	// Create Telegraph page with retry mechanism, support multiple pages if needed
	var telegraphURL string
	var telegraphURLs []string

	// 檢測是否需要分頁
	if len(htmlContent.String()) > 60*1024 { // 使用60KB作為安全邊界
		// 使用多頁方法
		telegraphURLs, err = h.telegraph.CreatePageSeries(context.Background(), pageTitle, htmlContent.String())
		if err != nil {
			h.logger.Error("failed to create telegraph page series for manual recap",
				zap.Error(err),
				zap.Int64("chat_id", data.ChatID),
				zap.String("title", pageTitle),
			)
			return nil, tgbot.NewExceptionError(err).WithMessage("生成 Telegraph 文章失敗，請稍後再試或聯繫管理員。").WithReply(replyToMessage)
		}

		// 使用第一個URL作為主URL
		if len(telegraphURLs) > 0 {
			telegraphURL = telegraphURLs[0]
		} else {
			return nil, tgbot.NewExceptionError(fmt.Errorf("empty telegraph URLs")).WithMessage("生成 Telegraph 文章失敗，請稍後再試或聯繫管理員。").WithReply(replyToMessage)
		}
	} else {
		// 使用單頁方法
		telegraphURL, err = h.telegraph.CreatePage(context.Background(), pageTitle, htmlContent.String())
		if err != nil {
			h.logger.Error("failed to create telegraph page for manual recap",
				zap.Error(err),
				zap.Int64("chat_id", data.ChatID),
				zap.String("title", pageTitle),
			)
			return nil, tgbot.NewExceptionError(err).WithMessage("生成 Telegraph 文章失敗，請稍後再試或聯繫管理員。").WithReply(replyToMessage)
		}
		telegraphURLs = []string{telegraphURL}
	}

	// 1. 嘗試使用 OpenAI 生成銳評式濃縮摘要
	condensedSummary, genErr := h.chatHistories.GenSarcasticCondensed(data.ChatID, histories)
	if genErr != nil || condensedSummary == "" {
		// 2. Fallback：採用既有簡單算法
		condensedSummary = "最近討論的主題包括: "
		if len(summarizations) > 0 {
			allText := strings.Join(summarizations, " ")

			// 提取關鍵詞
			words := strings.Fields(allText)
			wordCount := make(map[string]int)
			for _, word := range words {
				if len(word) > 1 {
					wordCount[word]++
				}
			}
			keyWords := []string{}
			for word, count := range wordCount {
				if count > 2 && len(word) > 1 && !strings.Contains("的了是在和與於及", word) {
					keyWords = append(keyWords, word)
					if len(keyWords) >= 3 {
						break
					}
				}
			}

			if len(keyWords) > 0 {
				condensedSummary = fmt.Sprintf("群組在過去 %d 小時內主要討論了 %s 等主題。", data.Hour, strings.Join(keyWords, "、"))
			} else {
				firstSummary := summarizations[0]
				if len(firstSummary) > 50 {
					condensedSummary = firstSummary[:50] + "..."
				} else {
					condensedSummary = firstSummary
				}
			}
		}
	} else {
		// 確保摘要文本乾淨整潔
		condensedSummary = strings.TrimSpace(condensedSummary)
	}

	// Send the link to Telegram
	modelName := h.chatHistories.GetOpenAIModelName()
	sarcasticModelName := h.chatHistories.GetSarcasticCondensedModelName()

	// 添加多頁信息（如果有多頁）
	multiPageInfo := ""
	if len(telegraphURLs) > 1 {
		multiPageInfo = "\n\n<b>注意：</b>由於內容較長，已分為 " + strconv.Itoa(len(telegraphURLs)) + " 個頁面："
		for i, url := range telegraphURLs {
			multiPageInfo += fmt.Sprintf("\n- <a href=\"%s\">第 %d 部分</a>", url, i+1)
		}
	}

	content := fmt.Sprintf("📝 <b>聊天回顧已發布到 Telegraph</b>: <a href=\"%s\">%s</a>%s\n\n<b>濃縮總結：</b>\n%s\n\n%s#recap\n<i>🤖️ 由 %s 生成 濃縮總結</i>\n<i>🤖️ 由 %s 生成 分段總結</i>",
		telegraphURL,
		tgbot.EscapeHTMLSymbols(pageTitle),
		multiPageInfo,
		tgbot.EscapeHTMLSymbols(condensedSummary),
		lo.Ternary(chatType == telegram.ChatTypeGroup, "<b>Tips: </b>由於群組不是超級群組（supergroup），因此消息鏈接引用暫時被禁用了，如果希望使用該功能，請通過短時間內將群組開放為公共群組並還原回私有群組，或通過其他操作將本群組升級為超級群組後，該功能方可恢復正常運作。\n\n", ""),
		sarcasticModelName,
		modelName,
	)

	msg := tgbotapi.NewMessage(c.Update.CallbackQuery.Message.Chat.ID, content)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = inlineKeyboardMarkup // Attach voting buttons

	if replyToMessage != nil {
		msg.ReplyToMessageID = replyToMessage.MessageID
	}

	h.logger.Info("sending chat histories recap link for chat",
		zap.Int64("chat_id", c.Update.CallbackQuery.Message.Chat.ID),
		zap.String("telegraph_url", telegraphURL),
	)

	_, sendErr := c.Bot.Send(msg)
	if sendErr != nil {
		h.logger.Error("failed to send recap link", zap.Error(sendErr), zap.Int64("chat_id", c.Update.CallbackQuery.Message.Chat.ID))
		// Don't return error here, try to delete the original message anyway
	}

	// Delete the "Generating..." message
	deleteConfig := tgbotapi.NewDeleteMessage(c.Update.CallbackQuery.Message.Chat.ID, messageID)
	_, delErr := c.Bot.Request(deleteConfig)
	if delErr != nil {
		h.logger.Error("failed to delete waiting message", zap.Error(delErr))
	}

	return nil, nil // Indicate success
}
