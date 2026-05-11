// Package traffic provides live observability into who is moving bytes on the
// host: proxy subscription users on one side (sourced from Sing-box's Clash
// API) and OS processes/network interfaces on the other (sourced from
// gopsutil). The data is computed continuously in-process and exposed as
// point-in-time snapshots plus a short rolling history.
package traffic

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

// ProxyUserSample reports live and cumulative traffic for one proxy client.
// Rates are computed from the delta between two Clash API polls; totals come
// from the Client table (kept in sync by the existing traffic-accounting code).
type ProxyUserSample struct {
	Email           string   `json:"email"`
	UploadRateBps   uint64   `json:"upload_rate_bps"`
	DownloadRateBps uint64   `json:"download_rate_bps"`
	ActiveConns     int      `json:"active_conns"`
	UploadTotal     int64    `json:"upload_total"`
	DownloadTotal   int64    `json:"download_total"`
	TopTargets      []string `json:"top_targets"`
}

// NICSample reports the network rate for a single OS network interface, plus
// the cumulative byte counters straight from gopsutil.
type NICSample struct {
	Name       string `json:"name"`
	InRateBps  uint64 `json:"in_rate_bps"`
	OutRateBps uint64 `json:"out_rate_bps"`
	TotalIn    uint64 `json:"total_in"`
	TotalOut   uint64 `json:"total_out"`
}

// ProcessSample reports a process that currently has open network sockets.
// Per-process bandwidth measurement is not exposed (would require eBPF or
// nethogs); active connection count is a useful proxy.
type ProcessSample struct {
	PID          int      `json:"pid"`
	Name         string   `json:"name"`
	User         string   `json:"user"`
	Command      string   `json:"command"`
	ActiveConns  int      `json:"active_conns"`
	ListenPorts  []int    `json:"listen_ports"`
	Destinations []string `json:"destinations"`
}

// Snapshot is the unified view returned by the monitor at one point in time.
type Snapshot struct {
	At         time.Time         `json:"at"`
	ProxyUsers []ProxyUserSample `json:"proxy_users"`
	NICs       []NICSample       `json:"nics"`
	Processes  []ProcessSample   `json:"processes"`
	// Source-level errors are surfaced so the UI can show "Clash API not
	// running" without the whole snapshot looking broken.
	ProxyError   string `json:"proxy_error,omitempty"`
	SystemError  string `json:"system_error,omitempty"`
}

const (
	// proxyTickInterval is the cadence for refreshing Clash API connections.
	// 2s feels live in the UI without flooding sing-box's HTTP listener.
	proxyTickInterval = 2 * time.Second
	// processTickInterval is the cadence for enumerating OS processes —
	// expensive (one syscall per /proc/<pid>) so we keep it on a slower clock.
	processTickInterval = 5 * time.Second
	// historyCap bounds the rolling history. 60 samples × 2s = last 2 min.
	historyCap = 60
)

// Monitor coordinates the per-source samplers and exposes the latest snapshot
// plus a short rolling history. Construct via NewMonitor and call Start once;
// the goroutine exits when the supplied context is cancelled.
type Monitor struct {
	sm *proxy.SingboxManager

	mu      sync.RWMutex
	latest  Snapshot
	history []Snapshot

	proxyAgg   *proxyAggregator
	sysAgg     *systemAggregator
	lastProcAt time.Time
}

// NewMonitor wires up the monitor with the running Sing-box manager. The
// manager is used to discover the Clash API port and check whether sing-box
// is currently running before we attempt to hit the API.
func NewMonitor(sm *proxy.SingboxManager) *Monitor {
	return &Monitor{
		sm:       sm,
		proxyAgg: newProxyAggregator(),
		sysAgg:   newSystemAggregator(),
	}
}

// Start kicks off the background sampler. It returns immediately; the loop
// runs until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	ticker := time.NewTicker(proxyTickInterval)
	defer ticker.Stop()
	// Prime an initial sample so /traffic/live doesn't return empty for the
	// first two seconds after boot.
	m.tick(true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(false)
		}
	}
}

// tick gathers one full snapshot. The process scan is skipped on most ticks
// because enumerating /proc is expensive; we reuse the previous process list
// in between scans so the UI stays populated.
func (m *Monitor) tick(force bool) {
	now := time.Now()
	users, perr := m.proxyAgg.sample(m.sm)
	nics, sysErr := m.sysAgg.sampleNICs()

	doProcs := force || now.Sub(m.lastProcAt) >= processTickInterval
	var procs []ProcessSample
	if doProcs {
		procs, _ = m.sysAgg.sampleProcesses()
		m.lastProcAt = now
	} else {
		m.mu.RLock()
		procs = m.latest.Processes
		m.mu.RUnlock()
	}

	snap := Snapshot{
		At:         now,
		ProxyUsers: users,
		NICs:       nics,
		Processes:  procs,
	}
	if perr != nil {
		snap.ProxyError = perr.Error()
	}
	if sysErr != nil {
		snap.SystemError = sysErr.Error()
	}

	m.mu.Lock()
	m.latest = snap
	m.history = append(m.history, snap)
	if len(m.history) > historyCap {
		// Trim from the head; cheap since we don't hold a reference window.
		drop := len(m.history) - historyCap
		m.history = append(m.history[:0:0], m.history[drop:]...)
	}
	m.mu.Unlock()
}

// Latest returns a copy of the most recent snapshot. Safe to call concurrently.
func (m *Monitor) Latest() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

// History returns up to N most recent snapshots, oldest first. Pass 0 to mean
// "everything we have."
func (m *Monitor) History(maxSeconds int) []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.history) == 0 {
		return nil
	}
	if maxSeconds <= 0 {
		out := make([]Snapshot, len(m.history))
		copy(out, m.history)
		return out
	}
	cutoff := time.Now().Add(-time.Duration(maxSeconds) * time.Second)
	idx := sort.Search(len(m.history), func(i int) bool {
		return !m.history[i].At.Before(cutoff)
	})
	out := make([]Snapshot, len(m.history)-idx)
	copy(out, m.history[idx:])
	return out
}
