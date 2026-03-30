package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"context"
	"log"
	"time"
)

type RenewalWorker struct {
	service      *paymentApp.RenewalService
	leadDays     int
	pollInterval time.Duration
}

func NewRenewalWorker(service *paymentApp.RenewalService, leadDays int, pollInterval time.Duration) *RenewalWorker {
	if pollInterval <= 0 {
		pollInterval = time.Hour
	}
	return &RenewalWorker{
		service:      service,
		leadDays:     leadDays,
		pollInterval: pollInterval,
	}
}

func (w *RenewalWorker) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(w.pollInterval)
		defer ticker.Stop()
		log.Printf("[renewal-worker] started (lead_days=%d interval=%v)", w.leadDays, w.pollInterval)

		w.runOnce()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[renewal-worker] stopped")
				return
			case <-ticker.C:
				w.runOnce()
			}
		}
	}()
}

func (w *RenewalWorker) runOnce() {
	if w.service == nil {
		return
	}
	if err := w.service.RunCycle(time.Now().UTC(), w.leadDays); err != nil {
		log.Printf("[renewal-worker] WARNING: renewal cycle failed: %v", err)
	}
}
