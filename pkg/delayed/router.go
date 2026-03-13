package delayed

import (
	"log"
	"sync"
)

// Router is a lightweight topic-based dispatcher for delayed events.
// It maps topic strings to handler functions, allowing multiple bounded
// contexts to register their own handlers without knowing about each other.
//
// Usage:
//
//	router := delayed.NewRouter()
//	router.Handle("invoice.check_timeout", invoiceTimeoutWorker.HandlerFunc())
//	router.Handle("renewal.check_timeout", renewalWorker.HandlerFunc())
//
//	publisher := delayed.NewInMemoryPublisher(router.Dispatch)
type Router struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

// NewRouter creates an empty Router.
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]HandlerFunc),
	}
}

// Handle registers a handler for the given topic.
// If a handler already exists for the topic, it is replaced.
func (r *Router) Handle(topic string, fn HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[topic] = fn
	log.Printf("[delayed-router] registered handler for topic %q", topic)
}

// Dispatch routes a delayed event to the registered handler for its topic.
// If no handler is registered, the event is logged and discarded.
//
// This method is safe to use as the HandlerFunc callback for InMemoryPublisher.
func (r *Router) Dispatch(topic string, payload []byte) {
	r.mu.RLock()
	fn, ok := r.handlers[topic]
	r.mu.RUnlock()

	if !ok {
		log.Printf("[delayed-router] WARNING: no handler registered for topic %q, discarding", topic)
		return
	}
	fn(topic, payload)
}
