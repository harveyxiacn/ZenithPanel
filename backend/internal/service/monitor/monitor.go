package monitor

import (
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

type SystemStats struct {
	CPUUsage  float64 `json:"cpu_usage"`
	MemTotal  uint64  `json:"mem_total"`
	MemUsed   uint64  `json:"mem_used"`
	MemUsage  float64 `json:"mem_usage"`
	DiskTotal uint64  `json:"disk_total"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskUsage float64 `json:"disk_usage"`
}

// GetSystemStats retrieves current hardware telemetry like CPU, Memory, Disk
func GetSystemStats() (*SystemStats, error) {
	stats := &SystemStats{}

	// CPU
	cpuPercents, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercents) > 0 {
		stats.CPUUsage = cpuPercents[0]
	}

	// Memory
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		stats.MemTotal = vmStat.Total
		stats.MemUsed = vmStat.Used
		stats.MemUsage = vmStat.UsedPercent
	}

	// Disk
	diskStat, err := disk.Usage("/")
	if err == nil {
		stats.DiskTotal = diskStat.Total
		stats.DiskUsed = diskStat.Used
		stats.DiskUsage = diskStat.UsedPercent
	}

	return stats, nil
}
