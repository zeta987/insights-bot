# Changelog / 更新日誌

## v0.1.0 — 2026-07-14

### 正體中文

- 聊天回顧全面改用 Telegram 原生 Rich Message 傳送，取代原本的 Telegraph 頁面。傳送流程建立在 Telegram Bot API 10.1（2026-06-11）新增的 `sendRichMessage` 系列介面之上，支援標題、表格、清單、引用區塊等排版元素，Telegram Desktop 6.9 系列起即可完整顯示。
- 濃縮總結與詳細總結合併在同一則訊息內，濃縮總結永遠直接可見；超長回顧會自動分段傳送，後續段落以回覆第一則訊息的方式串接。
- 回顧內容附上 `[1](https://t.me/c/...)` 形式的引用連結，點擊即可跳回原始訊息，訊息尾端並附上模型執行紀錄（model execution trace）。
- 純文字備援改為受控觸發：僅在 Telegram 回傳與排版格式相關的 HTTP 400 錯誤時才改用純文字重送，其餘錯誤照常回報。
- 完整移除 Telegraph 整合（`internal/services/telegraph`、`internal/thirdparty/telegraph`）。
- 新增吐槽（sarcastic）風格的濃縮總結，可透過 `SARCASTIC_CONDENSED_MODEL_NAME` 指定專用模型，並支援以逗號分隔的備援模型鏈（`SARCASTIC_CONDENSED_MODEL_NAME_backup`、`OPENAI_API_MODEL_NAME_backup`）。
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
- A new `CHECK_MODEL` conditional repair model (with a `CHECK_MODEL_backup` chain) runs only when the condensed Rich Markdown fails validation, so no extra model calls happen on the happy path.
- The new `CHAT_HISTORIES_SUMMARIZATION_LANGUAGE` setting controls the output language of generated summaries.
- The rich message delivery path has been hardened: message lengths are counted in UTF-16 code units with a dedicated fallback for "message is too long" errors; condensed-summary fallback truncation is centralized and safe around links and markers; `@username` mentions inside recaps are neutralized so they no longer ping users; markdown split/escape helpers are now shared between packages; and recap placeholder messages are cleaned up when sending fails.
- Auto-recap messages can now optionally be pinned, and `/smr` shows a retry button when summarization fails (synced from upstream).
- Test fixtures were scrubbed of personal display names, and `.gitignore` now covers AI tool directories (`.agent`, `.codex`, `.gemini`, `.agents`, `openspec`).
- The PostgreSQL image in docker-compose was bumped to `postgres:17-alpine`.
