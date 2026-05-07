package notify

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// loadConfig reads notification settings from the DB settings table.
func loadConfig(db *gorm.DB) Config {
	get := func(key string) string {
		var s model.Setting
		db.Where("key = ?", key).First(&s)
		return s.Value
	}
	parseBool := func(v string) bool { b, _ := strconv.ParseBool(v); return b }

	return Config{
		TelegramToken:      get("notify_telegram_token"),
		TelegramChatID:     get("notify_telegram_chat_id"),
		WebhookURL:         get("notify_webhook_url"),
		EnableExpiringSoon: parseBool(get("notify_enable_expiring_soon")),
		EnableExpired:      parseBool(get("notify_enable_expired")),
		EnableTrafficLimit: parseBool(get("notify_enable_traffic_limit")),
		EnableProxyCrashed: parseBool(get("notify_enable_proxy_crashed")),
	}
}

// RunClientChecks fires expiry and traffic-limit notifications for all enabled clients.
// It is safe to call repeatedly — it does not track previous runs; the operator
// should configure an appropriate check interval (e.g. every 6 hours).
func RunClientChecks(db *gorm.DB) {
	cfg := loadConfig(db)
	if cfg.TelegramToken == "" && cfg.WebhookURL == "" {
		return // nothing configured, skip
	}

	var clients []model.Client
	if err := db.Where("enable = ?", true).Find(&clients).Error; err != nil {
		log.Printf("notify: failed to query clients: %v", err)
		return
	}

	now := time.Now().Unix()
	soon := now + 3*24*60*60 // 3 days from now

	for _, c := range clients {
		// Expiry checks
		if c.ExpiryTime > 0 {
			if c.ExpiryTime <= now {
				Send(cfg, Event{
					Type:    EventClientExpired,
					Message: fmt.Sprintf("Client *%s* has expired.", c.Email),
				})
			} else if c.ExpiryTime <= soon {
				daysLeft := (c.ExpiryTime - now) / 86400
				Send(cfg, Event{
					Type:    EventClientExpiringSoon,
					Message: fmt.Sprintf("Client *%s* expires in %d day(s).", c.Email, daysLeft),
				})
			}
		}

		// Traffic limit checks (>90%)
		if c.Total > 0 {
			used := c.UpLoad + c.DownLoad
			if used > 0 && float64(used)/float64(c.Total) >= 0.90 {
				pct := int(float64(used) / float64(c.Total) * 100)
				Send(cfg, Event{
					Type:    EventTrafficLimitReached,
					Message: fmt.Sprintf("Client *%s* has used %d%% of their traffic limit.", c.Email, pct),
				})
			}
		}
	}
}
