# Changelog / 更新日誌

## v1.1.0 — 2026-11-16

### 正體中文

- 新增媒體理解（`MEDIA_UNDERSTANDING_ENABLED`，預設關閉）：圖片、貼圖、自訂 emoji、GIF、影片、圓形影片、語音、音訊與檔案都會記入聊天記錄，並在背景由媒體模型產生描述，讓回顧能涵蓋文字以外的內容。描述以 `[photo: …]`、`[voice 0:12: …]`、`[sticker 😂: …]` 等標記寫入，自訂 emoji 則在原本的備用 emoji 後面加上〔描述〕。可用 `MEDIA_UNDERSTANDING_MODEL_NAME`、`MEDIA_UNDERSTANDING_MAX_FILE_MB`、`MEDIA_UNDERSTANDING_MAX_VOICE_SECONDS` 與 `MEDIA_UNDERSTANDING_MAX_VIDEO_SECONDS` 調整。
- 檔案依類型處理：PDF 直接交給模型，Word、Excel、PowerPoint（docx、xlsx、pptx）先擷取文字，純文字與程式碼檔讀成文字後摘要；不支援的類型只記錄檔名與大小。
- 支援 Telegram 自訂 emoji：內建的 Bot API 函式庫會丟掉 `custom_emoji_id`，現在由 webhook 從原始內容補回，再透過 `getCustomEmojiStickers` 取得貼圖並產生描述。
- 透明背景的貼圖與 emoji 會先合成到對比色背景、必要時放大後再交給模型，避免透明處被當成黑色而看不見黑色文字。模型讀不出來時會改用反轉背景、剪影或對比拉伸重試；影片貼圖、GIF 與影片則改用靜態縮圖。
- `CHECK_MODEL` 會審查貼圖、自訂 emoji、透明或過小圖片的描述，必要時透過工具呼叫程式重新算圖，看過新圖後再修正描述。
- 媒體描述依群組分開快取，同一個檔案在不同群組各自描述，群組之間不共用任何內容；同一張貼圖在同一個群組只會描述一次。
- 手動回顧會沿用同一群組在所選時段內已有的自動與手動回顧，只重新摘要尚未涵蓋的訊息，再把結果合併；模型資訊會多一行「沿用回顧」，列出沿用份數與另外摘要的訊息數。預設開啟，可用 `CHAT_HISTORIES_RECAP_REUSE_ENABLED=0` 關閉。資料庫會在啟動時自動新增三個欄位。
- 濃縮總結改為 200-300 字的平實概覽，依重要程度說明各個主要話題與結論，取代原本的一句話銳評。
- 濃縮總結只在模型呼叫失敗（例如 HTTP 429、5xx）或回傳空白內容時才改用備援模型；格式錯誤（程式碼區塊、JSON、多行等）直接交給 `CHECK_MODEL` 修正，不再重新生成。被拒絕的輸出會截取樣本寫入 log，方便追查原因。
- 詳細總結會拒絕含程式碼區塊、外洩草稿或被輸出上限截斷的結果並改用備援模型；`CHECK_MODEL` 也會標出不屬於回顧的行並刪除，不會改寫回顧文字與引用標記。
- 模型資訊中的「Check」改名為「校對模型」，模型名稱也會保留設定中的推理強度後綴（例如 `gpt-6.1-sol-xhigh`），不再只顯示閘道回傳的名稱。
- 對話補全改以串流接收，避免長時間生成被 Cloudflare 的讀取逾時切斷。
- Rich Message 改以 UTF-8 位元組計算分段長度，修正中文回顧尾段被 Telegram 默默截掉的問題。
- 附說明文字的媒體，說明中的連結與自訂 emoji 現在也會被處理。
- 安全強化：宣稱超過 16,777,216 像素的圖片不會解碼；Office 檔案的所有內容共用一個解壓縮上限；Bot API 錯誤不再帶出含 bot token 的網址；媒體描述的審查 log 只在開啟 `OPENAI_VERBOSE_PAYLOAD_LOGS` 時才記錄文字內容。
- 開發：golangci-lint 設定遷移到 v2；新增相依套件 `golang.org/x/image` v0.19.0，用來解碼 WebP 貼圖。

### English

- New media understanding (`MEDIA_UNDERSTANDING_ENABLED`, off by default): photos, stickers, custom emoji, GIFs, videos, round videos, voice messages, audio and files now enter chat histories, and a media model describes them in the background, so recaps cover more than text. Descriptions are stored as markers such as `[photo: …]`, `[voice 0:12: …]` and `[sticker 😂: …]`, and a custom emoji keeps its fallback emoji followed by 〔a description〕. Tune it with `MEDIA_UNDERSTANDING_MODEL_NAME`, `MEDIA_UNDERSTANDING_MAX_FILE_MB`, `MEDIA_UNDERSTANDING_MAX_VOICE_SECONDS` and `MEDIA_UNDERSTANDING_MAX_VIDEO_SECONDS`.
- Files are handled by type: PDFs go to the model as they are, Word, Excel and PowerPoint files (docx, xlsx, pptx) have their text extracted first, and plain-text and code files are read as text and summarized; other types are recorded with their name and size only.
- Telegram custom emoji are supported: the bundled Bot API library drops `custom_emoji_id`, so the webhook now restores it from the raw update and the bot fetches the sticker through `getCustomEmojiStickers` to describe it.
- Transparent stickers and emoji are flattened onto a contrasting background, and enlarged when small, before they reach the model, so black text on transparent pixels no longer vanishes. When the model still cannot read one, the bot retries with the other background, a silhouette or a contrast stretch, and falls back on the still thumbnail for video stickers, GIFs and videos.
- `CHECK_MODEL` reviews the descriptions of stickers, custom emoji and transparent or tiny images, and can re-render the image with code through a tool call before it corrects a description.
- Media descriptions are cached per chat: the same file is described separately in each group, groups never share any of it, and a sticker is described only once per group.
- Manual recaps reuse the automatic and manual recaps that the same chat already has inside the selected window, summarize only the messages they do not cover, and merge the results; the model info gains a "沿用回顧" line with how many recaps were reused and how many messages were summarized anew. It is on by default and `CHAT_HISTORIES_RECAP_REUSE_ENABLED=0` turns it off. Three database columns are added automatically at startup.
- The condensed summary is now a plain 200–300 character overview that states each main topic and its outcome in order of importance, replacing the single-sentence roast.
- Condensed summaries fall back to backup models only when the model call fails (for example HTTP 429 or 5xx) or returns nothing; format problems such as code fences, JSON or multiple lines go straight to `CHECK_MODEL` for repair instead of being regenerated. A sample of each rejected output is logged to help trace the cause.
- Detailed recaps with code fences, leaked drafts or output cut off by the length limit are rejected in favor of the backup models, and `CHECK_MODEL` marks lines that do not belong to the recap so the bot deletes them without rewriting the recap text or its source markers.
- The model info label "Check" is now "校對模型", and model names keep their configured reasoning-effort suffix (for example `gpt-6.1-sol-xhigh`) instead of showing only the name the gateway reports.
- Chat completions are now received as streams, so long generations are no longer cut off by Cloudflare's read timeout.
- Rich messages are now split by UTF-8 bytes, fixing CJK recaps whose tail Telegram silently dropped.
- Links and custom emoji inside media captions are now processed too.
- Hardening: images whose header claims more than 16,777,216 pixels are not decoded, all parts of an Office file share one decompression budget, Bot API errors no longer carry the URL that holds the bot token, and media review logs include description text only when `OPENAI_VERBOSE_PAYLOAD_LOGS` is on.
- Development: the golangci-lint config is migrated to v2, and `golang.org/x/image` v0.19.0 is added to decode WebP stickers.

## v1.0.0 — 2026-07-14

### 正體中文

- 聊天回顧全面改用 Telegram 原生 Rich Message 傳送，取代原本的 Telegraph 頁面。傳送流程建立在 Telegram Bot API 10.1（2026-06-11）新增的 `sendRichMessage` 系列介面之上，支援標題、表格、清單、引用區塊等排版元素，Telegram Desktop 6.9 系列起即可完整顯示。
- 濃縮總結與詳細總結合併在同一則訊息內，濃縮總結永遠直接可見；超長回顧會自動分段傳送，後續段落以回覆第一則訊息的方式串接。
- 回顧內容附上 `[1](https://t.me/c/...)` 形式的引用連結，點擊即可跳回原始訊息，訊息尾端並附上模型執行紀錄（model execution trace）。
- 純文字備援改為受控觸發：僅在 Telegram 回傳與排版格式相關的 HTTP 400 錯誤時才改用純文字重送，其餘錯誤照常回報。
- 完整移除 Telegraph 整合（`internal/services/telegraph`、`internal/thirdparty/telegraph`）。
- 新增吐槽（sarcastic）風格的濃縮總結，可透過 `SARCASTIC_CONDENSED_MODEL_NAME` 指定專用模型，並支援以逗號分隔的備援模型鏈（`SARCASTIC_CONDENSED_MODEL_NAME_backup`、`OPENAI_API_MODEL_NAME_backup`）。
- 濃縮總結改為「一句話銳評」風格：以歇後語式的韻律把全部話題大雜燴串成一句小故事，可帶粗體與斜體，取代原本多行清單式的濃縮版面。
- 新增 `CHECK_MODEL` 條件式修復模型（含 `CHECK_MODEL_backup` 備援鏈），只在濃縮總結的 Rich Markdown 驗證失敗時才會啟動，正常情況不會多花任何模型呼叫。
- 新增 `CHAT_HISTORIES_SUMMARIZATION_LANGUAGE` 設定，可指定摘要的輸出語言。
- 強化 Rich Message 傳送路徑的穩定性：訊息長度改以 UTF-16 code unit 計算並加入「message is too long」錯誤的備援處理；濃縮總結的備援截斷邏輯集中管理，確保不會截斷連結或標記符號；回顧中的 `@username` 會經過中和處理，避免誤觸提及通知；Markdown 分割與跳脫輔助函式改為跨套件共用；傳送失敗時會清理回顧的佔位訊息。
- 自動回顧訊息新增釘選選項；`/smr` 失敗時提供重試按鈕（同步自 upstream）。
- 測試資料已清除個人顯示名稱；`.gitignore` 新增 AI 工具目錄（`.agent`、`.codex`、`.gemini`、`.agents`、`openspec`）。
- docker-compose 的 PostgreSQL 映像檔升級為 `postgres:17-alpine`。

### English

- Chat recaps are now delivered as native Telegram Rich Messages, replacing Telegraph pages entirely. Delivery is built on the `sendRichMessage` family of endpoints introduced in Telegram Bot API 10.1 (2026-06-11), with headings, tables, lists, blockquotes and other layout elements, rendered natively by the Telegram Desktop 6.9 series and later.
- The condensed and detailed summaries now live in a single message, with the condensed summary always visible up front; very long recaps are split into multiple parts, and each follow-up part replies to the first message to keep the thread together.
- Recaps carry citation links in the form `[1](https://t.me/c/...)` that jump back to the original messages, and every recap ends with a model execution trace footer.
- Plain-text fallback is now tightly controlled: it triggers only on Telegram formatting-specific HTTP 400 errors, while all other errors surface as usual.
- The entire Telegraph integration has been removed (`internal/services/telegraph`, `internal/thirdparty/telegraph`).
- Sarcastic condensed summaries can run on a dedicated model via `SARCASTIC_CONDENSED_MODEL_NAME`, with comma-separated backup model chains (`SARCASTIC_CONDENSED_MODEL_NAME_backup`, `OPENAI_API_MODEL_NAME_backup`).
- The condensed summary is now a single-sentence roast: one rhythmic, meme-flavored line that strings every topic into a little story (inline bold/italic allowed), replacing the previous multi-line list layout.
- A new `CHECK_MODEL` conditional repair model (with a `CHECK_MODEL_backup` chain) runs only when the condensed Rich Markdown fails validation, so no extra model calls happen on the happy path.
- The new `CHAT_HISTORIES_SUMMARIZATION_LANGUAGE` setting controls the output language of generated summaries.
- The rich message delivery path has been hardened: message lengths are counted in UTF-16 code units with a dedicated fallback for "message is too long" errors; condensed-summary fallback truncation is centralized and safe around links and markers; `@username` mentions inside recaps are neutralized so they no longer ping users; markdown split/escape helpers are now shared between packages; and recap placeholder messages are cleaned up when sending fails.
- Auto-recap messages can now optionally be pinned, and `/smr` shows a retry button when summarization fails (synced from upstream).
- Test fixtures were scrubbed of personal display names, and `.gitignore` now covers AI tool directories (`.agent`, `.codex`, `.gemini`, `.agents`, `openspec`).
- The PostgreSQL image in docker-compose was bumped to `postgres:17-alpine`.
