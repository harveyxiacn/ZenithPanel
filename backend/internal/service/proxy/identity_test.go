package proxy

import (
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func TestStatsIdentities(t *testing.T) {
	clients := []model.Client{
		{ID: 1, Email: "solo"},
		{ID: 2, Email: "dup"},
		{ID: 3, Email: "dup"},
	}
	got := StatsIdentities(clients)
	want := map[uint]string{1: "solo", 2: "dup#2", 3: "dup#3"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("identity(%d) = %q, want %q", id, got[id], w)
		}
	}
}
