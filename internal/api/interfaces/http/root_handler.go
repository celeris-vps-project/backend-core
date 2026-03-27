package api

import (
	apiConfig "backend-core/internal/api/config"
	"context"
	"fmt"
	"github.com/cloudwego/hertz/pkg/app"
	"log"
)

type RootHandler struct {
	config apiConfig.ServerConfig
}

func NewRootHandler(config apiConfig.ServerConfig) *RootHandler {
	return &RootHandler{
		config: config,
	}
}

func (h RootHandler) Handle(ctx context.Context, c *app.RequestContext) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_html := fmt.Sprintf(`
	<html>
	<head><title>Celeris Service</title></head>
	<body>
		<h2>%s Celeris Service</h2>
		<p>This is an api gateway for Celeris</p>
		<p>Official: <a href="%[2]s">%[2]s</a></p>
	</body>
	</html>
`, h.config.Name, h.config.Domain)
	_, err := c.WriteString(_html)
	if err != nil {
		log.Fatalf("[rootHandler.handle] raise error when init handler: %s", err.Error())
		return
	}
}
