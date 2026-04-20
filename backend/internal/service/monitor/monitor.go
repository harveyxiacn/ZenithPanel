package monitor

import (
	"os"
	"sync"
	"time"

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

// cacheTTL controls how long a stats snapshot is reused. Frontend polls at ~5s,
// so a 3s window keeps the UI fresh while cutting gopsutil syscalls roughly in half.
const cacheTTL = 3 * time.Second

var (
	statsMu       sync.Mutex
	cachedStats   *SystemStats
	cachedAt      time.Time
	cachedHost    string
	cachedHostSet bool
)

// GetSystemStats retrieves current hardware telemetry (CPU, memory, disk, load, network).
// Results are cached for cacheTTL to reduce syscall pressure on low-spec VPS.
func GetSystemStats() (*SystemStats, error) {
	statsMu.Lock()
	if cachedStats != nil && time.Since(cachedAt) < cacheTTL {
		snap := *cachedStats
		statsMu.Unlock()
		return &snap, nil
	}
	statsMu.Unlock()

	stats := &SystemStats{}

	if cpuPercents, err := cpu.Percent(0, false); err == nil && len(cpuPercents) > 0 {
		stats.CPUPercent = cpuPercents[0]
	}
	if vmStat, err := mem.VirtualMemory(); err == nil {
		stats.MemTotal = vmStat.Total
		stats.MemUsed = vmStat.Used
		stats.MemPercent = vmStat.UsedPercent
	}
	if diskStat, err := disk.Usage("/"); err == nil {
		stats.DiskTotal = diskStat.Total
		stats.DiskUsed = diskStat.Used
		stats.DiskPercent = diskStat.UsedPercent
	}
	if uptime, err := host.Uptime(); err == nil {
		stats.UptimeSeconds = uptime
	}

	// Hostname effectively never changes for a running process; memoize it.
	statsMu.Lock()
	if !cachedHostSet {
		cachedHost, _ = os.Hostname()
		cachedHostSet = true
	}
	stats.Hostname = cachedHost
	statsMu.Unlock()

	if loadStat, err := load.Avg(); err == nil {
		stats.LoadAvg = []float64{loadStat.Load1, loadStat.Load5, loadStat.Load15}
	}
	if netCounters, err := net.IOCounters(false); err == nil && len(netCounters) > 0 {
		stats.NetIn = netCounters[0].BytesRecv
		stats.NetOut = netCounters[0].BytesSent
	}

	statsMu.Lock()
	cachedStats = stats
	cachedAt = time.Now()
	snap := *stats
	statsMu.Unlock()
	return &snap, nil
}
