package config

import (
	"fmt"
	"strings"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/hlog"
)

func NewHertzHandler(serverConfig ServerConfig, logConfig LogConfig) *server.Hertz {
	hlog.SetLevel(hertzLogLevel(logConfig.Level))

	HertzOptions := config.Option{
		F: func(o *config.Options) {
			o.Addr = fmt.Sprintf("%s:%s", serverConfig.Listen, serverConfig.Port.String())
		},
	}
	return server.Default(HertzOptions)
}

func hertzLogLevel(level string) hlog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return hlog.LevelTrace
	case "debug":
		return hlog.LevelDebug
	case "notice":
		return hlog.LevelNotice
	case "warn", "warning":
		return hlog.LevelWarn
	case "error":
		return hlog.LevelError
	case "fatal":
		return hlog.LevelFatal
	case "info", "":
		return hlog.LevelInfo
	default:
		return hlog.LevelInfo
	}
}
