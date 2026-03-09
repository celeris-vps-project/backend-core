package infra

import (
	"backend-core/internal/node/domain"
	"sync"
	"time"
)

// cacheEntry wraps a NodeState with an expiration timestamp.
type cacheEntry struct {
	state     *domain.NodeState
	expiresAt time.Time
}

// MemoryNodeStateCache is an in-memory implementation of domain.NodeStateCache.
// Entries expire automatically after the configured TTL.
type MemoryNodeStateCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewMemoryNodeStateCache creates a new in-memory cache with the given TTL.
// A background goroutine runs every ttl/2 to evict expired entries.
func NewMemoryNodeStateCache(ttl time.Duration) *MemoryNodeStateCache {
	c := &MemoryNodeStateCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

func (c *MemoryNodeStateCache) SetNodeState(nodeID string, state *domain.NodeState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[nodeID] = &cacheEntry{
		state:     state,
		expiresAt: time.Now().Add(c.ttl),
	}
	return nil
}

func (c *MemoryNodeStateCache) GetNodeState(nodeID string) (*domain.NodeState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[nodeID]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, nil
	}
	return entry.state, nil
}

func (c *MemoryNodeStateCache) GetAllNodeStates() (map[string]*domain.NodeState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	result := make(map[string]*domain.NodeState)
	for id, entry := range c.entries {
		if now.Before(entry.expiresAt) {
			result[id] = entry.state
		}
	}
	return result, nil
}

func (c *MemoryNodeStateCache) DeleteNodeState(nodeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, nodeID)
	return nil
}

// Stop terminates the background eviction goroutine.
func (c *MemoryNodeStateCache) Stop() {
	close(c.stopCh)
}

func (c *MemoryNodeStateCache) evictLoop() {
	ticker := time.NewTicker(c.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.evict()
		case <-c.stopCh:
			return
		}
	}
}

func (c *MemoryNodeStateCache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, id)
		}
	}
}
