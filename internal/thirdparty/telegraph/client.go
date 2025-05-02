package telegraph

import (
	"net/http"
	"strings"
	"time"

	"github.com/celestix/telegraph-go/v2"
	"go.uber.org/fx"

	"github.com/nekomeowww/insights-bot/internal/configs"
)

var Module = fx.Provide(NewClient)

func NewClient(cfg *configs.Config) *telegraph.TelegraphClient {
	opt := &telegraph.ClientOpt{
		ApiUrl: cfg.Telegraph.ApiUrl,
		HttpClient: &http.Client{
			Timeout: time.Duration(cfg.Telegraph.TimeoutSec) * time.Second,
		},
	}
	if opt.ApiUrl == "" {
		opt.ApiUrl = "https://api.telegra.ph/"
	}
	if !strings.HasSuffix(opt.ApiUrl, "/") {
		opt.ApiUrl += "/"
	}
	return telegraph.GetTelegraphClient(opt)
}
