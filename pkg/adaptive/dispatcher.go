package adaptive

import (
	"log"
	"sync/atomic"
)

// Processor is the generic interface for request processing.
// Both sync and async implementations must satisfy this contract.
//
// Type parameters:
//   - Req: the request type (e.g. CheckoutRequest)
//   - Res: the response type (e.g. *CheckoutResult)
type Processor[Req any, Res any] interface {
	Process(req Req) (Res, error)
}

// Dispatcher is a generic adaptive gateway that routes requests to either
// a synchronous or asynchronous processor based on real-time QPS.
//
// When QPS < threshold → syncProcessor (typically returns HTTP 200)
// When QPS >= threshold → asyncProcessor (typically returns HTTP 202)
//
// This is a business-agnostic wrapper. The actual business logic lives in
// the Processor implementations provided by each domain.
type Dispatcher[Req any, Res any] struct {
	syncProcessor  Processor[Req, Res]
	asyncProcessor Processor[Req, Res]
	qpsMonitor     *SlidingWindowQPSMonitor
	threshold      int64
}

// NewDispatcher creates a new adaptive dispatcher.
//
// Parameters:
//   - syncProc:  processor used under normal load (QPS < threshold)
//   - asyncProc: processor used under high load (QPS >= threshold)
//   - monitor:   sliding window QPS monitor
//   - threshold: QPS threshold for switching to async mode (default: 500)
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
func (d *Dispatcher[Req, Res]) Dispatch(req Req) (Res, error) {
	// 1. Record this request for QPS tracking
	d.qpsMonitor.Record()

	// 2. Check current QPS and select processor
	currentQPS := d.qpsMonitor.CurrentQPS()
	thresh := atomic.LoadInt64(&d.threshold)
	isHighLoad := currentQPS >= float64(thresh)

	if isHighLoad {
		log.Printf("[adaptive.Dispatcher] HIGH LOAD (QPS=%.1f >= %d) → async mode",
			currentQPS, thresh)
		return d.asyncProcessor.Process(req)
	}

	log.Printf("[adaptive.Dispatcher] NORMAL LOAD (QPS=%.1f < %d) → sync mode",
		currentQPS, thresh)
	return d.syncProcessor.Process(req)
}

// SetThreshold updates the QPS threshold at runtime (e.g. via admin API).
func (d *Dispatcher[Req, Res]) SetThreshold(t int) {
	atomic.StoreInt64(&d.threshold, int64(t))
	log.Printf("[adaptive.Dispatcher] threshold updated to %d QPS", t)
}

// GetThreshold returns the current QPS threshold.
func (d *Dispatcher[Req, Res]) GetThreshold() int {
	return int(atomic.LoadInt64(&d.threshold))
}

// IsHighLoad returns true if the current QPS exceeds the threshold.
func (d *Dispatcher[Req, Res]) IsHighLoad() bool {
	return d.qpsMonitor.CurrentQPS() >= float64(atomic.LoadInt64(&d.threshold))
}

// QPSStats returns the current QPS monitoring statistics.
func (d *Dispatcher[Req, Res]) QPSStats() QPSStats {
	return d.qpsMonitor.Stats()
}

// Monitor returns the underlying QPS monitor (for external stats access).
func (d *Dispatcher[Req, Res]) Monitor() *SlidingWindowQPSMonitor {
	return d.qpsMonitor
}
