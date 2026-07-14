<p align="center">
  <image src="./docs/images/icon.png" width="200px" height="200px" />
</p>

<h1 align="center">insights-bot</h1>

<p align="center">
  <img src="https://github.com/nekomeowww/insights-bot/actions/workflows/ci.yml/badge.svg">
  <img src="https://github.com/nekomeowww/insights-bot/actions/workflows/build.yml/badge.svg" />
  <img src="https://github.com/nekomeowww/insights-bot/actions/workflows/release-build.yml/badge.svg" />
  <a href="https://goreportcard.com/badge/github.com/nekomeowww/insights-bot"><img src="https://goreportcard.com/badge/github.com/nekomeowww/insights-bot" /></a>
  <a href="https://hub.docker.com/r/nekomeowww/insights-bot">
    <img src="https://img.shields.io/docker/pulls/nekomeowww/insights-bot" />
  </a>
  <a href="https://hub.docker.com/r/nekomeowww/insights-bot">
    <img src="https://img.shields.io/docker/v/nekomeowww/insights-bot" />
  </a>
</p>

<p align="center">
  <a href="https://t.me/ayaka_insights_bot_group">
    <img src="https://img.shields.io/badge/Chat%20on-Telegram-%235AA9E6?logo=telegram" />
  </a>
  <a href="https://slack.com/oauth/v2/authorize?client_id=2628877438886.5144808095409&scope=chat:write,commands&user_scope=">
    <img src="https://img.shields.io/badge/Add_to_Slack-4A154B?logo=slack" />
  </a>
</p>

**正體中文** | [简体中文](./README.zh-CN.md) | [English](./README.md)

一個透過 OpenAI GPT 模型為你的資訊流提供洞察的機器人。

---

## 支援的聊天平台

- Telegram
- Slack
- Discord

---

## 版本釋出

此 fork（[zeta987/insights-bot](https://github.com/zeta987/insights-bot)）的版本一覽如下，完整內容請見 [CHANGELOG.md](./CHANGELOG.md)。

- **v1.0.0**（2026-07-14）— 首個 fork 正式版本：聊天記錄回顧改以 Telegram 原生 Rich Message 傳送（採用 Telegram Bot API 10.1（2026-06-11）新增的 `sendRichMessage` 系列介面，Telegram Desktop 6.9 系列起可完整顯示）並移除 Telegraph 整合，同時加入吐槽風格濃縮總結、備援模型鏈、`CHECK_MODEL` 條件式修復與摘要輸出語言設定等功能。

---

## 使用方法

### 指令

Insights Bot 內建了一系列指令，在 Telegram 中與機器人對話時，可以使用 `/help` 取得可用的指令清單。
你也可以使用 `/cancel` 取消任何與機器人之間正在進行的操作。

#### 網頁摘要

指令：`/smr`

參數：URL，或回覆一則只包含 URL 的訊息

用法：

```txt
/smr https://www.example.com
```

```txt
/smr [回覆一則只包含 URL 的訊息]
```

透過傳送附帶 URL 的 `/smr` 指令，或回覆一則只包含 URL 的訊息，機器人會嘗試摘要該網頁並回傳結果。

#### 設定聊天記錄回顧

> **Warning**
> **此指令目前無法在 Slack/Discord 整合中使用。**

指令：`/configure_recap`

參數：無

```txt
/configure_recap
```

透過傳送 `/configure_recap` 指令，機器人會傳送一則附有可互動選項的訊息，點選按鈕即可選擇想要設定的項目。

#### 聊天記錄回顧

> **Warning**
> **此指令目前無法在 Slack/Discord 整合中使用。**

指令：`/recap`

參數：無

```txt
/recap
```

透過傳送 `/recap` 指令，機器人會嘗試摘要聊天記錄，並依你稍後選擇的選項回傳結果。

#### 訂閱群組的聊天記錄回顧

> **Warning**
> **此指令目前無法在 Slack/Discord 整合中使用。**

指令：`/subscribe_recap`

參數：無

```txt
/subscribe_recap
```

透過傳送 `/subscribe_recap` 指令，機器人會開始擷取你所訂閱群組的訊息，並在回顧訊息產生後，透過私訊傳送一份回顧訊息的副本給你。

#### 取消訂閱群組的聊天記錄回顧

> **Warning**
> **此指令目前無法在 Slack/Discord 整合中使用。**

指令：`/unsubscribe_recap`

參數：無

```txt
/unsubscribe_recap
```

透過傳送 `/unsubscribe_recap` 指令，機器人將不再為你訂閱的群組傳送回顧訊息的副本。

#### 摘要私訊中的轉傳訊息

> **Warning**
> **此指令目前無法在 Slack/Discord 整合中使用。**

指令：`/recap_forwarded_start`、`/recap_forwarded`

參數：無

```txt
/recap_forwarded_start
```

```txt
<轉傳訊息>
```

```txt
/recap_forwarded
```

透過傳送 `/recap_forwarded_start` 指令，機器人會開始擷取你之後在私訊中傳送的轉傳訊息，並在你隨後傳送 `/recap_forwarded` 指令時嘗試摘要這些訊息。

## 部署

### 使用二進位檔執行

你需要先將這個儲存庫 clone 下來，然後自行建置二進位檔。

```shell
git clone https://github.com/nekomeowww/insights-bot
```

```shell
go build -a -o "build/insights-bot" "github.com/nekomeowww/insights-bot/cmd/insights-bot"
```

接著將 `.env.example` 檔案複製到 `build` 目錄並重新命名為 `.env`，然後填入環境變數。

```shell
cd build
cp ../.env.example .env
vim .env
```

```shell
# 賦予二進位檔可執行權限
$ chmod +x ./insights-bot
# 執行二進位檔
$ ./insights-bot
```

### 使用 Docker 執行

```shell
docker run -it --rm -e TELEGRAM_BOT_TOKEN=<Telegram Bot API 權杖> -e OPENAI_API_SECRET=<OpenAI API 金鑰> -e DB_CONNECTION_STR="<PostgreSQL 連線 URL>" insights-bot ghcr.io/nekomeowww/insights-bot:latest
```

### 使用 Docker Compose 執行

Clone 這個專案：

```shell
git clone github.com/nekomeowww/insights-bot
```

或者只複製或下載必要的 `.env.example` 與 `docker-compose.yml` 檔案（但這樣只能使用預先打包好的 Docker 映像檔來執行機器人）：

```shell
curl -O https://raw.githubusercontent.com/nekomeowww/insights-bot/main/.env.example
curl -O https://raw.githubusercontent.com/nekomeowww/insights-bot/main/docker-compose.yml
```

複製 `.env.example` 檔案的內容來建立你的 `.env` 檔案。`.env` 檔案應放在專案根目錄，與 `docker-compose.yml` 檔案同一層。

```shell
cp .env.example .env
```

在 `.env` 中填入你的 OpenAI 金鑰與其他環境變數，然後執行：

```shell
docker compose --profile hub up -d
```

如果你偏好從本機程式碼建置並執行 Docker 映像檔（也就是手動建置，需要這個專案的完整原始碼），則執行：

```shell
docker compose --profile local up -d --build
```

### 自行建置

#### 使用 Go 建置

```shell
go build -a -o "release/insights-bot" "github.com/nekomeowww/insights-bot/cmd/insights-bot"
```

#### 使用 Docker 建置

```shell
docker buildx build --platform linux/arm64,linux/amd64 -t <tag> -f Dockerfile .
```

## 使用的連接埠

| 連接埠 | 說明 |
|------|-------------|
| 6060 | pprof 除錯伺服器 |
| 7069 | 健康檢查伺服器 |
| 7070 | Slack App/Bot Webhook 伺服器 |
| 7071 | Telegram Bot Webhook 伺服器 |
| 7072 | Discord Bot Webhook 伺服器 |

## 設定

### 環境變數

| 名稱                                          | 必填 | 預設值                                                                                  | 說明                                                                                                                                                                                                                                                                                                                                                                     |
| --------------------------------------------- | -------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TIMEZONE_SHIFT_SECONDS`                      | `false`  | `0`                                                                                      | 自動為群組產生聊天記錄回顧訊息時所使用的時區偏移量（以秒為單位），預設為 0。                                                                                                                                                                                                                                                                                                 |
| `TELEGRAM_BOT_TOKEN`                          | `true`   |                                                                                          | Telegram Bot API 權杖，可以透過 [@BotFather](https://t.me/BotFather) 建立並取得。                                                                                                                                                                                                                                                                                          |
| `TELEGRAM_BOT_WEBHOOK_URL`                    | `false`  |                                                                                          | Telegram Bot Webhook 的 URL 與連接埠，可以使用 [https://ngrok.com/](https://ngrok.com/) 或 Cloudflare Tunnel 將本機伺服器公開到網際網路。                                                                                                                                                                                                                                    |
| `TELEGRAM_BOT_WEBHOOK_PORT`                   | `false`  | `7071`                                                                                   | Telegram Bot Webhook 伺服器連接埠，預設為 7071。                                                                                                                                                                                                                                                                                                                            |
| `OPENAI_API_SECRET`                           | `true`   |                                                                                          | OpenAI API 金鑰，格式類似 `sk-************************************************`，可以登入 OpenAI 平台後在 [http://platform.openai.com/account/api-keys](http://platform.openai.com/account/api-keys) 建立一個。                                                                                                                                                              |
| `OPENAI_API_HOST`                             | `false`  | `https://api.openai.com`                                                                 | OpenAI API 主機位址，如果設定了中繼或反向代理，可以在此指定，例如 `https://openai.example.workers.dev`。                                                                                                                                                                                                                                                                      |
| `OPENAI_API_MODEL_NAME`                       | `false`  | `gpt-3.5-turbo`                                                                          | OpenAI API 模型名稱，預設為 `gpt-3.5-turbo`，如果想使用其他模型（例如 `gpt-4`）可以在此指定。                                                                                                                                                                                                                                                                                 |
| `OPENAI_API_MODEL_NAME_backup`                | `false`  |                                                                                          | 產生詳細總結時使用的備援模型。支援以逗號分隔多個模型名稱，當主要模型失敗或回傳空白內容時會依序重試。                                                                                                                                                                                                                                                                            |
| `OPENAI_API_TOKEN_LIMIT`                      | `false`  | `4096`                                                                                   | OpenAI API 的 Token 上限，用於在呼叫 Chat Completion API 之前計算文字的切分與截斷，通常設定為模型的最大 Token 上限，再交由 insights-bot 決定如何處理，預設為 `4096`。                                                                                                                                                                                                            |
| `OPENAI_API_CHAT_HISTORIES_RECAP_TOKEN_LIMIT` | `false`  | `2000`                                                                                   | OpenAI 聊天記錄回顧的 Token 上限，即產生並回覆的聊天記錄回顧訊息的 Token 長度，預設為 2000，這會為實際的聊天上下文保留 OPENAI_API_TOKEN_LIMIT - 2000 個 Token。                                                                                                                                                                                                                 |
| `CHECK_MODEL`                                 | `false`  |                                                                                          | 選用的條件式修復模型。只有在濃縮總結輸出無效時才會執行，例如輸出成 JSON、被程式碼圍欄包住、帶有標題或清單符號，或不是單行的一句話，並將其改寫為可直接顯示的一句話銳評；輸出有效時會跳過此模型。                                                                                                                                                                                     |
| `CHECK_MODEL_backup`                          | `false`  |                                                                                          | `CHECK_MODEL` 的備援模型。支援以逗號分隔多個模型名稱，當主要 Check 模型失敗或仍回傳無效的 Rich Markdown 時會依序重試。                                                                                                                                                                                                                                                          |
| `OPENAI_FORCE_CHECK_MODEL_FAILURE`            | `false`  | `0`                                                                                      | 測試用開關。在條件式修復期間強制主要 Check 模型嘗試失敗，用來驗證能否順利切換到 `CHECK_MODEL_backup`。                                                                                                                                                                                                                                                                          |
| `OPENAI_FORCE_CONDENSED_PRIMARY_FAILURE_FOR_TEST` | `false`  | `0`                                                                                      | 測試用開關。強制 `SARCASTIC_CONDENSED_MODEL_NAME` 失敗一次，並切換到 `SARCASTIC_CONDENSED_MODEL_NAME_backup`。                                                                                                                                                                                                                                                                |
| `OPENAI_VERBOSE_PAYLOAD_LOGS`                 | `false`  | `0`                                                                                      | 除錯用開關。記錄回顧 / Check 模型流程完整的 OpenAI 請求與回應內容。由於日誌內容敏感且量大，建議只在排查問題時開啟。                                                                                                                                                                                                                                                              |
| `SARCASTIC_CONDENSED_SYSTEM_PROMPT`           | `false`  |                                                                                          | 濃縮總結的自訂系統提示詞，應要求模型輸出 Telegram Rich Markdown 片段；未設定時使用程式內建的結構化 Rich Markdown 提示詞。                                                                                                                                                                                                                                                        |
| `SARCASTIC_CONDENSED_USER_PROMPT`             | `false`  |                                                                                          | 濃縮總結的自訂使用者提示詞範本，必須包含 `{{ .ChatHistory }}` 佔位符，且應只要求輸出最終的 Rich Markdown 片段。                                                                                                                                                                                                                                                                 |
| `SARCASTIC_CONDENSED_MODEL_NAME`              | `false`  |                                                                                          | 專門用於產生吐槽風格濃縮總結的 OpenAI 模型名稱。未設定時使用與 `OPENAI_API_MODEL_NAME` 相同的模型。這讓你可以為吐槽風格摘要使用不同的模型（例如 `gpt-4`），同時讓其他作業使用較便宜的模型。                                                                                                                                                                                        |
| `SARCASTIC_CONDENSED_MODEL_NAME_backup`       | `false`  |                                                                                          | 當 `SARCASTIC_CONDENSED_MODEL_NAME` 失敗、回傳空白內容或產生無效的濃縮 Rich Markdown 時使用的備援模型。支援以逗號分隔多個模型名稱並依序重試。                                                                                                                                                                                                                                     |
| `CHAT_HISTORIES_SUMMARIZATION_LANGUAGE`       | `false`  | `Simplified Chinese`                                                                     | 聊天記錄摘要輸出所使用的語言，控制提示詞範本中 `{{ .Language }}` 使用的語言。例如：`English`、`Traditional Chinese`、`Japanese` 等。未設定時預設為 `Simplified Chinese`。                                                                                                                                                                                                        |
| `DB_CONNECTION_STR`                           | `true`   | `postgresql://postgres:123456@db_local:5432/postgres?search_path=public&sslmode=disable` | PostgreSQL 資料庫連線 URL，例如 `postgres://postgres:postgres@localhost:5432/postgres`。如果需要指定 schema，可以在結尾加上 `?search_path=<schema name>`。                                                                                                                                                                                                                     |
| `SLACK_CLIENT_ID`                             | `false`  |                                                                                          | Slack App 的 client id，可以建立一個 Slack App 後取得，請參考[教學](https://api.slack.com/tutorials/slack-apps-and-postman)。                                                                                                                                                                                                                                                  |
| `SLACK_CLIENT_SECRET`                         | `false`  |                                                                                          | Slack App 的 client secret，可以建立一個 Slack App 後取得，請參考[教學](https://api.slack.com/tutorials/slack-apps-and-postman)。                                                                                                                                                                                                                                              |
| `SLACK_WEBHOOK_PORT`                          | `false`  | `7070`                                                                                   | Slack Bot/App Webhook 伺服器連接埠，預設為 7070。                                                                                                                                                                                                                                                                                                                            |
| `DISCORD_BOT_TOKEN`                           | `false`  |                                                                                          | Discord Bot 權杖，可以建立一個 Discord App 後取得，請參考[入門文件](https://discord.com/developers/docs/getting-started)。                                                                                                                                                                                                                                                     |
| `DISCORD_BOT_PUBLIC_KEY`                      | `false`  |                                                                                          | Discord Bot 公鑰，可以建立一個 Discord App 後取得，請參考[入門文件](https://discord.com/developers/docs/getting-started)；當提供 `DISCORD_BOT_TOKEN` 時為必填。                                                                                                                                                                                                                 |
| `DISCORD_BOT_WEBHOOK_PORT`                    | `false`  | `7072`                                                                                   | Discord Bot Webhook 伺服器連接埠，預設為 7072。                                                                                                                                                                                                                                                                                                                              |
| `REDIS_HOST`                                  | `true`   | `localhost`                                                                              | 要連線的 Redis 主機，預設為 `localhost`。                                                                                                                                                                                                                                                                                                                                    |
| `REDIS_PORT`                                  | `true`   | `6379`                                                                                   | Redis 連接埠，預設為 `6379`。                                                                                                                                                                                                                                                                                                                                                |
| `REDIS_TLS_ENABLED`                           | `false`  | `false`                                                                                  | 是否啟用 Redis TLS 連線，預設為 `false`。                                                                                                                                                                                                                                                                                                                                     |
| `REDIS_USERNAME`                              | `false`  |                                                                                          | Redis 使用者名稱。                                                                                                                                                                                                                                                                                                                                                           |
| `REDIS_PASSWORD`                              | `false`  |                                                                                          | Redis 密碼。                                                                                                                                                                                                                                                                                                                                                                 |
| `REDIS_DB`                                    | `false`  | `0`                                                                                      | Redis 資料庫編號，預設為 `0`。                                                                                                                                                                                                                                                                                                                                                |
| `REDIS_CLIENT_CACHE_ENABLED`                  | `false`  | `false`                                                                                  | 是否啟用 Redis 用戶端快取，預設為 `false`，關於用戶端快取的更多細節可以參考 [https://redis.io/docs/manual/client-side-caching/](https://redis.io/docs/manual/client-side-caching/) 與 [https://github.com/redis/rueidis#client-side-caching](https://github.com/redis/rueidis#client-side-caching)。                                                                             |
| `LOG_FILE_PATH`                               | `false`  | `<insights-bot_executable>/logs/insights-bot.log`                                        | 日誌檔案路徑，如果以二進位檔執行並想指定日誌儲存位置，可以在此設定。Docker 卷中的預設路徑為 `/var/log/insights-bot/insights-bot.log`，你可以在執行 `docker run` 指令時以 `-e LOG_FILE_PATH=<path>` 覆寫預設值，或修改 `docker-compose.yml` 檔案並加入新的 `LOG_FILE_PATH`。                                                                                                        |
| `LOG_LEVEL`                                   | `false`  | `info`                                                                                   | 日誌等級，可用值為 `debug`、`info`、`warn`、`error`。                                                                                                                                                                                                                                                                                                                          |
| `LOCALES_DIR`                                 | `false`  | `locales`                                                                                | 語系檔目錄，預設為 `locales`，建議設定為絕對路徑。                                                                                                                                                                                                                                                                                                                             |

## 誌謝

- 專案圖示由 [Midjourney](https://www.midjourney.com/app/jobs/ff3e9b42-181b-4181-a9ae-6777f957835d/) 產生
- 感謝 [OpenAI](https://openai.com/) 提供 GPT 系列模型
