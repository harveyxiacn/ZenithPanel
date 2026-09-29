package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/firewall"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/subserver"
)

// subSrv is the optional public subscription listener (see subserver).
var subSrv = subserver.New(func(c *gin.Context) {
	if !getSubLimiter(c.ClientIP()).Allow() {
		c.Status(http.StatusTooManyRequests)
		return
	}
	sub.GenerateSubscription(c)
})

// StartSubscriptionServer starts the public subscription listener at boot
// when enabled in settings.
func StartSubscriptionServer() {
	if cfg := subserver.Load(); cfg.Enabled {
		if err := subSrv.Apply(cfg); err != nil {
			log.Printf("subscription server: %v", err)
		}
	}
}

// subFallbackHost is the host share links use when neither an explicit
// public host nor a certificate name is available — the same rule as the
// subscription generator (request host, or a configured node address when
// the admin is on loopback / the unix socket).
func subFallbackHost(c *gin.Context) string {
	return sub.ServerAddrFor(c)
}

func registerSubscriptionServerRoutes(g *gin.RouterGroup, panelPort func() string) {
	g.GET("/admin/subscription", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": subSrv.Status(subFallbackHost(c))})
	})

	g.PUT("/admin/subscription", func(c *gin.Context) {
		var req struct {
			Enabled          bool   `json:"enabled"`
			Port             int    `json:"port"`
			PublicHost       string `json:"public_host"`
			RegenerateSecret bool   `json:"regenerate_secret"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
			return
		}
		cfg := subserver.Load()
		cfg.Enabled = req.Enabled
		if req.Port != 0 {
			cfg.Port = req.Port
		}
		cfg.PublicHost = strings.TrimSpace(req.PublicHost)
		if cfg.Port < 1 || cfg.Port > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Port must be 1-65535"})
			return
		}
		if strconv.Itoa(cfg.Port) == panelPort() {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Port is used by the panel itself"})
			return
		}
		var clash int64
		config.DB.Model(&model.Inbound{}).Where("port = ? AND enable = ?", cfg.Port, true).Count(&clash)
		if clash > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Port is used by an inbound"})
			return
		}
		if cfg.Secret == "" || req.RegenerateSecret {
			cfg.Secret = subserver.NewSecret()
		}
		if err := subserver.Save(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to save settings"})
			return
		}
		if err := subSrv.Apply(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
			return
		}

		var notes []string
		if cfg.Enabled {
			port := strconv.Itoa(cfg.Port)
			if !firewallAllows(port) {
				if err := firewall.AddRule("tcp", port, "ACCEPT", "", firewall.ManagedCommentPrefix+"subscription"); err != nil {
					notes = append(notes, "Could not open the port in the host firewall: "+err.Error())
				} else {
					notes = append(notes, fmt.Sprintf("Opened TCP %s in the host firewall.", port))
				}
			}
			notes = append(notes, fmt.Sprintf("If your cloud provider has a security group / security list, allow inbound TCP %d there too.", cfg.Port))
		} else {
			EnsurePanelPortsOpen() // closes the port the listener no longer uses
		}
		recordAudit(c, "subscription_server.update", fmt.Sprintf("enabled=%v port=%d", cfg.Enabled, cfg.Port))
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Saved", "data": gin.H{
			"status": subSrv.Status(subFallbackHost(c)),
			"notes":  notes,
		}})
	})
}

// firewallAllows reports whether an INPUT ACCEPT rule for tcp/port exists.
func firewallAllows(port string) bool {
	rules, err := firewall.ListRules()
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Port == port && r.Target == "ACCEPT" {
			return true
		}
	}
	return false
}

// inboundFirewallProtos returns the L4 protocols an inbound listens on.
func inboundFirewallProtos(in model.Inbound) []string {
	switch in.Protocol {
	case "hysteria2", "tuic":
		return []string{"udp"}
	case "shadowsocks":
		return []string{"tcp", "udp"}
	}
	return []string{"tcp"}
}

// EnsurePanelPortsOpen opens (if the host firewall blocks by default) the
// ports of every enabled inbound plus the public subscription listener.
// Called at startup — iptables rules added by the containerised panel don't
// survive a host reboot — and after each proxy apply, so an enabled node is
// actually reachable. The admin panel port is deliberately left alone.
func EnsurePanelPortsOpen() {
	var inbounds []model.Inbound
	config.DB.Where("enable = ?", true).Find(&inbounds)
	keep := map[string]bool{}
	for _, in := range inbounds {
		for _, proto := range inboundFirewallProtos(in) {
			port := strconv.Itoa(in.Port)
			keep[proto+"/"+port] = true
			if added, err := firewall.EnsureOpen(proto, port, firewall.ManagedCommentPrefix+in.Tag); err != nil {
				log.Printf("firewall: ensure %s/%d: %v", proto, in.Port, err)
				return // iptables unavailable — nothing else will work either
			} else if added {
				log.Printf("firewall: opened %s/%d for inbound %s", proto, in.Port, in.Tag)
			}
		}
	}
	if cfg := subserver.Load(); cfg.Enabled {
		port := strconv.Itoa(cfg.Port)
		keep["tcp/"+port] = true
		if added, err := firewall.EnsureOpen("tcp", port, firewall.ManagedCommentPrefix+"subscription"); err == nil && added {
			log.Printf("firewall: opened tcp/%d for the subscription server", cfg.Port)
		}
	}
	// Close ports the panel opened for listeners that no longer exist
	// (deleted / disabled / moved nodes). Only panel-labelled rules.
	if n, err := firewall.PruneManaged(keep); err != nil {
		log.Printf("firewall: prune: %v", err)
	} else if n > 0 {
		log.Printf("firewall: closed %d port(s) no longer used by any node", n)
	}
}
