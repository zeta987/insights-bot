package autorecap

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	TelegraphService "github.com/nekomeowww/insights-bot/internal/services/telegraph"
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
	Telegraph     *TelegraphService.Service
	Config        *configsPkg.Config
}

type AutoRecapService struct {
	logger        *logger.Logger
	botService    *tgbot.BotService
	chathistories *chathistories.Model
	tgchats       *tgchats.Model

	digger    *datastore.AutoRecapTimeCapsuleDigger
	telegraph *TelegraphService.Service
	started   bool
	Config    *configsPkg.Config
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
			telegraph:     params.Telegraph,
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
	logID, summarizations, err := m.chathistories.SummarizeChatHistories(chatID, chatType, histories)
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

	// 處理 Markdown 標題
	for i, s := range summarizations {
		summarizations[i] = tgbot.ReplaceMarkdownTitlesToTelegramBoldElement(s)
	}

	// 合併所有摘要
	rawSummaryAll := strings.Join(summarizations, "\n\n")
	modelName := m.chathistories.GetOpenAIModelName()

	// 生成銳評式濃縮總結
	condensedSummary, err := m.chathistories.GenSarcasticCondensed(chatID, histories)
	if err != nil || strings.TrimSpace(condensedSummary) == "" {
		m.logger.Warn("failed to generate sarcastic condensed summary, using fallback",
			zap.Error(err),
			zap.Int64("chat_id", chatID),
		)

		if len(summarizations) > 0 {
			firstSummary := summarizations[0]
			if len(firstSummary) > 50 {
				condensedSummary = firstSummary[:50] + "..."
			} else {
				condensedSummary = firstSummary
			}
		} else {
			condensedSummary = fmt.Sprintf("過去 %d 小時的群組聊天回顧", hours)
		}
	} else {
		condensedSummary = strings.TrimSpace(condensedSummary)
	}

	// 準備 Telegraph 文章內容
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	pageTitle := fmt.Sprintf("【群組 %s】自動 %d 小時總結", tgbot.EscapeHTMLSymbols(chatTitle), hours)

	htmlSummary := fmt.Sprintf("<p><small>統計時間範圍：於 %s 發起的過去 %d 小時</small></p><hr>", timestamp, hours)
	paragraphsAll := strings.Split(rawSummaryAll, "\n\n")
	for _, p := range paragraphsAll {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if strings.HasPrefix(p, "##") {
			titleText := strings.TrimSpace(strings.TrimPrefix(p, "##"))
			htmlSummary += "<h2>" + titleText + "</h2>"
			continue
		}

		p = strings.ReplaceAll(p, "*", "<b>")
		p = strings.ReplaceAll(p, "*", "</b>")
		p = strings.ReplaceAll(p, "_", "<i>")
		p = strings.ReplaceAll(p, "_", "</i>")

		htmlSummary += "<p>" + p + "</p>"
	}

	htmlSummary += "<hr><p><em>由 " + modelName + " 生成</em></p>"

	// 建立 Telegraph 文章
	var telegraphURL string
	var telegraphURLs []string
	if len(htmlSummary) > 60*1024 {
		telegraphURLs, err = m.telegraph.CreatePageSeries(context.Background(), pageTitle, htmlSummary)
		if err != nil {
			m.logger.Error("failed to create telegraph page series for auto recap",
				zap.Error(err),
				zap.Int64("chat_id", chatID),
				zap.String("title", pageTitle),
			)
			return
		}
		if len(telegraphURLs) > 0 {
			telegraphURL = telegraphURLs[0]
		}
	} else {
		telegraphURL, err = m.telegraph.CreatePage(context.Background(), pageTitle, htmlSummary)
		if err != nil {
			m.logger.Error("failed to create telegraph page for auto recap",
				zap.Error(err),
				zap.Int64("chat_id", chatID),
				zap.String("title", pageTitle),
			)
			return
		}
		telegraphURLs = []string{telegraphURL}
	}

	if telegraphURL == "" {
		m.logger.Error("telegraph url is empty, aborting auto recap sending", zap.Int64("chat_id", chatID))
		return
	}

	// 準備多頁提醒資訊
	multiPageInfo := ""
	if len(telegraphURLs) > 1 {
		multiPageInfo = "\n\n<b>注意：</b>由於內容較長，已分為 " + strconv.Itoa(len(telegraphURLs)) + " 個頁面："
		for i, url := range telegraphURLs {
			multiPageInfo += fmt.Sprintf("\n- <a href=\"%s\">第 %d 部分</a>", url, i+1)
		}
	}

	// 準備 Telegram 訊息內容
	content := fmt.Sprintf(
		"📝 <b>自動聊天回顧已發布到 Telegraph</b>: <a href=\"%s\">%s</a>%s\n\n<b>濃縮總結：</b>\n%s\n\n%s#recap #recap_auto\n🤖️ 由 %s 生成",
		telegraphURL,
		tgbot.EscapeHTMLSymbols(pageTitle),
		multiPageInfo,
		condensedSummary,
		lo.Ternary(chatType == telegram.ChatTypeGroup, "<b>Tips: </b>由于群组不是超级群组（supergroup），因此消息链接引用暂时被禁用了，如果希望使用该功能，请通过短时间内将群组开放为公共群组并还原回私有群组，或通过其他操作将本群组升级为超级群组后，该功能方可恢复正常运作。\n\n", ""),
		modelName,
	)

	// 發送訊息
	limiter := ratelimit.New(5) // 限制每秒最多發送5條訊息
	targetChats := make([]targetChat, 0)

	// 根據發送模式決定目標聊天
	if tgchat.AutoRecapSendMode(options.AutoRecapSendMode) == tgchat.AutoRecapSendModePublicly {
		targetChats = append(targetChats, targetChat{
			chatID:              chatID,
			isPrivateSubscriber: false,
		})
	}

	for _, subscriber := range subscribers {
		targetChats = append(targetChats, targetChat{
			chatID:              subscriber.UserID,
			isPrivateSubscriber: true,
		})
	}

	// 發送訊息到所有目標聊天
	for _, targetChat := range targetChats {
		limiter.Take()
		m.logger.Info("sending chat histories recap for chat",
			zap.Int64("summarized_for_chat_id", chatID),
			zap.Int64("sending_target_chat_id", targetChat.chatID))

		msg := tgbotapi.NewMessage(targetChat.chatID, "")
		msg.ParseMode = tgbotapi.ModeHTML

		if targetChat.isPrivateSubscriber {
			msg.Text = fmt.Sprintf("您好，这是您订阅的 <b>%s</b> 群组的定时聊天回顾。\n\n%s",
				tgbot.EscapeHTMLSymbols(chatTitle), content)

			inlineKeyboardMarkup, err := m.chathistories.NewVoteRecapWithUnsubscribeInlineKeyboardMarkup(
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

			msg.ReplyMarkup = inlineKeyboardMarkup
		} else {
			msg.Text = content
			msg.ReplyMarkup = inlineKeyboardMarkup
		}

		sentMsg, err := m.botService.Send(msg)
		if err != nil {
			m.logger.Error("failed to send chat histories recap",
				zap.Int64("chat_id", chatID),
				zap.Int("auto_recap_rates", options.AutoRecapRatesPerDay),
				zap.Error(err),
			)
			continue
		}

		// 處理訊息釘選
		if options.PinAutoRecapMessage && !targetChat.isPrivateSubscriber {
			may := fo.NewMay0().Use(func(err error, messageArgs ...any) {
				if len(messageArgs) == 0 {
					m.logger.Error(err.Error())
					return
				}
				prefix, _ := messageArgs[0].(string)

				if len(messageArgs) == 1 {
					m.logger.Error(prefix, zap.Error(err))
					return
				}
				fields := make([]zap.Field, 0)
				fields = append(fields, zap.Error(err))

				for i, v := range messageArgs[1:] {
					field, ok := v.(zap.Field)
					if !ok {
						fields = append(fields, zap.Any(fmt.Sprintf("error_field_%d", i), field))
					} else {
						fields = append(fields, field)
					}
				}

				m.logger.Error(prefix, fields...)
			})

			// 取消釘選上一條訊息
			lastPinnedMessage, err := m.chathistories.FindLastTelegramPinnedMessage(chatID)
			if err != nil {
				m.logger.Error("failed to find last pinned message",
					zap.Int64("chat_id", chatID),
					zap.Error(err),
				)
			} else {
				may.Invoke(
					m.botService.UnpinChatMessage(tgbot.NewUnpinChatMessageConfig(chatID, lastPinnedMessage.MessageID)),
					"failed to unpin chat message",
					zap.Int64("chat_id", chatID),
					zap.Int("message_id", lastPinnedMessage.MessageID),
				)
				may.Invoke(
					m.chathistories.UpdatePinnedMessage(lastPinnedMessage.ChatID, lastPinnedMessage.MessageID, false),
					"failed to save one telegram sent message",
					zap.Int64("chat_id", lastPinnedMessage.ChatID),
					zap.Int("message_id", lastPinnedMessage.MessageID),
				)
			}

			// 釘選新訊息
			may.Invoke(
				m.botService.PinChatMessage(tgbot.NewPinChatMessageConfig(chatID, sentMsg.MessageID)),
				"failed to pin chat message",
				zap.Int64("chat_id", chatID),
				zap.Int("message_id", sentMsg.MessageID),
			)
			may.Invoke(
				m.chathistories.SaveOneTelegramSentMessage(&sentMsg, true),
				"failed to save one telegram sent message",
			)
		} else {
			err = m.chathistories.SaveOneTelegramSentMessage(&sentMsg, false)
			if err != nil {
				m.logger.Error("failed to save one telegram sent message",
					zap.Int64("chat_id", chatID),
					zap.Error(err))
			}
		}
	}
}

/*
// sendAutoRecapToGroup sends an auto-recap to a Telegram group, now creating a Telegraph page first
func (s *AutoRecapService) sendAutoRecapToGroup(
	ctx context.Context,
	bot *tgbot.Bot,
	c *ent.TelegramChat,
	o *ent.TelegramChatRecapsOptions,
	summarizations []string,
	ratesParDayRate int,
) error {
	// ... function body ...
	return nil
}

// sendRegularAutoRecapToGroup is a fallback function for when Telegraph creation fails
func (s *AutoRecapService) sendRegularAutoRecapToGroup(
	ctx context.Context,
	bot *tgbot.Bot,
	c *ent.TelegramChat,
	o *ent.TelegramChatRecapsOptions,
	summarizations []string,
	ratesParDayRate int,
) error {
	// ... function body ...
	return nil
}

// sendAutoRecapToPrivate sends an auto-recap to a Telegram private chat, now creating a Telegraph page first
func (s *AutoRecapService) sendAutoRecapToPrivate(ctx context.Context, bot *tgbot.Bot, c *ent.TelegramChat, subscriberIDs []int64, summarizations []string, ratesParDayRate int) error {
	// ... function body ...
	return nil
}

// sendRegularAutoRecapToPrivate is a fallback function for when Telegraph creation fails
func (s *AutoRecapService) sendRegularAutoRecapToPrivate(ctx context.Context, bot *tgbot.Bot, c *ent.TelegramChat, subscriberIDs []int64, summarizations []string, ratesParDayRate int) error {
	// ... function body ...
	return nil
}
*/

/*
func (s *AutoRecapService) handleSendAutoRecap(ctx context.Context, bot *tgbot.Bot, c *ent.TelegramChat, o *ent.TelegramChatRecapsOptions, subscriberIDs []int64, summarizations []string, ratesParDayRate int) error {
	// ... function body ...
	return nil
}
*/
