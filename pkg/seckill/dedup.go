package seckill

import (
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// DedupStore — in-memory idempotency guard with TTL
// ─────────────────────────────────────────────────────────────────────────────

// DedupStore prevents the same logical operation from executing twice within
// a configurable time window. Typical use: user X can only buy product Y once
// per seckill round.
//
// Keys are stored in a sync.Map for lock-free reads on the hot path.
// A background goroutine periodically evicts expired entries so memory stays
// bounded even under long-running seckill events.
type DedupStore struct {
	entries  sync.Map      // key → expireAt (int64, unix-nano)
	ttl      time.Duration // how long a key is "remembered"
	blocked  int64         // atomic counter of rejected duplicates
	stopGC   chan struct{}
	gcTicker *time.Ticker
}

// NewDedupStore creates a dedup store with the given TTL.
// A background GC runs every ttl/2 (min 1s) to evict expired keys.
func NewDedupStore(ttl time.Duration) *DedupStore {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	gcInterval := ttl / 2
	if gcInterval < time.Second {
		gcInterval = time.Second
	}

	d := &DedupStore{
		ttl:      ttl,
		stopGC:   make(chan struct{}),
		gcTicker: time.NewTicker(gcInterval),
	}
	go d.gc()
	return d
}

// MarkIfAbsent atomically checks whether `key` already exists (not expired).
//   - Returns true  if this is the FIRST time seeing the key → caller may proceed.
//   - Returns false if the key was already present → duplicate, caller should reject.
//
// On first-seen, the key is stored with an expiration timestamp.
func (d *DedupStore) MarkIfAbsent(key string) bool {
	now := time.Now().UnixNano()
	expireAt := now + int64(d.ttl)

	// Fast path: check if already exists and not expired
	if v, loaded := d.entries.LoadOrStore(key, expireAt); loaded {
		// Key existed — check if it's expired
		oldExpire := v.(int64)
		if now < oldExpire {
			// Not expired → duplicate
			atomic.AddInt64(&d.blocked, 1)
			return false
		}
		// Expired → overwrite with new expiration
		d.entries.Store(key, expireAt)
		return true
	}
	// Key was newly stored → first time
	return true
}

// Blocked returns the total number of duplicate requests rejected.
func (d *DedupStore) Blocked() int64 {
	return atomic.LoadInt64(&d.blocked)
}

// Remove explicitly deletes a key (e.g. on execute failure to allow retry).
func (d *DedupStore) Remove(key string) {
	d.entries.Delete(key)
}

// Stop shuts down the background GC goroutine. Call on graceful shutdown.
func (d *DedupStore) Stop() {
	close(d.stopGC)
}

// gc periodically walks the map and deletes expired entries.
func (d *DedupStore) gc() {
	for {
		select {
		case <-d.gcTicker.C:
			now := time.Now().UnixNano()
			d.entries.Range(func(key, value any) bool {
				if now >= value.(int64) {
					d.entries.Delete(key)
				}
				return true
			})
		case <-d.stopGC:
			d.gcTicker.Stop()
			return
		}
	}
}
