package monitor

import (
	"sync"
	"time"
)

// NetworkSample holds a single point-in-time network I/O reading.
type NetworkSample struct {
	Timestamp int64  `json:"ts"`   // Unix milliseconds
	InBytes   uint64 `json:"in"`   // cumulative bytes received (from gopsutil)
	OutBytes  uint64 `json:"out"`  // cumulative bytes sent
	InRate    uint64 `json:"in_rate"`  // bytes/s since previous sample
	OutRate   uint64 `json:"out_rate"` // bytes/s since previous sample
}

const ringSize = 60 // 60 samples, collected ~every 5 seconds → ~5 minutes of history

var (
	ringMu   sync.RWMutex
	ring     [ringSize]NetworkSample
	ringHead int  // next write position
	ringFull bool // true once the ring has been filled once
	lastIn   uint64
	lastOut  uint64
	lastTime time.Time
)

// RecordNetworkSample appends a new sample derived from the latest system stats
// to the ring buffer. Should be called once per monitor poll cycle.
func RecordNetworkSample(netIn, netOut uint64) {
	ringMu.Lock()
	defer ringMu.Unlock()

	now := time.Now()
	sample := NetworkSample{
		Timestamp: now.UnixMilli(),
		InBytes:   netIn,
		OutBytes:  netOut,
	}

	if !lastTime.IsZero() {
		dt := now.Sub(lastTime).Seconds()
		if dt > 0 && netIn >= lastIn && netOut >= lastOut {
			sample.InRate = uint64(float64(netIn-lastIn) / dt)
			sample.OutRate = uint64(float64(netOut-lastOut) / dt)
		}
	}
	lastIn = netIn
	lastOut = netOut
	lastTime = now

	ring[ringHead] = sample
	ringHead = (ringHead + 1) % ringSize
	if ringHead == 0 {
		ringFull = true
	}
}

// GetNetworkHistory returns the ring buffer contents in chronological order.
func GetNetworkHistory() []NetworkSample {
	ringMu.RLock()
	defer ringMu.RUnlock()

	if !ringFull && ringHead == 0 {
		return nil
	}

	var result []NetworkSample
	if ringFull {
		for i := ringHead; i < ringSize; i++ {
			result = append(result, ring[i])
		}
		for i := 0; i < ringHead; i++ {
			result = append(result, ring[i])
		}
	} else {
		result = make([]NetworkSample, ringHead)
		copy(result, ring[:ringHead])
	}
	return result
}
