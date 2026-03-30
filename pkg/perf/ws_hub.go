package perf

import (
	"backend-core/pkg/authn"
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/hertz-contrib/websocket"
)

// ─────────────────────────────────────────────────────────────────────────────
// PerformanceHub — WebSocket hub for real-time performance metrics
// ─────────────────────────────────────────────────────────────────────────────

// perfClient represents a single WebSocket connection to the performance hub.
type perfClient struct {
	conn      *websocket.Conn
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
	writeMu   sync.Mutex
	closed    bool
}

var errPerfClientClosed = errors.New("websocket client closed")

func (c *perfClient) enqueue(msg []byte) bool {
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

func (c *perfClient) write(messageType int, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.closed {
		return errPerfClientClosed
	}

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(messageType, payload); err != nil {
		c.closed = true
		_ = c.conn.Close()
		return err
	}

	return nil
}

func (c *perfClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)

		c.writeMu.Lock()
		c.closed = true
		_ = c.conn.Close()
		c.writeMu.Unlock()
	})
}

// PerformanceHub manages WebSocket connections and periodically broadcasts
// performance snapshots from the EndpointTracker.
type PerformanceHub struct {
	tracker    *EndpointTracker
	mu         sync.RWMutex
	clients    map[*perfClient]struct{}
	register   chan *perfClient
	unregister chan *perfClient
	broadcast  chan []byte
	tickets    *PerfTicketStore
	interval   int64 // broadcast interval in seconds (atomic)
	topN       int   // number of top endpoints to include
	cancelFunc context.CancelFunc
}

// NewPerformanceHub creates a hub and starts its run loop + ticker.
func NewPerformanceHub(tracker *EndpointTracker, intervalSec int, topN int) *PerformanceHub {
	if intervalSec <= 0 {
		intervalSec = 2
	}
	if topN <= 0 {
		topN = 5
	}

	ctx, cancel := context.WithCancel(context.Background())

	h := &PerformanceHub{
		tracker:    tracker,
		clients:    make(map[*perfClient]struct{}),
		register:   make(chan *perfClient),
		unregister: make(chan *perfClient),
		broadcast:  make(chan []byte, 256),
		tickets:    NewPerfTicketStore(),
		interval:   int64(intervalSec),
		topN:       topN,
		cancelFunc: cancel,
	}
	go h.run()
	go h.tickLoop(ctx)
	return h
}

func (h *PerformanceHub) requestUnregister(c *perfClient) {
	c.close()
	go func() {
		h.unregister <- c
	}()
}

// SetInterval changes the broadcast interval (seconds). Admin-adjustable.
func (h *PerformanceHub) SetInterval(sec int) {
	if sec < 1 {
		sec = 1
	}
	if sec > 30 {
		sec = 30
	}
	atomic.StoreInt64(&h.interval, int64(sec))
	log.Printf("[perf-hub] broadcast interval set to %ds", sec)
}

// GetInterval returns the current broadcast interval in seconds.
func (h *PerformanceHub) GetInterval() int {
	return int(atomic.LoadInt64(&h.interval))
}

// run is the main hub loop.
func (h *PerformanceHub) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("[perf-hub] client connected (total: %d)", count)

			// Send an immediate snapshot on connect
			go func() {
				snap := h.tracker.Snapshot(h.topN)
				data, err := json.Marshal(snap)
				if err == nil {
					_ = c.enqueue(data)
				}
			}()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
			}
			count := len(h.clients)
			h.mu.Unlock()
			c.close()
			log.Printf("[perf-hub] client disconnected (total: %d)", count)

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				if !c.enqueue(msg) {
					h.requestUnregister(c)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// tickLoop periodically generates snapshots and broadcasts them.
func (h *PerformanceHub) tickLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		interval := time.Duration(atomic.LoadInt64(&h.interval)) * time.Second
		time.Sleep(interval)

		h.mu.RLock()
		clientCount := len(h.clients)
		h.mu.RUnlock()

		if clientCount == 0 {
			continue
		}

		snap := h.tracker.Snapshot(h.topN)
		data, err := json.Marshal(snap)
		if err != nil {
			log.Printf("[perf-hub] failed to marshal snapshot: %v", err)
			continue
		}

		select {
		case h.broadcast <- data:
		default:
			log.Printf("[perf-hub] broadcast channel full, dropping snapshot")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP Handlers
// ─────────────────────────────────────────────────────────────────────────────

var perfUpgrader = websocket.HertzUpgrader{
	CheckOrigin: func(ctx *app.RequestContext) bool { return true },
}

// IssueTicket is an HTTP handler that issues a short-lived WS ticket.
// Must be called behind AdminMiddleware.
func (h *PerformanceHub) IssueTicket(_ context.Context, c *app.RequestContext) {
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

// ServeWS upgrades an HTTP connection to WebSocket for performance streaming.
func (h *PerformanceHub) ServeWS(_ context.Context, c *app.RequestContext) {
	ticket := c.Query("ticket")
	if ticket == "" {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "missing ticket"})
		return
	}
	_, _, err := h.tickets.Redeem(ticket)
	if err != nil {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "invalid or expired ticket"})
		return
	}

	err = perfUpgrader.Upgrade(c, func(conn *websocket.Conn) {
		cl := &perfClient{
			conn: conn,
			send: make(chan []byte, 64),
			done: make(chan struct{}),
		}
		h.register <- cl

		// Writer goroutine
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

		// Reader goroutine — reads client messages (e.g. interval change)
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var cmd struct {
				Action   string `json:"action"`
				Interval int    `json:"interval"`
			}
			if err := json.Unmarshal(msg, &cmd); err == nil {
				if cmd.Action == "set_interval" && cmd.Interval > 0 {
					h.SetInterval(cmd.Interval)
				}
			}
		}
		h.requestUnregister(cl)
	})
	if err != nil {
		log.Printf("[perf-hub] upgrade error: %v", err)
	}
}

// SetIntervalHandler handles PUT /admin/performance/interval
func (h *PerformanceHub) SetIntervalHandler(_ context.Context, c *app.RequestContext) {
	var body struct {
		Interval int `json:"interval"`
	}
	if err := c.BindJSON(&body); err != nil || body.Interval < 1 || body.Interval > 30 {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "interval must be between 1 and 30 seconds"})
		return
	}
	h.SetInterval(body.Interval)
	c.JSON(consts.StatusOK, utils.H{"message": "interval updated", "interval": body.Interval})
}

// GetSnapshotHandler handles GET /admin/performance/snapshot (REST fallback)
func (h *PerformanceHub) GetSnapshotHandler(_ context.Context, c *app.RequestContext) {
	snap := h.tracker.Snapshot(h.topN)
	snap.Type = "performance_snapshot"
	c.JSON(consts.StatusOK, snap)
}
