package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/celestix/telegraph-go/v2"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
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
	pageSizeLimit      = 60 * 1024       // 60 KB limit for content
	safetyBuffer       = 2 * 1024        // 2 KB safety buffer for serialization overhead
)

type Service struct {
	cfg    *configs.Config
	client *telegraph.TelegraphClient
	openai openai.Client
	logger *logger.Logger
	bot    *tgbotapi.BotAPI
}

type NewServiceParams struct {
	fx.In

	Config    *configs.Config
	Client    *telegraph.TelegraphClient
	OpenAI    openai.Client
	Logger    *logger.Logger
	Lifecycle fx.Lifecycle
}

func NewService(params NewServiceParams) *Service {
	service := &Service{
		cfg:    params.Config,
		client: params.Client,
		openai: params.OpenAI,
		logger: params.Logger,
	}

	var err error
	service.bot, err = tgbotapi.NewBotAPI(params.Config.Telegram.BotToken)
	if err != nil {
		service.logger.Error("failed to create telegram bot API", zap.Error(err))
	}

	params.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go service.maybeRunPagingTest() // 啟動測試
			return nil
		},
	})

	return service
}

func init() {
	// no-op placeholder to satisfy linter for possible future init logic
}

func (s *Service) maybeRunPagingTest() {
	if !s.cfg.TelegraphPagingTestEnabled {
		return
	}

	s.logger.Info("paging test: enabled, starting test")

	// 1. 檢查測試文件路徑
	if s.cfg.TelegraphPagingTestFile == "" {
		s.logger.Error("paging test: TELEGRAPH_PAGING_TEST_FILE not configured")
		return
	}

	// 使用絕對路徑
	testFilePath := s.cfg.TelegraphPagingTestFile
	if !strings.HasPrefix(testFilePath, "/") && !strings.Contains(testFilePath, ":\\") {
		// 如果是相對路徑，轉換為絕對路徑
		pwd, err := os.Getwd()
		if err != nil {
			s.logger.Error("paging test: failed to get working directory",
				zap.Error(err))
			return
		}
		testFilePath = filepath.Join(pwd, testFilePath)
	}

	s.logger.Info("paging test: using test file",
		zap.String("file_path", testFilePath))

	// 2. 讀取測試文件內容
	testContent, err := os.ReadFile(testFilePath)
	if err != nil {
		s.logger.Error("paging test: failed to read test file",
			zap.Error(err),
			zap.String("file_path", testFilePath))
		return
	}

	if len(testContent) == 0 {
		s.logger.Error("paging test: test file is empty",
			zap.String("file_path", testFilePath))
		return
	}

	testContentStr := string(testContent)

	// 3. 建立時間戳記和標題
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	// 使用更有意義的標題格式，模擬群組名、用戶和時間
	groupName := "ZETA的AI資料群組"
	userName := "測試用戶"
	baseTitle := fmt.Sprintf("%s %s觸發 %s", groupName, userName, timestamp)

	// 4. 先使用 OpenAI 生成摘要（Recap）
	var recapMarkdown string
	var sarcasticSummary string

	if s.openai != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// 將內容根據 token 限制截斷，避免 prompt 過長
		truncated := testContentStr
		if len(testContentStr) > 30000 {
			truncated = testContentStr[:30000]
			s.logger.Info("paging test: content truncated from original length",
				zap.Int("original_length", len(testContentStr)),
				zap.Int("truncated_length", 30000))
		}

		// 先生成詳細摘要
		s.logger.Info("paging test: requesting OpenAI for detailed summary")
		summaryResp, err := s.openai.SummarizeAny(ctx, truncated)
		if err != nil {
			s.logger.Warn("paging test: failed to get detailed summary", zap.Error(err))
		} else if len(summaryResp.Choices) > 0 {
			recapMarkdown = strings.TrimSpace(summaryResp.Choices[0].Message.Content)
			s.logger.Info("paging test: successfully generated detailed summary",
				zap.Int("length", len(recapMarkdown)))
		}

		// 再生成銳評式濃縮總結
		if recapMarkdown != "" {
			s.logger.Info("paging test: requesting OpenAI for sarcastic condensed summary")
			sarcasticSummary, err = s.openai.SarcasticCondense(ctx, recapMarkdown)
			if err != nil {
				s.logger.Warn("paging test: failed to get sarcastic summary", zap.Error(err))
			} else {
				sarcasticSummary = strings.TrimSpace(sarcasticSummary)
				s.logger.Info("paging test: successfully generated sarcastic summary",
					zap.Int("length", len(sarcasticSummary)))
			}
		}
	}

	// 如果沒有獲取到 OpenAI 的摘要，使用預設文本
	if recapMarkdown == "" {
		excerpt := testContentStr
		if len(excerpt) > 500 {
			excerpt = excerpt[:500]
		}
		recapMarkdown = fmt.Sprintf("(Recap 生成失敗，以下為原始內容節錄)\n\n%s", excerpt)
	}

	if sarcasticSummary == "" {
		sarcasticSummary = "Telegraph 長文本分頁測試內容。"
	}

	// 5. 將 Markdown 轉換為 HTML
	htmlContent := fmt.Sprintf("<h3>📝 聊天摘要</h3><p>%s</p><hr><h3>💬 原始內容</h3><p>%s</p>",
		strings.ReplaceAll(recapMarkdown, "\n\n", "</p><p>"),
		strings.ReplaceAll(testContentStr, "\n", "</p><p>"))

	// 6. 創建 Telegraph 頁面（支援自動分頁）
	var urls []string

	// 檢測是否需要分頁（根據序列化後的實際 JSON 大小）
	needPaging := func(html string) bool {
		nodes, err := telegraph.ContentFormat(html)
		if err != nil {
			s.logger.Warn("failed to format content for paging check", zap.Error(err))
			return len(html) > pageSizeLimit-safetyBuffer
		}
		jsonBytes, err := json.Marshal(nodes)
		if err != nil {
			s.logger.Warn("failed to marshal nodes for paging check", zap.Error(err))
			return len(html) > pageSizeLimit-safetyBuffer
		}
		s.logger.Info("paging test: content size check",
			zap.Int("json_size", len(jsonBytes)),
			zap.Int("limit", pageSizeLimit),
			zap.Bool("needs_paging", len(jsonBytes) > pageSizeLimit-safetyBuffer))
		return len(jsonBytes) > pageSizeLimit-safetyBuffer
	}

	if needPaging(htmlContent) {
		// 使用多頁方法
		s.logger.Info("paging test: content needs paging, creating page series")
		urls, err = s.CreatePageSeries(context.Background(), baseTitle, htmlContent)
		if err != nil {
			s.logger.Error("paging test: failed to create telegraph page series",
				zap.Error(err),
				zap.String("title", baseTitle))
			return
		}

		if len(urls) == 0 {
			s.logger.Error("paging test: no telegraph URLs returned")
			return
		}
	} else {
		// 使用單頁方法
		s.logger.Info("paging test: content fits in single page")
		singlePageURL, err := s.CreatePage(context.Background(), baseTitle, htmlContent)
		if err != nil {
			s.logger.Error("paging test: failed to create telegraph page",
				zap.Error(err),
				zap.String("title", baseTitle))
			return
		}
		urls = []string{singlePageURL}
	}

	// 7. 發送訊息到測試群組
	if s.cfg.AutoRecapTestChatID == 0 {
		s.logger.Error("paging test: AUTO_RECAP_TEST_CHAT_ID not configured")
		return
	}

	// 生成訊息格式
	var pagesInfo string
	if len(urls) > 1 {
		// 多頁：列出各頁連結
		pagesLinks := make([]string, len(urls))
		for i, url := range urls {
			pagesLinks[i] = fmt.Sprintf("<a href=\"%s\">第 %d 部分</a>", url, i+1)
		}
		pagesInfo = fmt.Sprintf("📑 <b>分頁總結</b>：%s", strings.Join(pagesLinks, " | "))
	} else if len(urls) == 1 {
		// 單頁：只顯示一個連結
		pagesInfo = fmt.Sprintf("📝 <a href=\"%s\">查看完整總結</a>", urls[0])
	}

	// 組合訊息內容
	messageText := fmt.Sprintf("🔄 <b>%s 聊天總結</b>\n\n<b>時間:</b> %s\n<b>觸發:</b> %s\n\n%s\n\n<b>💡 銳評:</b>\n%s",
		groupName,
		timestamp,
		userName,
		pagesInfo,
		sarcasticSummary)

	// 發送到測試群組
	msg := tgbotapi.NewMessage(s.cfg.AutoRecapTestChatID, messageText)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = false

	resp, err := s.bot.Send(msg)
	if err != nil {
		s.logger.Error("paging test: failed to send test message",
			zap.Error(err),
			zap.Int64("chat_id", s.cfg.AutoRecapTestChatID))
		return
	}

	s.logger.Info("paging test: successfully sent test message to chat",
		zap.Int64("chat_id", s.cfg.AutoRecapTestChatID),
		zap.Int("message_id", resp.MessageID),
		zap.Strings("urls", urls))
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
	// 先檢查內容轉換為 JSON 後的大小
	nodes, err := telegraph.ContentFormat(html)
	if err != nil {
		s.logger.Warn("failed to format content for Telegraph", zap.Error(err))
		// 如果格式化失敗，退回到依據字符計算
		if len(html) < pageSizeLimit-safetyBuffer {
			return []string{html}
		}
	} else {
		jsonBytes, err := json.Marshal(nodes)
		if err != nil {
			s.logger.Warn("failed to marshal nodes for size check", zap.Error(err))
		} else if len(jsonBytes)+safetyBuffer < pageSizeLimit {
			// 如果整個內容在大小限制內，直接返回
			s.logger.Info("content size check passed, no need for splitting",
				zap.Int("json_size", len(jsonBytes)),
				zap.Int("limit", pageSizeLimit))
			return []string{html}
		}
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
		testHTML := currentPart + p
		nodes, err := telegraph.ContentFormat(testHTML)
		if err != nil {
			s.logger.Warn("failed to format content for size check",
				zap.Error(err),
				zap.Int("current_part_length", len(currentPart)),
				zap.Int("paragraph_length", len(p)))

			// 如果格式化失敗，退回到依據字符計算
			if len(testHTML) >= pageSizeLimit-safetyBuffer {
				// 當前部分已滿，添加頁腳並保存
				footerHTML := "<hr><p><em>（本頁面為分割內容，請查看系列頁面獲取完整總結）</em></p>"
				currentPart += footerHTML
				parts = append(parts, currentPart)

				// 開始新的部分，添加頁面標題和提示
				currentPart = fmt.Sprintf("<p><strong>%s（續 %d）</strong></p>", title, len(parts)+1)
				currentPart += "<p><strong>注意：</strong>這是分割內容的續頁</p><hr>"
				currentPart += p // 添加當前段落到新頁面
				continue
			}
		} else {
			jsonBytes, err := json.Marshal(nodes)
			if err != nil {
				s.logger.Warn("failed to marshal nodes for size check", zap.Error(err))
			} else if len(jsonBytes)+safetyBuffer >= pageSizeLimit {
				s.logger.Info("splitting content at paragraph",
					zap.Int("part_index", len(parts)+1),
					zap.Int("json_size", len(jsonBytes)),
					zap.Int("limit", pageSizeLimit))

				// 當前部分已滿，添加頁腳並保存
				footerHTML := "<hr><p><em>（本頁面為分割內容，請查看系列頁面獲取完整總結）</em></p>"
				currentPart += footerHTML
				parts = append(parts, currentPart)

				// 開始新的部分，添加頁面標題和提示
				currentPart = fmt.Sprintf("<p><strong>%s（續 %d）</strong></p>", title, len(parts)+1)
				currentPart += "<p><strong>注意：</strong>這是分割內容的續頁</p><hr>"
				currentPart += p // 添加當前段落到新頁面
				continue
			}
		}

		// 如果沒有超過大小限制，添加段落到當前部分
		currentPart += p
	}

	// 添加最後一部分（如果有內容的話）
	if len(currentPart) > 0 && currentPart != headerHTML {
		footerHTML := "<hr><p><em>（系列頁面結束）</em></p>"
		currentPart += footerHTML
		parts = append(parts, currentPart)
	}

	s.logger.Info("content successfully split into parts",
		zap.Int("total_parts", len(parts)))

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
	var createErrors []error

	// 先創建所有頁面
	for i, part := range parts {
		pageTitle := title
		if i > 0 {
			// 為多頁添加更有意義的標題後綴
			pageTitle = fmt.Sprintf("%s（第 %d 部分）", title, i+1)
		}

		url, err := s.CreatePage(ctx, pageTitle, part)
		if err != nil {
			s.logger.Error("failed to create part of the Telegraph page series",
				zap.Error(err),
				zap.String("title", pageTitle),
				zap.Int("part", i+1),
				zap.Int("total_parts", len(parts)),
			)
			createErrors = append(createErrors, err)
			continue
		}

		urls = append(urls, url)
		pageTitles = append(pageTitles, pageTitle)

		// throttle to avoid ACCESS_TOKEN_INVALID
		time.Sleep(pageCreateInterval)
	}

	// 檢查是否所有頁面都創建成功
	if len(createErrors) > 0 {
		return urls, fmt.Errorf("failed to create some pages in series: %v", createErrors)
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

	// 等待一段時間再進行編輯，避免 token 失效
	time.Sleep(pageCreateInterval * 2)

	// 逐頁編輯，插入鏈接
	var editErrors []error
	for i, u := range urls {
		path := strings.TrimPrefix(u, "https://telegra.ph/")
		newHTML := seriesHeader + parts[i]
		_, err := s.EditPage(ctx, path, pageTitles[i], newHTML)
		if err != nil {
			s.logger.Warn("failed to edit page to add series links",
				zap.Error(err),
				zap.String("url", u),
				zap.Int("page", i+1),
			)
			editErrors = append(editErrors, err)
		}

		time.Sleep(pageCreateInterval)
	}

	// 記錄編輯錯誤但不中斷流程
	if len(editErrors) > 0 {
		s.logger.Error("some pages failed to be edited with series links",
			zap.Errors("errors", editErrors))
	}

	s.logger.Info("successfully created Telegraph page series",
		zap.Int("total_pages", len(urls)),
		zap.String("title", title),
		zap.Strings("urls", urls),
	)

	return urls, nil
}
