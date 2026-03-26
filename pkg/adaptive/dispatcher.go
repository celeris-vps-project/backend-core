package adaptive

import (
	"context"
	"log"
	"sync/atomic"
)

// Processor is the generic interface for request processing.
// Both sync and async implementations must satisfy this contract.
type Processor[Req any, Res any] interface {
	Process(ctx context.Context, req Req) (Res, error)
}

// Dispatcher is a generic adaptive gateway that routes requests to either
// a synchronous or asynchronous processor based on real-time QPS.
type Dispatcher[Req any, Res any] struct {
	syncProcessor  Processor[Req, Res]
	asyncProcessor Processor[Req, Res]
	qpsMonitor     *SlidingWindowQPSMonitor
	threshold      int64
}

// NewDispatcher creates a new adaptive dispatcher.
func NewDispatcher[Req any, Res any](
	syncProc Processor[Req, Res],
	asyncProc Processor[Req, Res],
	monitor *SlidingWindowQPSMonitor,
	threshold int,
) *Dispatcher[Req, Res] {
	if threshold <= 0 {
		threshold = 500
	}
	return &Dispatcher[Req, Res]{
		syncProcessor:  syncProc,
		asyncProcessor: asyncProc,
		qpsMonitor:     monitor,
		threshold:      int64(threshold),
	}
}

// Dispatch processes a request, automatically selecting the appropriate
// processor based on current QPS.
func (d *Dispatcher[Req, Res]) Dispatch(ctx context.Context, req Req) (Res, error) {
	d.qpsMonitor.Record()

	currentQPS := d.qpsMonitor.CurrentQPS()
	thresh := atomic.LoadInt64(&d.threshold)
	isHighLoad := currentQPS >= float64(thresh)

	if isHighLoad {
		log.Printf("[adaptive.Dispatcher] HIGH LOAD (QPS=%.1f >= %d) → async mode",
			currentQPS, thresh)
		return d.asyncProcessor.Process(ctx, req)
	}

	log.Printf("[adaptive.Dispatcher] NORMAL LOAD (QPS=%.1f < %d) → sync mode",
		currentQPS, thresh)
	return d.syncProcessor.Process(ctx, req)
}

func (d *Dispatcher[Req, Res]) SetThreshold(t int) {
	atomic.StoreInt64(&d.threshold, int64(t))
	log.Printf("[adaptive.Dispatcher] threshold updated to %d QPS", t)
}

func (d *Dispatcher[Req, Res]) GetThreshold() int {
	return int(atomic.LoadInt64(&d.threshold))
}

func (d *Dispatcher[Req, Res]) IsHighLoad() bool {
	return d.qpsMonitor.CurrentQPS() >= float64(atomic.LoadInt64(&d.threshold))
}

func (d *Dispatcher[Req, Res]) QPSStats() QPSStats {
	return d.qpsMonitor.Stats()
}

func (d *Dispatcher[Req, Res]) Monitor() *SlidingWindowQPSMonitor {
	return d.qpsMonitor
}
