package http

import (
	consoleApp "backend-core/internal/console/app"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"backend-core/pkg/contracts"
	"context"
	"errors"
	"log"
	"sync"
	"time"

	hzApp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/hertz-contrib/websocket"
)

type Handler struct {
	svc *consoleApp.Service
}

func NewHandler(svc *consoleApp.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateSession(_ context.Context, c *hzApp.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}
	role, _ := authn.UserRole(c)
	session, err := h.svc.CreateSession(c.Param("id"), uid.String(), role == "admin")
	if err != nil {
		status, code := classifyConsoleError(err)
		c.JSON(status, apperr.Resp(code, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{
		"data": utils.H{
			"session_id": session.ID,
			"ticket":     session.Ticket,
			"ws_url":     "/api/v1/instances/console",
			"expires_at": session.ExpiresAt.Format(time.RFC3339),
		},
	})
}

var upgrader = websocket.HertzUpgrader{
	CheckOrigin: func(ctx *hzApp.RequestContext) bool { return true },
}

func (h *Handler) ServeWS(_ context.Context, c *hzApp.RequestContext) {
	ticket := c.Query("ticket")
	if ticket == "" {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "missing ticket query parameter"})
		return
	}
	session, err := h.svc.ConnectBrowser(ticket)
	if err != nil {
		status, code := classifyConsoleError(err)
		c.JSON(status, apperr.Resp(code, err.Error()))
		return
	}

	err = upgrader.Upgrade(c, func(conn *websocket.Conn) {
		defer h.svc.CloseSession(session.ID)
		_, agent, err := h.svc.WaitAgent(session.ID)
		if err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
			return
		}

		done := make(chan struct{})
		var doneOnce sync.Once
		finish := func() { doneOnce.Do(func() { close(done) }) }
		go func() {
			defer finish()
			for {
				messageType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if messageType != websocket.BinaryMessage {
					continue
				}
				if !agentSend(agent, contracts.ConsoleFrame{
					SessionID:  session.ID,
					InstanceID: session.InstanceID,
					Data:       data,
				}) {
					return
				}
			}
		}()
		go func() {
			defer finish()
			for {
				frame, ok := agent.Recv()
				if !ok {
					return
				}
				if !writeConsoleFrame(conn, frame) {
					return
				}
			}
		}()

		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-done:
				agentSend(agent, contracts.ConsoleFrame{SessionID: session.ID, InstanceID: session.InstanceID, Control: "close"})
				return
			case <-ping.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	})
	if err != nil {
		log.Printf("[console-ws] upgrade error: %v", err)
		h.svc.CloseSession(session.ID)
	}
}

func writeConsoleFrame(conn *websocket.Conn, frame contracts.ConsoleFrame) bool {
	if frame.Error != "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(frame.Error))
		return false
	}
	if frame.Control == "ready" || frame.Control == "open" {
		return true
	}
	if frame.Control == "close" {
		return false
	}
	if len(frame.Data) == 0 {
		return true
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, frame.Data); err != nil {
		return false
	}
	return true
}

func agentSend(agent interface {
	Send(contracts.ConsoleFrame) bool
}, frame contracts.ConsoleFrame) bool {
	return agent.Send(frame)
}

func classifyConsoleError(err error) (int, string) {
	switch {
	case errors.Is(err, consoleApp.ErrSessionNotFound), errors.Is(err, consoleApp.ErrSessionExpired):
		return consts.StatusUnauthorized, apperr.CodeUnauthorized
	case errors.Is(err, consoleApp.ErrAgentUnavailable), errors.Is(err, consoleApp.ErrRuntimeUnavailable):
		return consts.StatusUnprocessableEntity, apperr.CodeInvalidStateTransition
	default:
		return consts.StatusForbidden, apperr.CodeForbidden
	}
}
