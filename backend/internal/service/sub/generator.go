package sub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// GenerateSubscription creates a base64 encoded string of all V2ray/Xray links
// or a Clash/Mihomo YAML configuration based on the User-Agent or query parameter
func GenerateSubscription(c *gin.Context) {
	uuid := c.Param("uuid")
	format := c.Query("format") // empty, "clash", or "base64"

	// Find user
	var client model.Client
	if err := config.DB.Where("uuid = ?", uuid).First(&client).Error; err != nil {
		c.String(404, "User not found")
		return
	}

	if !client.Enable {
		c.String(403, "Account disabled")
		return
	}
	
	if client.ExpiryTime > 0 && time.Now().Unix() > client.ExpiryTime {
		c.String(403, "Account expired")
		return
	}

	// Fetch all enabled inbounds
	var inbounds []model.Inbound
	config.DB.Where("enable = ?", true).Find(&inbounds)

	// Determine format (Auto-detect Clash from User-Agent if not explicitly requested)
	userAgent := c.GetHeader("User-Agent")
	if format == "" {
		if detectClashClient(userAgent) {
			format = "clash"
		} else {
			format = "base64"
		}
	}

	if format == "clash" {
		yamlStr := buildClashConfig(inbounds, client)
		c.String(200, yamlStr)
		return
	}

	// Default: Standard Base64 encoded links
	links := buildBase64Links(inbounds, client)
	c.String(200, links)
}

func detectClashClient(ua string) bool {
	lowerUA := strings.ToLower(ua)
	clashKeywords := []string{"clash", "mihomo", "stash", "surge", "quantumult", "shadowrocket", "loon"}
	for _, kw := range clashKeywords {
		if strings.Contains(lowerUA, kw) {
			return true
		}
	}
	return false
}

func buildClashConfig(inbounds []model.Inbound, client model.Client) string {
	// Simple stub for Clash Meta (Mihomo) configuration YAML
	// In reality, this requires parsing inbound settings and generating YAML
	clashConfig := `port: 7890
socks-port: 7891
allow-lan: true
mode: rule
log-level: info
proxies:
`
	for _, in := range inbounds {
		clashConfig += fmt.Sprintf("  - name: \"%s\"\n", in.Tag)
		clashConfig += fmt.Sprintf("    type: %s\n", in.Protocol)
		clashConfig += fmt.Sprintf("    server: %s\n", "YOUR_SERVER_IP")
		clashConfig += fmt.Sprintf("    port: %d\n", in.Port)
		clashConfig += fmt.Sprintf("    uuid: %s\n", client.UUID)
		// Add TLS + Transport settings logic here
	}

	clashConfig += `
proxy-groups:
  - name: PROXY
    type: select
    proxies:
`
	for _, in := range inbounds {
		clashConfig += fmt.Sprintf("      - \"%s\"\n", in.Tag)
	}

	clashConfig += `
rules:
  - MATCH,PROXY
`
	return clashConfig
}

func buildBase64Links(inbounds []model.Inbound, client model.Client) string {
	var links string
	for _, in := range inbounds {
		// Example: vmess://... or vless://...
		link := fmt.Sprintf("%s://%s@SERVER_IP:%d?security=tls#%s\n", in.Protocol, client.UUID, in.Port, in.Tag)
		links += link
	}
	return base64.StdEncoding.EncodeToString([]byte(links))
}
