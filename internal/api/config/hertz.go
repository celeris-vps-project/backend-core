package config

import (
	"fmt"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
)

func NewHertzHandler(serverConfig ServerConfig) *server.Hertz {
	HertzOptions := config.Option{
		F: func(o *config.Options) {
			o.Addr = fmt.Sprintf("%s:%s", serverConfig.Listen, serverConfig.Port.String())
		},
	}
	return server.Default(HertzOptions)
}
