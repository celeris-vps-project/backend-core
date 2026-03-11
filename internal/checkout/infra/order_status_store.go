package infra

import "sync"

// OrderStatus represents the current state of an async checkout order.
type OrderStatus struct {
	OrderID  string `json:"order_id"`
	Status   string `json:"status"` // "queued" | "processing" | "completed" | "failed"
	Message  string `json:"message,omitempty"`
	QueuePos int    `json:"queue_pos,omitempty"`
}

// OrderStatusStore tracks the status of async orders so the frontend can poll.
// This is a simple in-memory implementation; in production, use Redis or DB.
type OrderStatusStore struct {
	mu     sync.RWMutex
	orders map[string]OrderStatus
}

// NewOrderStatusStore creates a ready-to-use order status store.
func NewOrderStatusStore() *OrderStatusStore {
	return &OrderStatusStore{orders: make(map[string]OrderStatus)}
}

// Set updates the status of an order.
func (s *OrderStatusStore) Set(orderID string, status OrderStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[orderID] = status
}

// Get retrieves the status of an order.
func (s *OrderStatusStore) Get(orderID string) (OrderStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.orders[orderID]
	return st, ok
}
