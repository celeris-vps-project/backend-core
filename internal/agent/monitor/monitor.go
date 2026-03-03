package monitor

import (
	"backend-core/pkg/contracts"
	"runtime"
	"time"
)

// Collect gathers basic host metrics.
// In production, replace with real syscall-based metrics (e.g. gopsutil).
func Collect(nodeID string) contracts.Heartbeat {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return contracts.Heartbeat{
		NodeID:     nodeID,
		CPUUsage:   0, // TODO: real CPU sampling
		MemUsage:   float64(m.Alloc) / float64(m.Sys) * 100,
		DiskUsage:  0, // TODO: real disk check
		Uptime:     0, // TODO: read from /proc/uptime
		VMCount:    0, // TODO: query libvirt/QEMU
		ReportedAt: time.Now().Format(time.RFC3339),
	}
}
