package accesslog

import (
	"context"
	"log"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

func Middleware() app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		start := time.Now()
		ctx.Next(c)

		path := ctx.FullPath()
		if path == "" {
			path = string(ctx.Request.URI().Path())
		}

		log.Printf("[access] %3d | %12s | %-7s %s | %s",
			ctx.Response.StatusCode(),
			time.Since(start).Round(time.Microsecond),
			string(ctx.Method()),
			path,
			ctx.ClientIP(),
		)
	}
}
