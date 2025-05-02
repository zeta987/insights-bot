package recap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nekomeowww/fo"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/types/redis"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/nekomeowww/insights-bot/pkg/types/tgchat"
	"github.com/nekomeowww/xo"
	"github.com/redis/rueidis"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

type privateSubscriptionStartCommandContext struct {
	ChatID    int64  `json:"chat_id"`
	ChatTitle string `json:"chat_title"`
}

func (h *CommandHandler) setRecapForPrivateSubscriptionModeStartCommandContext(chatID int64, chatTitle string) (string, error) {
	hashSource := fmt.Sprintf("recap/private_subscription_mode/start_command_context/%d", chatID)
	hashKey := fmt.Sprintf("%x", sha256.Sum256([]byte(hashSource)))[0:8]

	setCmd := h.redis.Client.B().
		Set().
		Key(redis.RecapPrivateSubscriptionStartCommandContext1.Format(hashKey)).
		Value(string(lo.Must(json.Marshal(privateSubscriptionStartCommandContext{
			ChatID:    chatID,
			ChatTitle: chatTitle,
		})))).
		ExSeconds(24 * 60 * 60).
		Build()

	err := h.redis.Do(context.Background(), setCmd).Error()
	if err != nil {
		return hashKey, err
	}

	return hashKey, nil
}

func (h *CommandHandler) getRecapForPrivateSubscriptionModeStartCommandContext(hash string) (*privateSubscriptionStartCommandContext, error) {
	getCmd := h.redis.Client.B().
		Get().
		Key(redis.RecapPrivateSubscriptionStartCommandContext1.Format(hash)).
		Build()

	str, err := h.redis.Do(context.Background(), getCmd).ToString()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}

		return nil, err
	}
	if str == "" {
		return nil, nil
	}

	var data privateSubscriptionStartCommandContext

	err = json.Unmarshal([]byte(str), &data)
	if err != nil {
		return nil, err
	}

	return &data, nil
}

func (h *CommandHandler) setSubscribeStartCommandContext(chatID int64, chatTitle string) (string, error) {
	hashSource := fmt.Sprintf("recap/subscribe_recap/start_command_context/%d", chatID)
	hashKey := fmt.Sprintf("%x", sha256.Sum256([]byte(hashSource)))[0:8]

	setCmd := h.redis.Client.B().
		Set().
		Key(redis.RecapSubscribeRecapStartCommandContext1.Format(hashKey)).
		Value(string(lo.Must(json.Marshal(privateSubscriptionStartCommandContext{
			ChatID:    chatID,
			ChatTitle: chatTitle,
		})))).
		ExSeconds(24 * 60 * 60).
		Build()

	err := h.redis.Do(context.Background(), setCmd).Error()
	if err != nil {
		return hashKey, err
	}

	return hashKey, nil
}

func (h *CommandHandler) getSubscribeStartCommandContext(hash string) (*privateSubscriptionStartCommandContext, error) {
	getCmd := h.redis.Client.B().
		Get().
		Key(redis.RecapSubscribeRecapStartCommandContext1.Format(hash)).
		Build()

	str, err := h.redis.Do(context.Background(), getCmd).ToString()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}

		return nil, err
	}
	if str == "" {
		return nil, nil
	}

	var data privateSubscriptionStartCommandContext

	err = json.Unmarshal([]byte(str), &data)
	if err != nil {
		return nil, err
	}

	return &data, nil
}

func newRecapCommandWhenUserNeverStartedChat(bot *tgbot.Bot, hashKey string) string {
	return fmt.Sprintf(""+
		"抱歉，在给您发送引导您创建聊天回顾的消息时出现了问题，这似乎是因为您<b>从未</b>和本 Bot（@%s） "+
		"<b>发起过对话</b>导致的。\n\n"+
		"由于当前群组的聊天回顾功能已经被<b>群组创建者</b>设定为<b>私聊订阅模式</b>，Bot 需要通过私聊的方"+
		"式向您发送引导您创建聊天回顾的消息，届时，您需要完成以下任一一个操作后方可继续创建聊天回顾：\n"+
		"1. <b>点击链接</b> https://t.me/%s?start=%s 与 Bot 开始对话就能继续原先的 /recap 命令操作"+
		"；\n"+
		"2. 点击 Bot 头像并且开始对话，然后在群组内重新发送 /recap 命令来创建聊天回顾。"+
		"", bot.Self.UserName, bot.Self.UserName, hashKey)
}

func newSubscribeRecapCommandWhenUserNeverStartedChat(bot *tgbot.Bot, hashKey string) string {
	return fmt.Sprintf(""+
		"抱歉，在为您订阅本群组定时聊天回顾时出现了问题，这似乎是因为您<b>从未</b>和本 Bot（@%s） <b>发起"+
		"过对话</b>导致的。\n\n"+
		"订阅群组的聊天回顾需要 Bot 需要有权限通过私聊的方式向您定期发送聊天回顾，届时，您需要完成以下任一一"+
		"个操作后方可完成订阅：\n"+
		"1. <b>点击链接</b> https://t.me/%s?start=%s 与 Bot 开始对话；\n"+
		"2. 点击 Bot 头像并且开始对话，然后在群组内重新发送 /subscribe_recap 命令来订阅本群组的定时聊"+
		"天回顾。"+
		"", bot.Self.UserName, bot.Self.UserName, hashKey)
}

func newRecapCommandWhenUserBlockedMessage(bot *tgbot.Bot, hashKey string) string {
	return fmt.Sprintf(""+
		"抱歉，在给您发送引导您创建聊天回顾的消息时出现了问题，这似乎是因为您已将本 Bot（@%s）<b>停用</b>"+
		"或是添加到了<b>黑名单</b>中导致的。\n\n"+
		"由于当前群组的聊天回顾功能已经被<b>群组创建者</b>设定为<b>私聊订阅模式</b>，Bot 需要通过私聊的方"+
		"式向您发送引导您创建聊天回顾的消息，届时，您需要根据下面的提示进行操作：\n"+
		"1. 将 Bot 从<b>黑名单中移除</b>；\n"+
		"2. <b>点击链接</b> https://t.me/%s?start=%s 继续创建聊天回顾，或是在群组内重新发送 /recap "+
		"命令来创建聊天回顾。"+
		"", bot.Self.UserName, bot.Self.UserName, hashKey)
}

func newSubscribeRecapCommandWhenUserBlockedMessage(bot *tgbot.Bot, hashKey string) string {
	return fmt.Sprintf(""+
		"抱歉，在为您订阅本群组定时聊天回顾时出现了问题，这似乎是因为您已将本 Bot（@%s）<b>停用</b>或是添加"+
		"到了<b>黑名单</b>中导致的。\n\n"+
		"订阅群组的聊天回顾需要 Bot 需要有权限通过私聊的方式向您定期发送聊天回顾，届时，您需要根据下面的提示"+
		"进行操作：\n"+
		"1. 将 Bot 从<b>黑名单中移除</b>；\n"+
		"2. <b>点击链接</b> https://t.me/%s?start=%s 继续订阅本群组的定时聊天回顾操作，或是在群组内重新"+
		"发送 /subscribe_recap 命令来订阅本群组的定时聊天回顾。"+
		"", bot.Self.UserName, bot.Self.UserName, hashKey)
}

func (h *CommandHandler) handleUserNeverStartedChatOrBlockedErr(c *tgbot.Context, chatID int64, _ string, message string) (tgbot.Response, error) {
	msg := tgbotapi.NewMessage(chatID, message)
	msg.ReplyToMessageID = c.Update.Message.MessageID
	msg.ParseMode = tgbotapi.ModeHTML

	sentMsg := c.Bot.MaySend(msg)

	may := fo.NewMay0().Use(func(err error, messageArgs ...any) {
		h.logger.Error("failed to push one delete later message", zap.Error(err))
	})

	may.Invoke(c.Bot.PushOneDeleteLaterMessage(c.Update.Message.From.ID, chatID, c.Update.Message.MessageID))
	may.Invoke(c.Bot.PushOneDeleteLaterMessage(c.Update.Message.From.ID, chatID, sentMsg.MessageID))

	return nil, nil
}

// handleRecapCommand handles the /recap command to generate a summary of recent chat history
func (h *CommandHandler) handleRecapCommand(c *tgbot.Context) (tgbot.Response, error) {
	chatType := telegram.ChatType(c.Update.Message.Chat.Type)
	if !lo.Contains([]telegram.ChatType{telegram.ChatTypeGroup, telegram.ChatTypeSuperGroup}, chatType) {
		return nil, tgbot.NewMessageError("只有在群组和超级群组内才可以创建聊天记录回顾哦！").WithReply(c.Update.Message)
	}

	chatID := c.Update.Message.Chat.ID
	chatTitle := c.Update.Message.Chat.Title

	has, err := h.tgchats.HasChatHistoriesRecapEnabledForGroups(chatID, chatTitle)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}
	if !has {
		return nil, tgbot.
			NewMessageError("聊天记录回顾功能在当前群组尚未启用，需要在群组管理员通过 /configure_recap 命令配置功能启用后才可以创建聊天回顾哦。").
			WithReply(c.Update.Message)
	}

	options, err := h.tgchats.FindOneRecapsOption(chatID)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}
	if options != nil && tgchat.AutoRecapSendMode(options.AutoRecapSendMode) == tgchat.AutoRecapSendModeOnlyPrivateSubscriptions {
		return h.handleRecapCommandForPrivateSubscriptionsMode(c)
	}

	perSeconds := h.tgchats.ManualRecapRatePerSeconds(options)

	_, ttl, ok, err := c.RateLimitForCommand(chatID, "/recap", 1, perSeconds)
	if err != nil {
		h.logger.Error("failed to check rate limit for command /recap", zap.Error(err))
	}
	if !ok {
		return nil, tgbot.
			NewMessageError(fmt.Sprintf("很抱歉，您的操作触发了我们的限制机制，为了保证系统的可用性，本命令每最多 %d 分钟最多使用一次，请您耐心等待 %d 分钟后再试，感谢您的理解和支持。", perSeconds, lo.Ternary(ttl/time.Minute <= 1, 1, ttl/time.Minute))).
			WithReply(c.Update.Message)
	}

	inlineKeyboardButtons, err := newRecapSelectHoursInlineKeyboardButtons(c, chatID, chatTitle, tgchat.AutoRecapSendModePublicly)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}

	return c.
		NewMessageReplyTo("请问您要为过去几个小时内的聊天创建回顾呢？", c.Update.Message.MessageID).
		WithReplyMarkup(inlineKeyboardButtons), nil
}

// handleRecapCommandForPrivateSubscriptionsMode handles the private subscription mode for recap command
func (h *CommandHandler) handleRecapCommandForPrivateSubscriptionsMode(c *tgbot.Context) (tgbot.Response, error) {
	chatID := c.Update.Message.Chat.ID
	fromID := c.Update.Message.From.ID

	if c.Bot.IsGroupAnonymousBot(c.Update.Message.From) {
		return nil, tgbot.
			NewMessageError("匿名管理员无法在设定为私聊回顾模式的群组内请求创建聊天记录回顾哦！如果需要创建聊天记录回顾，必须先将发送角色切换为普通用户然后再试哦。").
			WithReply(c.Update.Message).
			WithDeleteLater(fromID, chatID)
	}

	chatTitle := c.Update.Message.Chat.Title
	msg := tgbotapi.NewMessage(fromID, fmt.Sprintf("您正在请求为群组 <b>%s</b> 创建聊天回顾。\n请问您要为过去几个小时内的聊天创建回顾呢？", tgbot.EscapeHTMLSymbols(c.Update.Message.Chat.Title)))
	msg.ParseMode = tgbotapi.ModeHTML

	inlineKeyboardButtons, err := newRecapSelectHoursInlineKeyboardButtons(c, chatID, chatTitle, tgchat.AutoRecapSendModeOnlyPrivateSubscriptions)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}

	msg.ReplyMarkup = &inlineKeyboardButtons

	_, err = c.Bot.Send(msg)
	if err == nil {
		c.Bot.MayRequest(tgbotapi.NewDeleteMessage(chatID, c.Update.Message.MessageID))

		err = c.Bot.DeleteAllDeleteLaterMessages(fromID)
		if err != nil {
			h.logger.Error("failed to delete all delete later messages", zap.Error(err))
		}

		return nil, nil
	}

	hashKey, hashKeyErr := h.setRecapForPrivateSubscriptionModeStartCommandContext(chatID, chatTitle)
	if hashKeyErr != nil {
		return nil, tgbot.
			NewExceptionError(hashKeyErr).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}

	if c.Bot.IsCannotInitiateChatWithUserErr(err) {
		return h.handleUserNeverStartedChatOrBlockedErr(c, chatID, chatTitle, newRecapCommandWhenUserNeverStartedChat(c.Bot, hashKey))
	} else if c.Bot.IsBotWasBlockedByTheUserErr(err) {
		return h.handleUserNeverStartedChatOrBlockedErr(c, chatID, chatTitle, newRecapCommandWhenUserBlockedMessage(c.Bot, hashKey))
	} else {
		h.logger.Error("failed to send private message to user",
			zap.String("message", xo.SprintJSON(msg)),
			zap.Int64("chat_id", c.Update.Message.From.ID),
			zap.Error(err),
		)
	}

	return nil, nil
}

// handleStartCommandWithPrivateSubscriptionsRecap handles start command with private recap subscription
func (h *CommandHandler) handleStartCommandWithPrivateSubscriptionsRecap(c *tgbot.Context) (tgbot.Response, error) {
	args := strings.Split(c.Update.Message.CommandArguments(), " ")
	if len(args) != 1 {
		return nil, nil
	}

	context, err := h.getRecapForPrivateSubscriptionModeStartCommandContext(args[0])
	if err != nil {
		h.logger.Error("failed to get private subscription recap start command context", zap.Error(err))
		return nil, nil
	}
	if context == nil {
		return nil, nil
	}

	inlineKeyboardButtons, err := newRecapSelectHoursInlineKeyboardButtons(c, context.ChatID, context.ChatTitle, tgchat.AutoRecapSendModeOnlyPrivateSubscriptions)
	if err != nil {
		return nil, tgbot.
			NewExceptionError(err).
			WithMessage("聊天记录回顾生成失败，请稍后再试！").
			WithReply(c.Update.Message)
	}

	err = c.Bot.DeleteAllDeleteLaterMessages(c.Update.Message.From.ID)
	if err != nil {
		h.logger.Error("failed to delete all delete later messages", zap.Error(err))
	}

	return c.
		NewMessageReplyTo(fmt.Sprintf("您正在请求为群组 <b>%s</b> 创建聊天回顾。\n请问您要为过去几个小时内的聊天创建回顾呢？", tgbot.EscapeHTMLSymbols(context.ChatTitle)), c.Update.Message.MessageID).
		WithReplyMarkup(inlineKeyboardButtons).
		WithParseModeHTML(), nil
}

// handleChatMemberLeft handles when a chat member leaves
func (h *CommandHandler) handleChatMemberLeft(c *tgbot.Context) (tgbot.Response, error) {
	if c.Update.Message.LeftChatMember == nil {
		return nil, nil
	}

	chatID := c.Update.Message.Chat.ID
	leftMemberUserID := c.Update.Message.LeftChatMember.ID

	if leftMemberUserID == c.Bot.Self.ID {
		h.logger.Info("bot left the chat, removing subscription records is skipped", zap.Int64("chat_id", chatID))
		// Removed the call to unsubscribe all users as the method doesn't exist
		// err := h.tgchats.UnsubscribeAllToAutoRecaps(chatID)
		// if err != nil {
		// 	h.logger.Error("failed to unsubscribe all to auto recaps",
		// 		zap.Int64("chat_id", chatID),
		// 		zap.Int64("left_user_id", leftMemberUserID),
		// 		zap.Error(err),
		// 	)
		// }

		return nil, nil // Bot left, no further action needed for this handler
	}

	err := h.tgchats.UnsubscribeToAutoRecaps(chatID, leftMemberUserID)
	if err != nil {
		h.logger.Error("failed to unsubscribe to auto recaps",
			zap.Int64("chat_id", chatID),
			zap.Int64("left_user_id", leftMemberUserID),
			zap.Error(err),
		)
	}

	return nil, nil
}
