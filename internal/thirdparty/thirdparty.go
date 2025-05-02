package thirdparty

import (
	"go.uber.org/fx"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/telegraph"
)

func NewModules() fx.Option {
	return fx.Options(
		fx.Provide(openai.NewClient(true)),
		telegraph.Module,
	)
}
