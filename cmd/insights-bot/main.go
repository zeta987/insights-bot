package main

import (
	"context"
	"log"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/fx"

	"github.com/nekomeowww/insights-bot/internal/bots/discord"
	"github.com/nekomeowww/insights-bot/internal/bots/slack"
	"github.com/nekomeowww/insights-bot/internal/bots/telegram"
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/lib"
	"github.com/nekomeowww/insights-bot/internal/models"
	"github.com/nekomeowww/insights-bot/internal/services"
	"github.com/nekomeowww/insights-bot/internal/services/autorecap"
	"github.com/nekomeowww/insights-bot/internal/services/health"
	"github.com/nekomeowww/insights-bot/internal/services/pprof"
	"github.com/nekomeowww/insights-bot/internal/services/smr"
	"github.com/nekomeowww/insights-bot/internal/thirdparty"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, continuing with system environment variables.")
	}

	app := fx.New(fx.Options(
		fx.Provide(configs.NewConfig()),
		fx.Options(lib.NewModules()),
		fx.Options(datastore.NewModules()),
		fx.Options(models.NewModules()),
		fx.Options(thirdparty.NewModules()),
		fx.Options(services.NewModules()),
		fx.Options(telegram.NewModules()),
		fx.Options(slack.NewModules()),
		fx.Options(discord.NewModules()),
		fx.Invoke(health.Run()),
		fx.Invoke(pprof.Run()),
		fx.Invoke(autorecap.Run()),
		fx.Invoke(slack.Run()),
		fx.Invoke(telegram.Run()),
		fx.Invoke(discord.Run()),
		fx.Invoke(smr.Run()),
		fx.Invoke(func(config *configs.Config) {
			openai.SetSarcasticCondensedSystemPrompt(config.SarcasticCondensedSystemPrompt)
			openai.SetSarcasticCondensedUserPrompt(config.SarcasticCondensedUserPrompt)
		}),
	))

	app.Run()

	stopCtx, stopCtxCancel := context.WithTimeout(context.Background(), time.Second*15)
	defer stopCtxCancel()

	if err := app.Stop(stopCtx); err != nil {
		log.Fatal(err)
	}
}
