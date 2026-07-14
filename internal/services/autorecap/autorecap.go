package autorecap

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nekomeowww/fo"
	"github.com/nekomeowww/timecapsule/v2"
	"github.com/samber/lo"
	"github.com/sourcegraph/conc/pool"
	"go.uber.org/fx"
	"go.uber.org/multierr"
	"go.uber.org/ratelimit"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/ent"
	configsPkg "github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/models/chathistories"
	"github.com/nekomeowww/insights-bot/internal/models/tgchats"
	"github.com/nekomeowww/insights-bot/internal/services/recapdelivery"
	"github.com/nekomeowww/insights-bot/pkg/bots/tgbot"
	"github.com/nekomeowww/insights-bot/pkg/logger"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/nekomeowww/insights-bot/pkg/types/tgchat"
	"github.com/nekomeowww/insights-bot/pkg/types/timecapsules"
)

type NewAutoRecapParams struct {
	fx.In

	Lifecycle fx.Lifecycle

	Logger        *logger.Logger
	Bot           *tgbot.BotService
	ChatHistories *chathistories.Model
	TgChats       *tgchats.Model
	Digger        *datastore.AutoRecapTimeCapsuleDigger
	Config        *configsPkg.Config
}

type AutoRecapService struct {
	logger        *logger.Logger
	botService    *tgbot.BotService
	chathistories *chathistories.Model
	tgchats       *tgchats.Model

	digger  *datastore.AutoRecapTimeCapsuleDigger
	started bool
	Config  *configsPkg.Config
}

type targetChat struct {
	chatID              int64
	isPrivateSubscriber bool
}

func NewAutoRecapService() func(NewAutoRecapParams) (*AutoRecapService, error) {
	return func(params NewAutoRecapParams) (*AutoRecapService, error) {
		service := &AutoRecapService{
			logger:        params.Logger,
			botService:    params.Bot,
			chathistories: params.ChatHistories,
			tgchats:       params.TgChats,
			digger:        params.Digger,
			Config:        params.Config,
		}

		service.digger.SetHandler(service.sendChatHistoriesRecapTimeCapsuleHandler)
		service.tgchats.QueueSendChatHistoriesRecapTask()

		// DEBUG: The following is a test feature for auto-recap, please manually fill in the chatID in production
		if params.Config.AutoRecapTestEnabled && params.Config.AutoRecapTestChatID != 0 {
			go func() {
				chatID := params.Config.AutoRecapTestChatID
				service.logger.Info("executing auto recap for specific group immediately", zap.Int64("chat_id", chatID))

				capsule := &timecapsule.TimeCapsule[timecapsules.AutoRecapCapsule]{
					Payload: timecapsules.AutoRecapCapsule{ChatID: chatID},
				}

				service.sendChatHistoriesRecapTimeCapsuleHandler(nil, capsule)
			}()
		}

		return service, nil
	}
}

func (s *AutoRecapService) Check(ctx context.Context) error {
	return lo.Ternary(s.started, nil, fmt.Errorf("auto recap not started yet"))
}

func Run() func(service *AutoRecapService) {
	return func(service *AutoRecapService) {
		service.started = true
	}
}

func (m *AutoRecapService) sendChatHistoriesRecapTimeCapsuleHandler(
	digger *timecapsule.TimeCapsuleDigger[timecapsules.AutoRecapCapsule],
	capsule *timecapsule.TimeCapsule[timecapsules.AutoRecapCapsule],
) {
	m.logger.Debug("send chat histories recap time capsule handler invoked", zap.Int64("chat_id", capsule.Payload.ChatID))

	var enabled bool
	var options *ent.TelegramChatRecapsOptions
	var subscribers []*ent.TelegramChatAutoRecapsSubscribers

	may := fo.NewMay[int]()

	_ = may.Invoke(lo.Attempt(10, func(index int) error {
		var err error

		enabled, err = m.tgchats.HasChatHistoriesRecapEnabledForGroups(capsule.Payload.ChatID, "")
		if err != nil {
			m.logger.Error("failed to check chat histories recap enabled", zap.Error(err))
		}

		return err
	}))
	_ = may.Invoke(lo.Attempt(10, func(index int) error {
		var err error

		options, err = m.tgchats.FindOneRecapsOption(capsule.Payload.ChatID)
		if err != nil {
			m.logger.Error("failed to find chat recap options", zap.Error(err))
		}

		return err
	}))
	_ = may.Invoke(lo.Attempt(10, func(index int) error {
		var err error

		subscribers, err = m.tgchats.FindAutoRecapsSubscribers(capsule.Payload.ChatID)
		if err != nil {
			m.logger.Error("failed to find chat recap subscribers", zap.Error(err))
		}

		return err
	}))

	may.HandleErrors(func(errs []error) {
		// requeue if failed
		queueErr := m.tgchats.QueueOneSendChatHistoriesRecapTaskForChatID(capsule.Payload.ChatID, options)
		if queueErr != nil {
			m.logger.Error("failed to queue one send chat histories recap task for chat", zap.Int64("chat_id", capsule.Payload.ChatID), zap.Error(queueErr))
		}

		m.logger.Error("failed to check chat histories recap enabled, options or subscribers", zap.Error(multierr.Combine(errs...)))
	})
	if !enabled {
		m.logger.Debug("chat histories recap disabled, skipping...", zap.Int64("chat_id", capsule.Payload.ChatID))

		return
	}

	// always requeue
	err := m.tgchats.QueueOneSendChatHistoriesRecapTaskForChatID(capsule.Payload.ChatID, options)
	if err != nil {
		m.logger.Error("failed to queue one send chat histories recap task for chat", zap.Int64("chat_id", capsule.Payload.ChatID), zap.Error(err))
	}
	if options != nil && tgchat.AutoRecapSendMode(options.AutoRecapSendMode) == tgchat.AutoRecapSendModeOnlyPrivateSubscriptions && len(subscribers) == 0 {
		m.logger.Debug("chat histories recap send mode is only private subscriptions, but no subscribers, skipping...", zap.Int64("chat_id", capsule.Payload.ChatID))

		return
	}

	pool := pool.New().WithMaxGoroutines(20)
	pool.Go(func() {
		m.summarize(capsule.Payload.ChatID, options, subscribers)
	})
}

func (m *AutoRecapService) summarize(chatID int64, options *ent.TelegramChatRecapsOptions, subscribers []*ent.TelegramChatAutoRecapsSubscribers) {
	m.logger.Info("generating chat histories recap for chat",
		zap.Int64("chat_id", chatID),
		zap.String("module", "autorecap"),
		zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
	)

	chat, err := m.botService.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{
			ChatID: chatID,
		},
	})
	if err != nil {
		m.logger.Error("failed to get chat",
			zap.Int64("chat_id", chatID),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
			zap.Error(err),
		)
		return
	}

	chatType := telegram.ChatType(chat.Type)
	hours := 6 // 預設為6小時

	// 根據每日回顧次數設定小時間隔
	switch options.AutoRecapRatesPerDay {
	case 2:
		hours = 12
	case 3:
		hours = 8
	case 4:
		hours = 6
	}

	// 獲取聊天歷史
	var histories []*ent.ChatHistories
	var findErr error

	switch hours {
	case 6:
		histories, findErr = m.chathistories.FindLast6HourChatHistories(chatID)
	case 8:
		histories, findErr = m.chathistories.FindLast8HourChatHistories(chatID)
	case 12:
		histories, findErr = m.chathistories.FindLast12HourChatHistories(chatID)
	}

	if findErr != nil {
		m.logger.Error(fmt.Sprintf("failed to find last %d hour chat histories", hours),
			zap.Int64("chat_id", chatID),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
			zap.Error(findErr),
		)
		return
	}

	if len(histories) <= 5 {
		m.logger.Warn("no enough chat histories")
		return
	}

	chatTitle := histories[len(histories)-1].ChatTitle

	// 生成摘要
	logID, summarizations, recapTrace, err := m.chathistories.SummarizeChatHistories(chatID, chatType, histories)
	if err != nil {
		m.logger.Error(fmt.Sprintf("failed to summarize last %d hour chat histories", hours),
			zap.Int64("chat_id", chatID),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
			zap.Error(err),
		)
		return
	}

	// 獲取反饋統計
	counts, err := m.chathistories.FindFeedbackRecapsReactionCountsForChatIDAndLogID(chatID, logID)
	if err != nil {
		m.logger.Error("failed to find feedback recaps votes for chat",
			zap.Int64("chat_id", chatID),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
			zap.Error(err),
		)
		return
	}

	// 創建投票按鈕
	inlineKeyboardMarkup, err := m.chathistories.NewVoteRecapInlineKeyboardMarkup(m.botService.Bot(), chatID, logID, counts.UpVotes, counts.DownVotes, counts.Lmao)
	if err != nil {
		m.logger.Error("failed to create vote recap inline keyboard markup",
			zap.Int64("chat_id", chatID),
			zap.String("log_id", logID.String()),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
			zap.Error(err),
		)
		return
	}

	// 過濾空摘要
	summarizations = lo.Filter(summarizations, func(item string, _ int) bool { return item != "" })
	if len(summarizations) == 0 {
		m.logger.Warn("summarization is empty",
			zap.Int64("chat_id", chatID),
			zap.String("module", "autorecap"),
			zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
		)
		return
	}

	// 生成銳評式濃縮總結
	condensedSummary, condensedTrace, err := m.chathistories.GenSarcasticCondensed(chatID, histories)
	if err != nil || strings.TrimSpace(condensedSummary) == "" {
		m.logger.Warn("failed to generate sarcastic condensed summary, using fallback",
			zap.Error(err),
			zap.Int64("chat_id", chatID),
		)

		if len(summarizations) > 0 {
			firstSummary := []rune(strings.TrimSpace(summarizations[0]))
			if len(firstSummary) > 120 {
				firstSummary = firstSummary[:120]
			}
			condensedSummary = string(firstSummary)
		} else {
			condensedSummary = fmt.Sprintf("過去 %d 小時的群組聊天回顧", hours)
		}
	} else {
		condensedSummary = strings.TrimSpace(condensedSummary)
	}

	limiter := ratelimit.New(5)
	targetChats := make([]targetChat, 0, len(subscribers)+1)

	// 回顧內容只隨訂閱提示分成兩種變體，先各組裝一次再發送給所有目標
	buildRecapParts := func(subscriptionChatTitle string) []string {
		visibleSummary := chathistories.BuildRichRecapSummary(chathistories.RichRecapSummaryConfig{
			Title:                 chatTitle,
			Hours:                 hours,
			Automatic:             true,
			CondensedSummary:      condensedSummary,
			GeneralGroupNotice:    chatType == telegram.ChatTypeGroup,
			SubscriptionChatTitle: subscriptionChatTitle,
			CondensedTrace:        condensedTrace,
			RecapTrace:            recapTrace,
		})

		return chathistories.ComposeRichRecapMessages(visibleSummary, summarizations)
	}

	var publicParts, subscriberParts []string

	if tgchat.AutoRecapSendMode(options.AutoRecapSendMode) == tgchat.AutoRecapSendModePublicly {
		publicParts = buildRecapParts("")

		targetChats = append(targetChats, targetChat{
			chatID:              chatID,
			isPrivateSubscriber: false,
		})
	}

	if len(subscribers) > 0 {
		subscriberParts = buildRecapParts(chatTitle)
	}

	for _, subscriber := range subscribers {
		targetChats = append(targetChats, targetChat{
			chatID:              subscriber.UserID,
			isPrivateSubscriber: true,
		})
	}

	// 發送訊息到所有目標聊天
	for _, targetChat := range targetChats {
		m.logger.Info("sending chat histories recap for chat",
			zap.Int64("summarized_for_chat_id", chatID),
			zap.Int64("sending_target_chat_id", targetChat.chatID))

		targetKeyboard := inlineKeyboardMarkup
		if targetChat.isPrivateSubscriber {
			targetKeyboard, err = m.chathistories.NewVoteRecapWithUnsubscribeInlineKeyboardMarkup(
				m.botService.Bot(), chatID, chatTitle, targetChat.chatID, logID,
				counts.UpVotes, counts.DownVotes, counts.Lmao)
			if err != nil {
				m.logger.Error("failed to assign callback query data",
					zap.Int64("chat_id", chatID),
					zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
					zap.Error(err),
				)
				continue
			}
		}

		parts := publicParts
		if targetChat.isPrivateSubscriber {
			parts = subscriberParts
		}

		if len(parts) == 0 {
			m.logger.Error("rich recap composer returned no messages", zap.Int64("chat_id", chatID))
			continue
		}

		sentMessages, err := recapdelivery.SendRichRecapParts(m.botService, recapdelivery.Config{
			ChatID:      targetChat.chatID,
			Parts:       parts,
			ReplyMarkup: &targetKeyboard,
			BeforeSend: func() {
				limiter.Take()
			},
		})
		if err != nil {
			m.logger.Error("failed to send rich chat histories recap",
				zap.Int64("chat_id", chatID),
				zap.Int64("target_chat_id", targetChat.chatID),
				zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
				zap.Int("sent_message_count", len(sentMessages)),
				zap.Error(err),
			)
			for index := range sentMessages {
				if saveErr := m.chathistories.SaveOneTelegramSentMessage(&sentMessages[index], false); saveErr != nil {
					m.logger.Error("failed to save partially sent telegram message",
						zap.Int64("chat_id", chatID),
						zap.Int("message_id", sentMessages[index].MessageID),
						zap.Error(saveErr),
					)
				}
			}
			continue
		}
		if len(sentMessages) == 0 {
			m.logger.Error("rich recap sender returned no messages", zap.Int64("chat_id", chatID))
			continue
		}

		pinFirstMessage := options.PinAutoRecapMessage && !targetChat.isPrivateSubscriber
		pinSucceeded := false
		if pinFirstMessage {
			lastPinnedMessage, err := m.chathistories.FindLastTelegramPinnedMessage(chatID)
			if err != nil {
				m.logger.Error("failed to find last pinned message",
					zap.Int64("chat_id", chatID),
					zap.Error(err),
				)
			} else {
				if unpinErr := m.botService.UnpinChatMessage(tgbot.NewUnpinChatMessageConfig(chatID, lastPinnedMessage.MessageID)); unpinErr != nil {
					m.logger.Error("failed to unpin chat message", zap.Error(unpinErr), zap.Int64("chat_id", chatID))
				}
				if updateErr := m.chathistories.UpdatePinnedMessage(lastPinnedMessage.ChatID, lastPinnedMessage.MessageID, false); updateErr != nil {
					m.logger.Error("failed to update previous pinned message", zap.Error(updateErr), zap.Int64("chat_id", chatID))
				}
			}

			if pinErr := m.botService.PinChatMessage(tgbot.NewPinChatMessageConfig(chatID, sentMessages[0].MessageID)); pinErr != nil {
				m.logger.Error("failed to pin first rich recap message", zap.Error(pinErr), zap.Int64("chat_id", chatID))
			} else {
				pinSucceeded = true
			}
		}

		for index := range sentMessages {
			isPinned := pinSucceeded && index == 0
			if saveErr := m.chathistories.SaveOneTelegramSentMessage(&sentMessages[index], isPinned); saveErr != nil {
				m.logger.Error("failed to save one telegram sent message",
					zap.Int64("chat_id", chatID),
					zap.Int("message_id", sentMessages[index].MessageID),
					zap.Error(saveErr),
				)
			}
		}
	}
}
