package seckill

// ─────────────────────────────────────────────────────────────────────────────
// Driver interfaces — pluggable backends for multi-node cluster support
// ─────────────────────────────────────────────────────────────────────────────
//
// The Engine pipeline (Gate → Dedup → Stock → Execute) operates through these
// interfaces. The default local implementations (*Stock, *DedupStore, *Gate)
// satisfy them out of the box. For multi-node clusters, inject distributed
// implementations (e.g. Redis-backed) via WithStockDriver / WithDedupDriver /
// WithGateDriver.
//
// Single-node (default):
//
//	engine := seckill.New[Req, Res](
//	    seckill.WithStock[Req, Res](1000),        // creates local *Stock
//	    seckill.WithDedup[Req, Res](5*time.Minute), // creates local *DedupStore
//	    seckill.WithExecute[Req, Res](myFn),
//	)
//
// Multi-node cluster:
//
//	engine := seckill.New[Req, Res](
//	    seckill.WithStockDriver[Req, Res](myRedisStock),  // Redis DECR
//	    seckill.WithDedupDriver[Req, Res](myRedisDedup),  // Redis SETNX+TTL
//	    seckill.WithGate[Req, Res](200),                  // Gate stays per-node
//	    seckill.WithExecute[Req, Res](myFn),
//	)

// StockDriver abstracts atomic inventory operations.
//
// Local implementation: *Stock (CAS atomic int64, zero allocations).
// Cluster implementation example: Redis DECR with Lua script for atomic
// decrement-if-positive.
//
//	type RedisStock struct { rdb *redis.Client; key string }
//	func (r *RedisStock) TryAcquire() bool {
//	    val, _ := r.rdb.Decr(ctx, r.key).Result()
//	    if val < 0 { r.rdb.Incr(ctx, r.key); return false }
//	    return true
//	}
type StockDriver interface {
	// TryAcquire atomically decrements stock by one.
	// Returns true if inventory was available, false if sold out.
	TryAcquire() bool

	// Rollback returns one unit back to stock (e.g. on execute failure).
	Rollback()

	// Remaining returns current available inventory.
	Remaining() int64

	// Sold returns the number of units acquired so far.
	Sold() int64

	// Total returns the original/configured capacity.
	Total() int64

	// Reset sets inventory to a new total (admin restock).
	Reset(total int64)
}

// DedupDriver abstracts idempotency / duplicate-request detection.
//
// Local implementation: *DedupStore (sync.Map + TTL with background GC).
// Cluster implementation example: Redis SET key NX EX ttl.
//
//	type RedisDedup struct { rdb *redis.Client; ttl time.Duration; prefix string }
//	func (r *RedisDedup) MarkIfAbsent(key string) bool {
//	    ok, _ := r.rdb.SetNX(ctx, r.prefix+key, "1", r.ttl).Result()
//	    return ok
//	}
type DedupDriver interface {
	// MarkIfAbsent returns true if the key was NOT previously seen (first time),
	// false if the key already exists within TTL (duplicate).
	MarkIfAbsent(key string) bool

	// Remove deletes a key, allowing the same operation to be retried.
	Remove(key string)

	// Blocked returns the total count of duplicate requests rejected.
	Blocked() int64

	// Stop shuts down any background goroutines (e.g. GC).
	Stop()
}

// GateDriver abstracts concurrency limiting (semaphore).
//
// Local implementation: *Gate (buffered channel, per-node).
// In most deployments, Gate should remain per-node because it protects local
// resources (goroutines, memory, DB connections). For true cluster-wide
// concurrency control, inject a Redis-based distributed semaphore.
//
// Typical recommendation: keep Gate per-node. N nodes × capacity = cluster total.
type GateDriver interface {
	// TryEnter attempts to acquire a concurrency slot without blocking.
	// Returns true if the caller may proceed. Caller MUST call Leave() on true.
	TryEnter() bool

	// Leave releases a concurrency slot.
	Leave()

	// InFlight returns the current number of in-flight requests.
	InFlight() int64

	// Capacity returns the maximum allowed concurrency.
	Capacity() int
}

// compile-time assertions: local types satisfy their driver interfaces.
var (
	_ StockDriver = (*Stock)(nil)
	_ DedupDriver = (*DedupStore)(nil)
	_ GateDriver  = (*Gate)(nil)
)
