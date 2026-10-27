package proxy

import (
	"fmt"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// StatsIdentities maps every client ID to the canonical engine-visible name.
// Client emails are mutable and only unique within an inbound, so all new
// Xray and sing-box configurations use ClientIdentity instead.
func StatsIdentities(clients []model.Client) map[uint]string {
	out := make(map[uint]string, len(clients))
	for _, c := range clients {
		out[c.ID] = ClientIdentity(c.ID)
	}
	return out
}

// LegacyStatsIdentities returns the names emitted by panel versions before
// canonical client IDs. Keep this separate from StatsIdentities so accounting
// can recognize a still-running external proxy during a panel-only upgrade
// without ever emitting the legacy names in a newly generated configuration.
func LegacyStatsIdentities(clients []model.Client) map[uint]string {
	count := make(map[string]int, len(clients))
	for _, c := range clients {
		count[c.Email]++
	}
	out := make(map[uint]string, len(clients))
	for _, c := range clients {
		if count[c.Email] == 1 {
			out[c.ID] = c.Email
		} else {
			out[c.ID] = fmt.Sprintf("%s#%d", c.Email, c.ID)
		}
	}
	return out
}

// LoadStatsIdentities is StatsIdentities over every client in the DB.
func LoadStatsIdentities() map[uint]string {
	var clients []model.Client
	if config.DB != nil {
		config.DB.Select("id", "email").Find(&clients)
	}
	return StatsIdentities(clients)
}

// ClashAPIEnabled reports whether sing-box exposes its Clash API (bound to
// 127.0.0.1). It is on unless explicitly disabled: per-user traffic
// accounting and live rates for sing-box protocols depend on it.
func ClashAPIEnabled() bool {
	return config.GetSetting("singbox_clash_api_enabled") != "false"
}

// UserOutboundPrefix tags the per-user direct outbounds sing-box routes
// each authenticated user through; the Clash API reports the outbound in a
// connection's "chains", which is how traffic is attributed to users.
const UserOutboundPrefix = "user:"
