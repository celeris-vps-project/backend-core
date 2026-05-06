package monitor

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

type Collector struct {
	NodeID string
	Driver vm.Hypervisor
}

func NewCollector(nodeID string, driver vm.Hypervisor) *Collector {
	return &Collector{
		NodeID: nodeID,
		Driver: driver,
	}
}

func (c *Collector) Collect() contracts.Heartbeat {
	var cpuUsage float64
	cpuPercents, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercents) > 0 {
		cpuUsage = cpuPercents[0]
	}

	var memUsage float64
	vMem, err := mem.VirtualMemory()
	if err == nil {
		memUsage = vMem.UsedPercent
	}

	var diskUsage float64
	dUsage, err := disk.Usage("/")
	if err == nil {
		diskUsage = dUsage.UsedPercent
	}

	var uptime uint64
	hostInfo, err := host.Info()
	if err == nil {
		uptime = hostInfo.Uptime
	}

	reportedAt := time.Now().Format(time.RFC3339)
	vmInfos, _ := c.Driver.List()
	vmStates := make([]contracts.InstanceRuntimeState, 0, len(vmInfos))
	for _, info := range vmInfos {
		if info == nil || info.InstanceID == "" {
			continue
		}
		vmStates = append(vmStates, contracts.InstanceRuntimeState{
			InstanceID: info.InstanceID,
			State:      info.State,
			IPv4:       info.IPv4,
			IPv6:       info.IPv6,
			ReportedAt: reportedAt,
			VMTransferred: contracts.VMTransferred{
				Total: info.NetworkStats.Total,
				RX:    info.NetworkStats.RX,
				TX:    info.NetworkStats.TX,
			},
		})
	}

	return contracts.Heartbeat{
		NodeID:     c.NodeID,
		CPUUsage:   cpuUsage,
		MemUsage:   memUsage,
		DiskUsage:  diskUsage,
		Uptime:     int64(uptime),
		VMCount:    len(vmStates),
		ReportedAt: reportedAt,
		VMStates:   vmStates,
	}
}
