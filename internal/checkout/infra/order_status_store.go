package infra

import "sync"

// OrderStatus represents the current state of an async checkout order.
type OrderStatus struct {
	OrderID  string `json:"order_id"`
	Status   string `json:"status"` // "queued" | "processing" | "completed" | "failed"
	Message  string `json:"message,omitempty"`
	QueuePos int    `json:"queue_pos,omitempty"`
}

// OrderStatusStore tracks the status of async orders so the frontend can
// receive real-time updates via SSE (Server-Sent Events).
//
// In addition to simple Get/Set, the store supports a Subscribe/Unsubscribe
// pattern: when a status is Set, all subscribers for that order are notified
// immediately via their channel. This enables the SSE handler to push
// updates the moment they occur — zero polling overhead.
//
// This is a simple in-memory implementation; in production, use Redis
// Pub/Sub or similar for multi-instance support.
type OrderStatusStore struct {
	mu          sync.RWMutex
	orders      map[string]OrderStatus
	subscribers map[string][]chan OrderStatus // per-order subscriber channels
}

// NewOrderStatusStore creates a ready-to-use order status store.
func NewOrderStatusStore() *OrderStatusStore {
	return &OrderStatusStore{
		orders:      make(map[string]OrderStatus),
		subscribers: make(map[string][]chan OrderStatus),
	}
}

// Set updates the status of an order and notifies all subscribers.
func (s *OrderStatusStore) Set(orderID string, status OrderStatus) {
	s.mu.Lock()
	s.orders[orderID] = status
	// Copy subscriber list under lock, then notify outside the critical path
	subs := make([]chan OrderStatus, len(s.subscribers[orderID]))
	copy(subs, s.subscribers[orderID])
	s.mu.Unlock()

	// Non-blocking fan-out to all subscribers
	for _, ch := range subs {
		select {
		case ch <- status:
		default:
			// Subscriber channel full — skip (they'll get the next update)
		}
	}
}

// Get retrieves the current status of an order.
func (s *OrderStatusStore) Get(orderID string) (OrderStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.orders[orderID]
	return st, ok
}

// Subscribe registers a channel that receives status updates for the given
// order. The returned channel is buffered (capacity 8) to avoid blocking
// the Set path. The caller MUST call Unsubscribe when done to prevent leaks.
//
// Usage (in SSE handler):
//
//	ch := store.Subscribe(orderID)
//	defer store.Unsubscribe(orderID, ch)
//	for status := range ch { ... }
func (s *OrderStatusStore) Subscribe(orderID string) chan OrderStatus {
	ch := make(chan OrderStatus, 8)
	s.mu.Lock()
	s.subscribers[orderID] = append(s.subscribers[orderID], ch)
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel for the given order.
// It also closes the channel so range loops terminate cleanly.
func (s *OrderStatusStore) Unsubscribe(orderID string, ch chan OrderStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subs := s.subscribers[orderID]
	for i, sub := range subs {
		if sub == ch {
			s.subscribers[orderID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	// Clean up empty subscriber lists
	if len(s.subscribers[orderID]) == 0 {
		delete(s.subscribers, orderID)
	}
	close(ch)
}
