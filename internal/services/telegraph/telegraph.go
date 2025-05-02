package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/celestix/telegraph-go/v2"
	"github.com/sourcegraph/conc"
	"go.uber.org/fx"
	"go.uber.org/zap"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/pkg/logger"
)

var Module = fx.Options(
	fx.Provide(NewService),
)

const (
	maxRetries         = 3
	retryDelay         = 1 * time.Second
	pageCreateInterval = 2 * time.Second // throttle between createPage calls
)

type Service struct {
	cfg    *configs.Config
	client *telegraph.TelegraphClient
	logger *logger.Logger
}

type NewServiceParams struct {
	fx.In

	Config    *configs.Config
	Client    *telegraph.TelegraphClient
	Lifecycle fx.Lifecycle
	Logger    *logger.Logger
}

func NewService(param NewServiceParams) *Service {
	svc := &Service{
		cfg:    param.Config,
		client: param.Client,
		logger: param.Logger,
	}

	// 在應用程式啟動後才執行測試，以確保所有依賴（例如網路）已經就緒
	param.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go svc.maybeRunPagingTest()
			return nil
		},
	})

	return svc
}

func init() {
	// no-op placeholder to satisfy linter for possible future init logic
}

func (s *Service) maybeRunPagingTest() {
	if !s.cfg.TelegraphPagingTestEnabled {
		return
	}

	s.logger.Info("paging test: enabled, starting test")

	go func() {
		// 建立測試時間戳記
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		// 準備三個頁面的標題和測試內容
		title := fmt.Sprintf("Telegraph 測試 %s", timestamp)
		testPages := []string{
			"<p>測試1</p>",
			"<p>測試2</p>",
			"<p>測試3</p>",
		}

		urls := make([]string, 0, 3)
		pageTitles := make([]string, 0, 3)

		// 創建三個頁面
		for i, content := range testPages {
			pageTitle := title
			if i > 0 {
				pageTitle = fmt.Sprintf("%s-%d", title, i+1)
			}

			url, err := s.CreatePage(context.Background(), pageTitle, content)
			if err != nil {
				s.logger.Error("paging test: failed to create test page",
					zap.Error(err),
					zap.Int("page", i+1),
					zap.String("title", pageTitle))
				continue
			}

			urls = append(urls, url)
			pageTitles = append(pageTitles, pageTitle)
			time.Sleep(pageCreateInterval)
		}

		if len(urls) == 0 {
			s.logger.Error("paging test: failed to create any test pages")
			return
		}

		// 生成包含所有URL的鏈接列表HTML
		linksHTML := "<p><strong>測試頁面列表：</strong></p><ul>"
		for i, u := range urls {
			linksHTML += fmt.Sprintf("<li><a href=\"%s\">測試 %d</a></li>", u, i+1)
		}
		linksHTML += "</ul><hr>"

		// 將所有URL添加到每個頁面
		for i, u := range urls {
			path := strings.TrimPrefix(u, "https://telegra.ph/")
			newHTML := linksHTML + testPages[i]
			_, err := s.EditPage(context.Background(), path, pageTitles[i], newHTML)
			if err != nil {
				s.logger.Warn("paging test: failed to edit page to add links",
					zap.Error(err),
					zap.String("url", u))
			}
			time.Sleep(pageCreateInterval)
		}

		// 透過Telegram機器人發送測試訊息到測試群組
		if s.cfg.AutoRecapTestChatID != 0 {
			// 準備要發送的訊息
			message := fmt.Sprintf("🔄 <b>Telegraph 分頁測試結果</b>\n\n<b>時間:</b> %s\n\n<b>測試頁面:</b>", timestamp)
			for i, u := range urls {
				message += fmt.Sprintf("\n%d. <a href=\"%s\">測試 %d</a>", i+1, u, i+1)
			}

			// 發送訊息到測試群組
			msg := tgbotapi.NewMessage(s.cfg.AutoRecapTestChatID, message)
			msg.ParseMode = tgbotapi.ModeHTML

			// 使用Bot API發送訊息
			botAPI, err := tgbotapi.NewBotAPI(s.cfg.Telegram.BotToken)
			if err != nil {
				s.logger.Error("paging test: failed to create bot API", zap.Error(err))
				return
			}

			sentMsg, err := botAPI.Send(msg)
			if err != nil {
				s.logger.Error("paging test: failed to send message to test chat",
					zap.Error(err),
					zap.Int64("chat_id", s.cfg.AutoRecapTestChatID))
				return
			}

			s.logger.Info("paging test: successfully sent test message to chat",
				zap.Int("message_id", sentMsg.MessageID),
				zap.Int64("chat_id", s.cfg.AutoRecapTestChatID),
				zap.Strings("urls", urls))
		} else {
			s.logger.Warn("paging test: AUTO_RECAP_TEST_CHAT_ID is not configured, skipping sending test message")
		}

		s.logger.Info("paging test: successfully created telegraph test pages", zap.Strings("urls", urls))
	}()
}

// CreatePage creates a new Telegraph page with the given title and HTML content.
// It returns the URL of the created page.
func (s *Service) CreatePage(ctx context.Context, title, html string) (string, error) {
	if s.cfg.Telegraph.AccessToken == "" {
		return "", fmt.Errorf("telegraph access token is not configured")
	}

	var page *telegraph.Page
	var lastErr error

	// 使用 conc.WaitGroup 來處理重試邏輯
	wg := conc.NewWaitGroup()
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(retryDelay)
		}

		wg.Go(func() {
			// 使用 PageOpts 設置作者名稱
			opts := &telegraph.PageOpts{
				AuthorName:    "ZETA 的總結 AI",
				ReturnContent: false,
			}

			p, err := s.client.CreatePage(
				s.cfg.Telegraph.AccessToken,
				title,
				html,
				opts,
			)

			if err == nil {
				page = p
				return
			}

			lastErr = err
			s.logger.Warn("failed to create Telegraph page, retrying...",
				zap.Error(err),
				zap.String("title", title),
				zap.Int("attempt", i+1),
			)
		})
	}
	wg.Wait()

	if page == nil {
		s.logger.Error("all attempts to create Telegraph page failed",
			zap.Error(lastErr),
			zap.String("title", title),
		)
		return "", fmt.Errorf("failed to create Telegraph page after %d attempts: %w", maxRetries, lastErr)
	}

	// 提取路徑，方便日誌記錄和後續編輯
	path := strings.TrimPrefix(page.Url, "https://telegra.ph/")
	s.logger.Info("successfully created Telegraph page",
		zap.String("url", page.Url),
		zap.String("path", path),
		zap.String("title", title),
		zap.String("edit_url", fmt.Sprintf("https://edit.telegra.ph/%s", path)),
	)
	return page.Url, nil
}

// EditPage edits an existing Telegraph page with the given path, title and HTML content.
// It returns the URL of the edited page.
func (s *Service) EditPage(ctx context.Context, path, title, html string) (string, error) {
	if s.cfg.Telegraph.AccessToken == "" {
		return "", fmt.Errorf("telegraph access token is not configured")
	}

	var page *telegraph.Page
	var lastErr error

	// 從路徑中移除網站前綴（如果有）
	path = strings.TrimPrefix(path, "https://telegra.ph/")

	// 使用 conc.WaitGroup 來處理重試邏輯
	wg := conc.NewWaitGroup()
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(retryDelay)
		}

		wg.Go(func() {
			// 使用 PageOpts 設置作者名稱
			opts := &telegraph.PageOpts{
				AuthorName:    "ZETA 的總結 AI",
				ReturnContent: false,
			}

			p, err := s.client.EditPage(
				s.cfg.Telegraph.AccessToken,
				path,
				title,
				html,
				opts,
			)

			if err == nil {
				page = p
				return
			}

			lastErr = err
			s.logger.Warn("failed to edit Telegraph page, retrying...",
				zap.Error(err),
				zap.String("path", path),
				zap.String("title", title),
				zap.Int("attempt", i+1),
			)
		})
	}
	wg.Wait()

	if page == nil {
		s.logger.Error("all attempts to edit Telegraph page failed",
			zap.Error(lastErr),
			zap.String("path", path),
			zap.String("title", title),
		)
		return "", fmt.Errorf("failed to edit Telegraph page after %d attempts: %w", maxRetries, lastErr)
	}

	s.logger.Info("successfully edited Telegraph page",
		zap.String("url", page.Url),
		zap.String("path", path),
		zap.String("title", title),
	)
	return page.Url, nil
}

// DeletePage "deletes" a Telegraph page by setting its content to empty.
// Telegraph doesn't have a proper delete API, but we can effectively remove content.
// It returns true if the operation was successful.
func (s *Service) DeletePage(ctx context.Context, path string) (bool, error) {
	if s.cfg.Telegraph.AccessToken == "" {
		return false, fmt.Errorf("telegraph access token is not configured")
	}

	// 從路徑中移除網站前綴（如果有）
	path = strings.TrimPrefix(path, "https://telegra.ph/")

	// 將頁面內容設為空，實際上就是"刪除"頁面內容
	emptyHTML := "<p>This page has been deleted</p>"

	var success bool
	var lastErr error

	// 使用 conc.WaitGroup 來處理重試邏輯
	wg := conc.NewWaitGroup()
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(retryDelay)
		}

		wg.Go(func() {
			// 保留原標題，但清空內容
			opts := &telegraph.PageOpts{
				AuthorName:    "ZETA 的總結 AI",
				ReturnContent: false,
			}

			_, err := s.client.EditPage(
				s.cfg.Telegraph.AccessToken,
				path,
				"Deleted Page", // 可以更換為其他標題
				emptyHTML,
				opts,
			)

			if err == nil {
				success = true
				return
			}

			lastErr = err
			s.logger.Warn("failed to delete Telegraph page, retrying...",
				zap.Error(err),
				zap.String("path", path),
				zap.Int("attempt", i+1),
			)
		})
	}
	wg.Wait()

	if !success {
		s.logger.Error("all attempts to delete Telegraph page failed",
			zap.Error(lastErr),
			zap.String("path", path),
		)
		return false, fmt.Errorf("failed to delete Telegraph page after %d attempts: %w", maxRetries, lastErr)
	}

	s.logger.Info("successfully deleted Telegraph page content",
		zap.String("path", path),
	)
	return true, nil
}

// FormatContent formats HTML content for Telegraph
func (s *Service) FormatContent(html string) (string, error) {
	nodes, err := telegraph.ContentFormat(html)
	if err != nil {
		return "", fmt.Errorf("failed to format content for Telegraph: %w", err)
	}

	contentBytes, err := json.Marshal(nodes)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Telegraph nodes: %w", err)
	}

	return string(contentBytes), nil
}

// CheckContentLength checks if the HTML content length is too large for Telegraph API
func (s *Service) CheckContentLength(html string) bool {
	return len(html) < 64*1024 // Telegraph API limit is 64KB
}

// SplitContentIntoParts splits the HTML content into multiple parts if it's too large
// and returns a slice of HTML parts
func (s *Service) SplitContentIntoParts(html string, title string) []string {
	if len(html) < 64*1024 {
		return []string{html}
	}

	// 尋找適合的分割點：保持HTML結構完整性
	parts := []string{}
	currentPart := ""
	paragraphs := strings.Split(html, "</p>")

	// 添加標題和說明
	headerHTML := "<p><strong>注意：</strong>由於內容較長，已自動分割為多個頁面</p><hr>"
	currentPart = headerHTML

	for i, p := range paragraphs {
		// 添加閉合標籤
		if i < len(paragraphs)-1 || strings.TrimSpace(p) != "" {
			p = p + "</p>"
		}

		// 檢查添加此段後是否會超出限制
		if len(currentPart)+len(p) >= 63*1024 { // 留出1KB的安全緩衝區
			// 當前部分已滿，添加頁腳並保存
			footerHTML := "<hr><p><em>（本頁面為分割內容，請查看系列頁面獲取完整總結）</em></p>"
			currentPart += footerHTML
			parts = append(parts, currentPart)

			// 開始新的部分，添加頁面標題和提示
			currentPart = fmt.Sprintf("<p><strong>%s（續 %d）</strong></p>", title, len(parts)+1)
			currentPart += "<p><strong>注意：</strong>這是分割內容的續頁</p><hr>"
		}

		currentPart += p
	}

	// 添加最後一部分（如果有內容的話）
	if len(currentPart) > 0 && currentPart != headerHTML {
		footerHTML := "<hr><p><em>（系列頁面結束）</em></p>"
		currentPart += footerHTML
		parts = append(parts, currentPart)
	}

	return parts
}

// CreatePageSeries creates a series of Telegraph pages if content is too large
// It returns a slice of URLs for all created pages
func (s *Service) CreatePageSeries(ctx context.Context, title string, html string) ([]string, error) {
	if s.cfg.Telegraph.AccessToken == "" {
		return nil, fmt.Errorf("telegraph access token is not configured")
	}

	// 分割內容
	parts := s.SplitContentIntoParts(html, title)
	urls := make([]string, 0, len(parts))
	pageTitles := make([]string, 0, len(parts))

	// 創建多個頁面（含速率限制）
	for i, part := range parts {
		pageTitle := title
		if i > 0 {
			pageTitle = fmt.Sprintf("%s-%d", title, i+1)
		}

		url, err := s.CreatePage(ctx, pageTitle, part)
		if err != nil {
			s.logger.Error("failed to create part of the Telegraph page series",
				zap.Error(err),
				zap.String("title", pageTitle),
				zap.Int("part", i+1),
				zap.Int("total_parts", len(parts)),
			)
			continue
		}

		urls = append(urls, url)
		pageTitles = append(pageTitles, pageTitle)

		// throttle to avoid ACCESS_TOKEN_INVALID
		time.Sleep(pageCreateInterval)
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("failed to create any Telegraph pages for the series")
	}

	// 生成系列鏈接 HTML
	seriesHeader := "<p><strong>系列頁面：</strong></p><ul>"
	for i, u := range urls {
		seriesHeader += fmt.Sprintf("<li><a href=\"%s\">第 %d 部分</a></li>", u, i+1)
	}
	seriesHeader += "</ul><hr>"

	// 逐頁編輯，插入鏈接
	for i, u := range urls {
		path := strings.TrimPrefix(u, "https://telegra.ph/")
		newHTML := seriesHeader + parts[i]
		_, err := s.EditPage(ctx, path, pageTitles[i], newHTML)
		if err != nil {
			s.logger.Warn("failed to edit page to add series links", zap.Error(err), zap.String("url", u))
		}

		time.Sleep(pageCreateInterval)
	}

	s.logger.Info("successfully created Telegraph page series",
		zap.Int("total_pages", len(urls)),
		zap.String("title", title),
		zap.Strings("urls", urls),
	)

	return urls, nil
}
