package seckill

import (
	"context"
	"errors"
	"net/http"

	hertzApp "github.com/cloudwego/hertz/pkg/app"
)

// ─────────────────────────────────────────────────────────────────────────────
// Hertz HTTP adapter — turn an Engine into a one-liner route handler
// ─────────────────────────────────────────────────────────────────────────────

// BindFunc extracts and validates a typed request from the Hertz context.
// This is the "glue" between HTTP and the generic engine.
//
// Example:
//
//	func(c context.Context, ctx *hertzApp.RequestContext) (MyReq, error) {
//	    var req MyReq
//	    if err := ctx.BindJSON(&req); err != nil {
//	        return req, err
//	    }
//	    uid, _ := authn.UserID(ctx)
//	    req.CustomerID = uid.String()
//	    return req, nil
//	}
type BindFunc[Req any] func(c context.Context, ctx *hertzApp.RequestContext) (Req, error)

// RenderFunc customizes how a successful response is written to the client.
// If nil, HertzHandler uses a default JSON renderer with HTTP 200.
type RenderFunc[Res any] func(c context.Context, ctx *hertzApp.RequestContext, res Res)

// HertzHandler creates a Hertz handler function from an Engine + bind function.
// This is the primary integration point — one line to mount a seckill endpoint:
//
//	r.POST("/seckill/product", seckill.HertzHandler(engine, bindFn))
//
// HTTP status mapping for pipeline errors:
//
//	ErrGateFull  → 503 Service Unavailable
//	ErrDuplicate → 409 Conflict
//	ErrSoldOut   → 410 Gone
//	other error  → 500 Internal Server Error
//	success      → 200 OK (or custom via WithRender)
func HertzHandler[Req any, Res any](
	engine *Engine[Req, Res],
	bind BindFunc[Req],
	renders ...RenderFunc[Res],
) hertzApp.HandlerFunc {
	var render RenderFunc[Res]
	if len(renders) > 0 && renders[0] != nil {
		render = renders[0]
	}

	return func(c context.Context, ctx *hertzApp.RequestContext) {
		// 1. Bind request
		req, err := bind(c, ctx)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid request: " + err.Error(),
			})
			return
		}

		// 2. Execute pipeline
		res, err := engine.Execute(c, req)
		if err != nil {
			status, body := mapError(err)
			ctx.JSON(status, body)
			return
		}

		// 3. Render success
		if render != nil {
			render(c, ctx, res)
		} else {
			ctx.JSON(http.StatusOK, res)
		}
	}
}

// HertzStatsHandler creates a Hertz handler that returns engine stats as JSON.
// Mount this on an admin endpoint for runtime observability:
//
//	admin.GET("/seckill/stats", seckill.HertzStatsHandler(engine))
func HertzStatsHandler[Req any, Res any](engine *Engine[Req, Res]) hertzApp.HandlerFunc {
	return func(c context.Context, ctx *hertzApp.RequestContext) {
		ctx.JSON(http.StatusOK, engine.Stats())
	}
}

// HertzResetStockHandler creates a handler for admin stock reset.
// Works with any StockDriver implementation (local *Stock or distributed Redis).
// Expects JSON body: {"total": 1000}
//
//	admin.PUT("/seckill/stock", seckill.HertzResetStockHandler(engine))
func HertzResetStockHandler[Req any, Res any](engine *Engine[Req, Res]) hertzApp.HandlerFunc {
	return func(c context.Context, ctx *hertzApp.RequestContext) {
		driver := engine.StockDriver()
		if driver == nil {
			ctx.JSON(http.StatusBadRequest, map[string]string{
				"error": "stock is not configured for this engine",
			})
			return
		}

		var body struct {
			Total int64 `json:"total"`
		}
		if err := ctx.BindJSON(&body); err != nil || body.Total <= 0 {
			ctx.JSON(http.StatusBadRequest, map[string]string{
				"error": "total must be > 0",
			})
			return
		}

		driver.Reset(body.Total)
		ctx.JSON(http.StatusOK, map[string]any{
			"message":   "stock reset",
			"new_total": body.Total,
		})
	}
}

// ── error → HTTP status mapping ─────────────────────────────────────────────

func mapError(err error) (int, map[string]string) {
	switch {
	case errors.Is(err, ErrSoldOut):
		return http.StatusGone, map[string]string{
			"error": "sold out",
			"code":  "SOLD_OUT",
		}
	case errors.Is(err, ErrDuplicate):
		return http.StatusConflict, map[string]string{
			"error": "you have already participated in this seckill",
			"code":  "DUPLICATE",
		}
	case errors.Is(err, ErrGateFull):
		return http.StatusServiceUnavailable, map[string]string{
			"error": "server is busy, please retry",
			"code":  "GATE_FULL",
		}
	case errors.Is(err, ErrNoExecutor):
		return http.StatusInternalServerError, map[string]string{
			"error": "internal configuration error",
			"code":  "NO_EXECUTOR",
		}
	default:
		return http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
			"code":  "EXEC_ERROR",
		}
	}
}
