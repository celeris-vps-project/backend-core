// Package ws provides a WebSocket hub that broadcasts real-time node state
// updates to connected admin clients. It subscribes to NodeStateUpdatedEvent
// on the in-process EventBus and fans out JSON messages to all connections.
package ws

import (
	"backend-core/pkg/authn"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/hertz-contrib/websocket"
)

// client represents a single WebSocket connection.
type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub manages WebSocket connections and broadcasts node state updates.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	tickets    *TicketStore
}

// NewHub creates a Hub and starts its run loop.
func NewHub() *Hub {
	h := &Hub{
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 256),
		tickets:    NewTicketStore(),
	}
	go h.run()
	return h
}

// IssueTicket is an HTTP handler that issues a short-lived, one-time ticket
// for WebSocket authentication. It must be called behind AdminMiddleware so
// that current_user_id and current_user_role are already set in the context.
func (h *Hub) IssueTicket(_ context.Context, c *app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "unauthorized"})
		return
	}
	role, _ := authn.UserRole(c)

	ticket, err := h.tickets.Issue(uid.String(), role)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": "failed to issue ticket"})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ticket": ticket})
}

// Register subscribes this hub to node state events on the EventBus.
func (h *Hub) Register(bus *eventbus.EventBus) {
	bus.Subscribe(events.NodeStateUpdatedEvent{}.EventName(), func(e eventbus.Event) {
		evt, ok := e.(events.NodeStateUpdatedEvent)
		if !ok {
			return
		}
		data, err := json.Marshal(evt)
		if err != nil {
			log.Printf("[ws-hub] failed to marshal event: %v", err)
			return
		}
		// Non-blocking send to avoid slow subscribers blocking the event bus
		select {
		case h.broadcast <- data:
		default:
			log.Printf("[ws-hub] broadcast channel full, dropping message for node %s", evt.NodeID)
		}
	})
}

// run is the main hub loop managing client registration and message broadcasting.
func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
			log.Printf("[ws-hub] client connected (total: %d)", len(h.clients))

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
			log.Printf("[ws-hub] client disconnected (total: %d)", len(h.clients))

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Client too slow — drop it
					go func(cl *client) { h.unregister <- cl }(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// upgrader configures the WebSocket upgrade.
var upgrader = websocket.HertzUpgrader{
	CheckOrigin: func(ctx *app.RequestContext) bool { return true },
}

// ServeWS is the Hertz handler that upgrades an HTTP connection to WebSocket.
// Authentication is done via a short-lived, one-time `ticket` query parameter
// obtained from the IssueTicket endpoint.
func (h *Hub) ServeWS(_ context.Context, c *app.RequestContext) {
	// Authenticate via one-time ticket
	ticket := c.Query("ticket")
	if ticket == "" {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "missing ticket query parameter"})
		return
	}
	_, _, err := h.tickets.Redeem(ticket)
	if err != nil {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "invalid or expired ticket"})
		return
	}

	// Upgrade to WebSocket
	err = upgrader.Upgrade(c, func(conn *websocket.Conn) {
		cl := &client{
			conn: conn,
			send: make(chan []byte, 64),
		}
		h.register <- cl

		// Writer goroutine: sends messages from the hub to the client.
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer func() {
				ticker.Stop()
				conn.Close()
			}()
			for {
				select {
				case msg, ok := <-cl.send:
					conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if !ok {
						conn.WriteMessage(websocket.CloseMessage, nil)
						return
					}
					if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
						return
					}
				case <-ticker.C:
					// Ping to keep the connection alive
					conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Reader goroutine: reads (and discards) client messages; detects disconnect.
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		h.unregister <- cl
	})
	if err != nil {
		log.Printf("[ws-hub] upgrade error: %v", err)
	}
}
