package proxy

import (
	"fmt"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// StatsIdentities maps every client ID to the user name emitted into the
// engine configs (Xray `email`, sing-box user `name`). Engines report
// traffic per name, so the name must identify exactly one Client row.
//
// Emails are only unique per inbound (the same person usually has one row
// per protocol), so a bare email is used only when no other row shares it;
// otherwise the row gets "email#<client id>". Computed over all clients
// (enabled or not) so a client's identity doesn't change when another row
// is toggled.
func StatsIdentities(clients []model.Client) map[uint]string {
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

// identityOf returns the engine-facing user name for c.
func identityOf(ids map[uint]string, c model.Client) string {
	if id, ok := ids[c.ID]; ok {
		return id
	}
	return c.Email
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
