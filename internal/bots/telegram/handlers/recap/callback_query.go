package recap

import (
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
	"github.com/nekomeowww/insights-bot/internal/services/recapdelivery"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/logger"
	"github.com/nekomeowww/insights-bot/pkg/types/bot/handlers/recap"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/nekomeowww/insights-bot/pkg/types/tgchat"
)

type NewCallbackQueryHandlerParams struct {
	fx.In

	Logger        *logger.Logger
	ChatHistories *chathistories.Model
	TgChats       *tgchats.Model
}

type CallbackQueryHandler struct {
	logger        *logger.Logger
	chatHistories *chathistories.Model
	tgchats       *tgchats.Model
}

func NewCallbackQueryHandler() func(NewCallbackQueryHandlerParams) *CallbackQueryHandler {
	return func(param NewCallbackQueryHandlerParams) *CallbackQueryHandler {
		return &CallbackQueryHandler{
			logger:        param.Logger,
			chatHistories: param.ChatHistories,
			tgchats:       param.TgChats,
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

// handleCallbackQuerySelectHours handles the callback query and sends a Rich Message recap.
func (h *CallbackQueryHandler) handleCallbackQuerySelectHours(c *tgbot.Context) (tgbot.Response, error) {
	messageID := c.Update.CallbackQuery.Message.MessageID
	replyToMessage := c.Update.CallbackQuery.Message.ReplyToMessage

	var data recap.SelectHourCallbackQueryData
	err := c.BindFromCallbackQueryData(&data)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}
	if !lo.Contains(RecapSelectHourAvailable, data.Hour) {
		return nil, tgbot.NewExceptionError(fmt.Errorf("invalid hour: %d", data.Hour)).WithReply(replyToMessage)
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

	chatType := telegram.ChatType(histories[len(histories)-1].ChatType)

	logID, summarizations, recapTrace, err := h.chatHistories.SummarizeChatHistories(data.ChatID, chatType, histories)
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

	actorName := strings.TrimSpace(c.Update.CallbackQuery.From.FirstName + " " + c.Update.CallbackQuery.From.LastName)
	if actorName == "" {
		actorName = c.Update.CallbackQuery.From.UserName
	}
	groupName := strings.TrimSpace(data.ChatTitle)
	if groupName == "" {
		groupName = "當前聊天"
	}
	condensedSummary, condensedTrace, genErr := h.chatHistories.GenSarcasticCondensed(data.ChatID, histories)
	condensedSummary = strings.TrimSpace(condensedSummary)
	if genErr != nil || condensedSummary == "" {
		condensedSummary = chathistories.FallbackCondensedSummary(summarizations, fmt.Sprintf("過去 %d 小時的群組聊天回顧", data.Hour))
	}

	visibleSummary := chathistories.BuildRichRecapSummary(chathistories.RichRecapSummaryConfig{
		Title:              groupName,
		Hours:              int(data.Hour),
		InitiatorName:      actorName,
		InitiatorUserID:    c.Update.CallbackQuery.From.ID,
		CondensedSummary:   condensedSummary,
		GeneralGroupNotice: chatType == telegram.ChatTypeGroup,
		CondensedTrace:     condensedTrace,
		RecapTrace:         recapTrace,
	})

	parts := chathistories.ComposeRichRecapMessages(visibleSummary, summarizations)
	if len(parts) == 0 {
		return nil, tgbot.NewMessageError("聊天記錄回顧生成失敗，請稍後再試！").WithReply(replyToMessage)
	}

	replyToMessageID := 0
	if replyToMessage != nil {
		replyToMessageID = replyToMessage.MessageID
	}

	sentMessages, sendErr := recapdelivery.SendRichRecapParts(c.Bot, recapdelivery.Config{
		ChatID:                   c.Update.CallbackQuery.Message.Chat.ID,
		Parts:                    parts,
		ReplyToMessageID:         replyToMessageID,
		ReplyMarkup:              &inlineKeyboardMarkup,
		AllowSendingWithoutReply: true,
	})
	if sendErr != nil {
		h.logger.Error("failed to send rich chat recap",
			zap.Error(sendErr),
			zap.Int64("chat_id", c.Update.CallbackQuery.Message.Chat.ID),
			zap.Int("sent_message_count", len(sentMessages)),
		)
		return nil, tgbot.NewExceptionError(sendErr).WithMessage("聊天記錄回顧發送失敗，請稍後再試！").WithReply(replyToMessage)
	}
	h.logger.Info("sent rich chat recap",
		zap.Int64("chat_id", c.Update.CallbackQuery.Message.Chat.ID),
		zap.Int("message_count", len(sentMessages)),
	)

	// Delete the "Generating..." message
	deleteConfig := tgbotapi.NewDeleteMessage(c.Update.CallbackQuery.Message.Chat.ID, messageID)
	_, delErr := c.Bot.Request(deleteConfig)
	if delErr != nil {
		h.logger.Error("failed to delete waiting message", zap.Error(delErr))
	}

	return nil, nil // Indicate success
}
