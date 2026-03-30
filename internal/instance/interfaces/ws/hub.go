// Package ws streams customer-visible instance state changes over WebSocket.
package ws

import (
	"backend-core/internal/instance/domain"
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

type client struct {
	conn      *websocket.Conn
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex
	closed    bool
	userID    string
}

type outboundMessage struct {
	userID string
	data   []byte
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

// Hub manages user-scoped instance status streams.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	broadcast  chan outboundMessage
	tickets    *TicketStore
	repo       domain.InstanceRepository
}

func NewHub(repo domain.InstanceRepository) *Hub {
	h := &Hub{
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan outboundMessage, 256),
		tickets:    NewTicketStore(),
		repo:       repo,
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

func (h *Hub) Register(bus *eventbus.EventBus) {
	bus.Subscribe(events.InstanceStateUpdatedEvent{}.EventName(), func(e eventbus.Event) {
		evt, ok := e.(events.InstanceStateUpdatedEvent)
		if !ok {
			return
		}
		data, err := json.Marshal(evt)
		if err != nil {
			log.Printf("[instance-ws] failed to marshal event: %v", err)
			return
		}
		select {
		case h.broadcast <- outboundMessage{userID: evt.CustomerID, data: data}:
		default:
			log.Printf("[instance-ws] broadcast channel full, dropping message for instance %s", evt.InstanceID)
		}
	})
}

func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("[instance-ws] client connected (total: %d)", count)

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
			}
			count := len(h.clients)
			h.mu.Unlock()
			c.close()
			log.Printf("[instance-ws] client disconnected (total: %d)", count)

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				if c.userID != msg.userID {
					continue
				}
				if !c.enqueue(msg.data) {
					h.requestUnregister(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

var upgrader = websocket.HertzUpgrader{
	CheckOrigin: func(ctx *app.RequestContext) bool { return true },
}

func (h *Hub) ServeWS(_ context.Context, c *app.RequestContext) {
	ticket := c.Query("ticket")
	if ticket == "" {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "missing ticket query parameter"})
		return
	}
	userID, _, err := h.tickets.Redeem(ticket)
	if err != nil {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "invalid or expired ticket"})
		return
	}

	err = upgrader.Upgrade(c, func(conn *websocket.Conn) {
		cl := &client{
			conn:   conn,
			send:   make(chan []byte, 64),
			done:   make(chan struct{}),
			userID: userID,
		}
		h.register <- cl

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
					if err := cl.write(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		h.sendInitialSnapshots(cl)

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
		log.Printf("[instance-ws] upgrade error: %v", err)
	}
}

func (h *Hub) sendInitialSnapshots(cl *client) {
	if h.repo == nil {
		return
	}
	instances, err := h.repo.ListByCustomerID(cl.userID)
	if err != nil {
		log.Printf("[instance-ws] failed to load initial snapshots for user %s: %v", cl.userID, err)
		return
	}
	for _, inst := range instances {
		data, err := json.Marshal(toInstanceStateEvent(inst))
		if err != nil {
			log.Printf("[instance-ws] failed to marshal initial snapshot for instance %s: %v", inst.ID(), err)
			continue
		}
		if !cl.enqueue(data) {
			log.Printf("[instance-ws] initial snapshot queue full for user %s", cl.userID)
			return
		}
	}
}

func toInstanceStateEvent(inst *domain.Instance) events.InstanceStateUpdatedEvent {
	return events.InstanceStateUpdatedEvent{
		InstanceID:   inst.ID(),
		CustomerID:   inst.CustomerID(),
		OrderID:      inst.OrderID(),
		NodeID:       inst.NodeID(),
		Hostname:     inst.Hostname(),
		Plan:         inst.Plan(),
		OS:           inst.OS(),
		CPU:          inst.CPU(),
		MemoryMB:     inst.MemoryMB(),
		DiskGB:       inst.DiskGB(),
		IPv4:         inst.IPv4(),
		IPv6:         inst.IPv6(),
		Status:       inst.Status(),
		NetworkMode:  inst.NetworkMode(),
		NATPort:      inst.NATPort(),
		CreatedAt:    inst.CreatedAt().Format(time.RFC3339),
		StartedAt:    formatOptionalTime(inst.StartedAt()),
		StoppedAt:    formatOptionalTime(inst.StoppedAt()),
		SuspendedAt:  formatOptionalTime(inst.SuspendedAt()),
		TerminatedAt: formatOptionalTime(inst.TerminatedAt()),
	}
}

func formatOptionalTime(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	formatted := ts.Format(time.RFC3339)
	return &formatted
}
