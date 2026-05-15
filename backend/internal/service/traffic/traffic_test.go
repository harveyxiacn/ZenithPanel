package traffic

import (
	"context"
	"testing"
	"time"
)

func TestFlattenSortsByLiveRateThenTotalThenEmail(t *testing.T) {
	in := map[string]*ProxyUserSample{
		"c@x": {Email: "c@x", UploadRateBps: 0, DownloadRateBps: 0, UploadTotal: 100, DownloadTotal: 0},
		"a@x": {Email: "a@x", UploadRateBps: 1, DownloadRateBps: 0},
		"b@x": {Email: "b@x", UploadRateBps: 5, DownloadRateBps: 5},
		"d@x": {Email: "d@x", UploadRateBps: 0, DownloadRateBps: 0, UploadTotal: 50, DownloadTotal: 50},
	}
	out := flatten(in)
	// b has highest rate; a is second; c and d have zero rate so totals
	// decide — c has 100, d has 100 → tie → alphabetical → c then d.
	wantOrder := []string{"b@x", "a@x", "c@x", "d@x"}
	for i, w := range wantOrder {
		if out[i].Email != w {
			t.Errorf("position %d: want %s, got %s (full order: %#v)", i, w, out[i].Email, emails(out))
		}
	}
}

func TestSkipNICFiltersNoise(t *testing.T) {
	cases := map[string]bool{
		"lo":        true,
		"docker0":   true,
		"br-abc":    true,
		"veth1234":  true,
		"cni0":      true,
		"flannel.1": true,
		"eth0":      false,
		"ens18":     false,
		"wlp3s0":    false,
	}
	for name, want := range cases {
		if got := skipNIC(name); got != want {
			t.Errorf("skipNIC(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTruncateRespectsLimit(t *testing.T) {
	long := "abcdefghij"
	if got := truncate(long, 5); got != "abcde…" {
		t.Errorf("truncate long: got %q", got)
	}
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate short should be untouched, got %q", got)
	}
}

func TestPickTargetPrefersHostOverIP(t *testing.T) {
	host := pickTarget(clashConn{Metadata: clashConnMetadata{Host: "example.com", DestinationIP: "1.2.3.4"}})
	if host != "example.com" {
		t.Errorf("expected host preferred, got %q", host)
	}
	ip := pickTarget(clashConn{Metadata: clashConnMetadata{DestinationIP: "1.2.3.4"}})
	if ip != "1.2.3.4" {
		t.Errorf("expected ip fallback, got %q", ip)
	}
}

func TestMonitorHistoryRingCapsAtHistoryCap(t *testing.T) {
	m := NewMonitor(nil)
	// Seed history past the cap by tick()-ing directly. tick() reads
	// gopsutil + clash; with nil sm both produce errors but still record
	// a (mostly empty) sample, which is fine for the ring-buffer test.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	for i := 0; i < historyCap+10; i++ {
		m.tick(false)
	}
	hist := m.History(0)
	if len(hist) != historyCap {
		t.Fatalf("expected history bounded at %d, got %d", historyCap, len(hist))
	}
}

func TestMonitorHistoryByWindow(t *testing.T) {
	m := NewMonitor(nil)
	// Pre-seed history with synthetic timestamps so we can assert windowing.
	now := time.Now()
	m.mu.Lock()
	m.history = []Snapshot{
		{At: now.Add(-300 * time.Second)},
		{At: now.Add(-200 * time.Second)},
		{At: now.Add(-30 * time.Second)},
		{At: now.Add(-5 * time.Second)},
	}
	m.mu.Unlock()

	got := m.History(60)
	if len(got) != 2 {
		t.Fatalf("expected last-60s window to contain 2 samples, got %d", len(got))
	}
	if got[0].At.After(got[1].At) {
		t.Errorf("history not oldest-first")
	}
}

func emails(s []ProxyUserSample) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = v.Email
	}
	return out
}
