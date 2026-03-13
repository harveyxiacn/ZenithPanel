package monitor

import (
	"os"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type SystemStats struct {
	CPUPercent    float64   `json:"cpu_percent"`
	MemTotal      uint64    `json:"mem_total"`
	MemUsed       uint64    `json:"mem_used"`
	MemPercent    float64   `json:"mem_percent"`
	DiskTotal     uint64    `json:"disk_total"`
	DiskUsed      uint64    `json:"disk_used"`
	DiskPercent   float64   `json:"disk_percent"`
	UptimeSeconds uint64    `json:"uptime_seconds"`
	Hostname      string    `json:"hostname"`
	LoadAvg       []float64 `json:"load_avg"`
	NetIn         uint64    `json:"net_in"`
	NetOut        uint64    `json:"net_out"`
}

// GetSystemStats retrieves current hardware telemetry like CPU, Memory, Disk
func GetSystemStats() (*SystemStats, error) {
	stats := &SystemStats{}

	// CPU
	cpuPercents, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercents) > 0 {
		stats.CPUPercent = cpuPercents[0]
	}

	// Memory
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		stats.MemTotal = vmStat.Total
		stats.MemUsed = vmStat.Used
		stats.MemPercent = vmStat.UsedPercent
	}

	// Disk
	diskStat, err := disk.Usage("/")
	if err == nil {
		stats.DiskTotal = diskStat.Total
		stats.DiskUsed = diskStat.Used
		stats.DiskPercent = diskStat.UsedPercent
	}

	// Uptime
	uptime, err := host.Uptime()
	if err == nil {
		stats.UptimeSeconds = uptime
	}

	// Hostname
	stats.Hostname, _ = os.Hostname()

	// Load average
	loadStat, err := load.Avg()
	if err == nil {
		stats.LoadAvg = []float64{loadStat.Load1, loadStat.Load5, loadStat.Load15}
	}

	// Network I/O (aggregate all interfaces)
	netCounters, err := net.IOCounters(false)
	if err == nil && len(netCounters) > 0 {
		stats.NetIn = netCounters[0].BytesRecv
		stats.NetOut = netCounters[0].BytesSent
	}

	return stats, nil
}
