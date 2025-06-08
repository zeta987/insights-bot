# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**insights-bot** is a multi-platform chatbot (Telegram, Slack, Discord) that leverages OpenAI GPT models to provide intelligent insights through web page summarization and chat history recaps.

## Common Development Commands

### Build and Run
```bash
# Build binary
go build -a -o "build/insights-bot" "github.com/nekomeowww/insights-bot/cmd/insights-bot"

# Run with Docker Compose (from Docker Hub)
docker compose --profile hub up -d

# Run with Docker Compose (local build)
docker compose --profile local up -d --build

# Build multi-arch Docker image
docker buildx build --platform linux/arm64,linux/amd64 -t <tag> -f Dockerfile .
```

### Testing and Linting
```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/models/chathistories/...

# Run linter
golangci-lint run

# Run linter with auto-fix
golangci-lint run --fix
```

### Database Operations
```bash
# Generate Ent code after schema changes
go generate ./ent

# Apply database migrations (handled automatically on startup)
# Migrations are managed by Ent framework
```

## Architecture and Key Design Patterns

### Dependency Injection Framework
The project uses **Uber FX** for dependency injection. The entire application assembly happens in `cmd/insights-bot/main.go`. When adding new services or components:
1. Create a provider function that returns your component
2. Add it to the appropriate module in the FX app
3. Let FX handle the dependency graph

### Core Modules Structure
- **`internal/bots/`**: Platform-specific bot implementations (Telegram, Slack, Discord)
- **`internal/services/`**: Core business logic services (SMR, Auto Recap, etc.)
- **`internal/models/`**: Data models and business logic
- **`internal/datastore/`**: Data storage layer (PostgreSQL via Ent, Redis, Pinecone)
- **`pkg/bots/tgbot/`**: Reusable Telegram bot framework

### Handler Pattern
Each bot platform uses a handler pattern for processing commands:
- Commands are registered in the bot's main file
- Each command has its own handler in the `handlers/` directory
- Handlers can use middleware for common functionality (e.g., message recording)

### Service Communication Flow
1. **Webhook Entry** → Bot receives message via webhook endpoint
2. **Middleware Processing** → Message recording, rate limiting
3. **Command Routing** → Dispatcher routes to appropriate handler
4. **Business Logic** → Handler calls service layer (e.g., SMR service)
5. **External APIs** → Services interact with OpenAI, Telegraph, etc.
6. **Data Persistence** → Results stored in PostgreSQL/Redis
7. **Response** → Formatted response sent back to user

### Key Services
- **SMR Service** (`internal/services/smr/`): Handles web page summarization with queue processing
- **Auto Recap Service** (`internal/services/autorecap/`): Manages scheduled chat recaps
- **Telegraph Service** (`internal/services/telegraph/`): Creates web pages for long summaries

### Database Schema Management
The project uses **Ent** ORM. When modifying database schemas:
1. Edit schema files in `ent/schema/`
2. Run `go generate ./ent` to regenerate code
3. Migrations are handled automatically on startup

## Environment Configuration

### Essential Environment Variables
```bash
# Bot tokens
TELEGRAM_BOT_TOKEN=
SLACK_CLIENT_ID=
SLACK_CLIENT_SECRET=
DISCORD_BOT_TOKEN=

# OpenAI
OPENAI_API_SECRET=
OPENAI_API_MODEL_NAME=gpt-4o  # or gpt-4.1
OPENAI_API_HOST=https://api.openai.com  # can be customized

# Database
DB_CONNECTION_STR=postgres://user:pass@localhost:5432/insights_bot
REDIS_HOST=localhost
REDIS_PORT=6379

# Feature flags
FEATURE_SUMMARY_GENERATION_VIA_CHAT=true
SARCASTIC_CONDENSED_MODEL_NAME=  # Optional: for sarcastic mode
```

### Service Ports
- `6060`: pprof debugging server
- `7069`: Health check server
- `7070`: Slack webhook server
- `7071`: Telegram webhook server
- `7072`: Discord webhook server

## Development Tips

### Adding New Commands
1. Create handler file in `internal/bots/{platform}/handlers/`
2. Implement the handler interface
3. Register command in bot's command list
4. Add i18n strings to `locales/` files

### Working with Ent ORM
- Schema changes require running `go generate ./ent`
- Use transactions for multi-entity operations
- Leverage Ent's type-safe query builder

### Testing Strategies
- Unit tests should mock external dependencies (OpenAI, Telegraph)
- Use `internal/thirdparty/openai/openaimock/` for OpenAI mocking
- Integration tests can use Docker Compose with test profile

### Debugging
- Enable pprof server for performance profiling
- Use structured logging with appropriate log levels
- Check health endpoint at `:7069/` for service status