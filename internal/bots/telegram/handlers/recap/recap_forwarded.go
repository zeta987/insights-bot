package recap

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/internal/services/recapdelivery"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/types/redis"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

func (h *CommandHandler) handleRecapForwardedStartCommand(c *tgbot.Context) (tgbot.Response, error) {
	if !lo.Contains([]telegram.ChatType{telegram.ChatTypePrivate}, telegram.ChatType(c.Update.Message.Chat.Type)) {
		return nil, tgbot.NewMessageError("该命令当前只能在私聊中使用哦！")
	}

	has, err := h.chathistories.HasOngoingRecapForwardedFromPrivateMessages(c.Update.Message.From.ID)
	if err != nil {
		return nil, err
	}

	if has {
		delCmd := h.redis.B().
			Del().
			Key(redis.RecapReplayFromPrivateMessageBatch1.Format(c.Update.Message.From.ID)).
			Build()

		err := h.redis.Do(context.Background(), delCmd).Error()
		if err != nil {
			return nil, err
		}
	}

	err = h.chathistories.EnabledRecapForwardedFromPrivateMessages(c.Update.Message.From.ID)
	if err != nil {
		return nil, err
	}

	return c.NewMessageReplyTo("没问题，请将你需要总结的消息在 2 小时内发给我吧。发送完毕后可以通过发送 /recap_forwarded 给我来开始总结哦！", c.Update.Message.MessageID), nil
}

func (h *CommandHandler) handleRecapForwardedStartShouleCancel(c *tgbot.Context) (bool, error) {
	has, err := h.chathistories.HasOngoingRecapForwardedFromPrivateMessages(c.Update.Message.From.ID)
	if err != nil {
		return false, err
	}

	return has, nil
}

func (h *CommandHandler) handleRecapForwardedStartCancelCommand(c *tgbot.Context) (tgbot.Response, error) {
	err := h.chathistories.DisableRecapForwardedFromPrivateMessages(c.Update.Message.From.ID)
	if err != nil {
		return nil, err
	}

	return c.NewMessageReplyTo("好的，已经帮你把消息清理掉了，如果需要总结转发的消息，可以再次发送 /recap_forwarded_start 开始操作。", c.Update.Message.MessageID), nil
}

func (h *CommandHandler) handleRecapForwardedCommand(c *tgbot.Context) (tgbot.Response, error) {
	if !lo.Contains([]telegram.ChatType{telegram.ChatTypePrivate}, telegram.ChatType(c.Update.Message.Chat.Type)) {
		return nil, tgbot.NewMessageError("该命令当前只能在私聊中使用哦！")
	}

	waitingMessage, err := c.Bot.Send(tgbotapi.NewMessage(
		c.Update.Message.From.ID,
		"正在为已经接收到的聊天记录生成回顾，请稍等...",
	))
	if err != nil {
		h.logger.Error("failed to send waiting message", zap.Error(err))
	}

	histories, err := h.chathistories.FindPrivateForwardedChatHistories(c.Update.Message.From.ID)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天记录回顾生成失败，请稍后再试！").WithReply(c.Update.Message)
	}
	if len(histories) < 5 {
		return nil, tgbot.NewMessageError("目前收到的聊天记录不足 5 条哦，要再多发送给我一些之后之后再试试吗？").WithReply(c.Update.Message)
	}

	summarizations, recapTrace, err := h.chathistories.SummarizePrivateForwardedChatHistories(c.Update.Message.From.ID, histories)
	if err != nil {
		return nil, tgbot.NewExceptionError(err).WithMessage("聊天记录回顾生成失败，请稍后再试！").WithReply(c.Update.Message)
	}

	summarizations = lo.Filter(summarizations, func(item string, _ int) bool { return item != "" })
	if len(summarizations) == 0 {
		return nil, tgbot.NewMessageError("聊天记录回顾生成失败，请稍后再试！").WithReply(c.Update.Message)
	}

	var sourceHistory strings.Builder
	for _, history := range histories {
		actor := history.ActorDisplayName
		if actor == "" {
			actor = history.ActorUsername
		}
		_, _ = fmt.Fprintf(&sourceHistory, "%s: %s\n", actor, history.Text)
	}

	condensedSummary, condensedTrace, condensedErr := h.chathistories.GenSarcasticCondensedFromText(c.Update.Message.From.ID, sourceHistory.String())
	condensedSummary = strings.TrimSpace(condensedSummary)
	if condensedErr != nil || condensedSummary == "" {
		condensedSummary = chathistories.FallbackCondensedSummary(summarizations, "轉發訊息的聊天回顧")
	}

	initiatorName := strings.TrimSpace(c.Update.Message.From.FirstName + " " + c.Update.Message.From.LastName)
	if initiatorName == "" {
		initiatorName = c.Update.Message.From.UserName
	}
	visibleSummary := chathistories.BuildRichRecapSummary(chathistories.RichRecapSummaryConfig{
		Title:            "轉發訊息",
		InitiatorName:    initiatorName,
		InitiatorUserID:  c.Update.Message.From.ID,
		CondensedSummary: condensedSummary,
		CondensedTrace:   condensedTrace,
		RecapTrace:       recapTrace,
	})
	parts := chathistories.ComposeRichRecapMessages(visibleSummary, summarizations)

	sentMessages, sendErr := recapdelivery.SendRichRecapParts(c.Bot, recapdelivery.Config{
		ChatID:                   c.Update.Message.Chat.ID,
		Parts:                    parts,
		ReplyToMessageID:         c.Update.Message.MessageID,
		AllowSendingWithoutReply: true,
	})
	if sendErr != nil {
		h.logger.Error("failed to send forwarded histories rich recap",
			zap.Error(sendErr),
			zap.Int64("chat_id", c.Update.Message.Chat.ID),
			zap.Int("sent_message_count", len(sentMessages)),
		)
	} else {
		h.logger.Info("sent forwarded histories rich recap",
			zap.Int64("chat_id", c.Update.Message.Chat.ID),
			zap.Int("message_count", len(sentMessages)),
		)
	}

	// Delete the waiting placeholder even when sending failed so it does not
	// linger in the private chat.
	if waitingMessage.MessageID != 0 {
		deleteConfig := tgbotapi.NewDeleteMessage(c.Update.Message.From.ID, waitingMessage.MessageID)
		if _, delErr := c.Bot.Request(deleteConfig); delErr != nil {
			h.logger.Error("failed to delete waiting message", zap.Error(delErr))
		}
	}

	if sendErr != nil {
		return nil, tgbot.NewExceptionError(sendErr).WithMessage("聊天记录回顾发送失败，请稍后再试！").WithReply(c.Update.Message)
	}

	completion := tgbotapi.NewMessage(c.Update.Message.Chat.ID, "总结完成，如果你觉得不满意，可以再次发送 /recap_forwarded 重新生成哦！如果觉得满意，并且希望进行其他的总结操作，可以在开始前发送 /cancel 来清空当前已经接收到的消息，如果不进行操作，缓存的消息将会在 2 小时后被自动清理。")
	completion.ReplyToMessageID = c.Update.Message.MessageID
	if _, sendErr := c.Bot.Send(completion); sendErr != nil {
		h.logger.Error("failed to send forwarded recap completion message", zap.Error(sendErr))
	}

	return nil, nil
}
