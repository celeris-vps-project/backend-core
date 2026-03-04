package monitor

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"time"

	// 引入 gopsutil 相关的包
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

type Collector struct {
	NodeID string
	Driver vm.Hypervisor // 注入你上面定义的接口
}

func NewCollector(nodeID string, driver vm.Hypervisor) *Collector {
	return &Collector{
		NodeID: nodeID,
		Driver: driver,
	}
}

func (c *Collector) Collect() contracts.Heartbeat {
	// 1. CPU 使用率 (百分比)
	// 注意：这里传 0 表示非阻塞立即返回自上次调用以来的使用率。
	// 在探针中，建议在后台开个协程每秒调用 cpu.Percent(time.Second, false) 并缓存结果，
	// 否则在这里传 time.Second 会导致每次 Collect 阻塞 1 秒。
	var cpuUsage float64
	cpuPercents, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercents) > 0 {
		cpuUsage = cpuPercents[0]
	}

	// 2. 真实系统内存使用率 (百分比)
	var memUsage float64
	vMem, err := mem.VirtualMemory()
	if err == nil {
		memUsage = vMem.UsedPercent
	}

	// 3. 磁盘使用率 (百分比，这里以根目录 "/" 为例)
	var diskUsage float64
	dUsage, err := disk.Usage("/")
	if err == nil {
		diskUsage = dUsage.UsedPercent
	}

	// 4. 系统运行时间 (Uptime)
	var uptime uint64
	hostInfo, err := host.Info()
	if err == nil {
		uptime = hostInfo.Uptime // 返回的是秒数
	}

	return contracts.Heartbeat{
		NodeID:    c.NodeID,
		CPUUsage:  cpuUsage,
		MemUsage:  memUsage,
		DiskUsage: diskUsage,
		Uptime:    int64(uptime),
		VMCount: func() int {
			info, _ := c.Driver.List()
			return len(info)
		}(), // 通过接口获取 VM 列表并计算数量
		ReportedAt: time.Now().Format(time.RFC3339),
	}
}
