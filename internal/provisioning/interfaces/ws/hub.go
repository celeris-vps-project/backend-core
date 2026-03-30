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
	"errors"
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
	conn      *websocket.Conn
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex
	closed    bool
}

var errClientClosed = errors.New("websocket client closed")

func (c *client) enqueue(msg []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case <-c.done:
		return false
	case c.send <- msg:
		return true
	default:
		return false
	}
}

func (c *client) write(messageType int, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.closed {
		return errClientClosed
	}

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(messageType, payload); err != nil {
		c.closed = true
		_ = c.conn.Close()
		return err
	}

	return nil
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)

		c.writeMu.Lock()
		c.closed = true
		_ = c.conn.Close()
		c.writeMu.Unlock()
	})
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

func (h *Hub) requestUnregister(c *client) {
	c.close()
	go func() {
		h.unregister <- c
	}()
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
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("[ws-hub] client connected (total: %d)", count)

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
			}
			count := len(h.clients)
			h.mu.Unlock()
			c.close()
			log.Printf("[ws-hub] client disconnected (total: %d)", count)

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				if !c.enqueue(msg) {
					// Client too slow or already closing; drop it.
					h.requestUnregister(c)
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
			done: make(chan struct{}),
		}
		h.register <- cl

		// Writer goroutine: sends messages from the hub to the client.
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer func() {
				ticker.Stop()
				h.requestUnregister(cl)
			}()
			for {
				select {
				case <-cl.done:
					return
				case msg := <-cl.send:
					if err := cl.write(websocket.TextMessage, msg); err != nil {
						return
					}
				case <-ticker.C:
					// Ping to keep the connection alive
					if err := cl.write(websocket.PingMessage, nil); err != nil {
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
		h.requestUnregister(cl)
	})
	if err != nil {
		log.Printf("[ws-hub] upgrade error: %v", err)
	}
}
