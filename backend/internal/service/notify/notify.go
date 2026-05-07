// Package notify delivers panel event notifications via Telegram Bot API or
// a user-configured generic webhook.
//
// Usage:
//
//	cfg := loadNotifyConfig()   // read from DB settings
//	notify.Send(cfg, notify.Event{Type: notify.EventProxyCoreCrashed, Message: "..."})
//	notify.RunClientChecks(db)  // called periodically from a background goroutine
// a user-configured generic webhook. All delivery errors are logged and
// silently discarded so notification failures never affect core operations.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// EventType identifies what happened.
type EventType string

const (
	EventClientExpiringSoon  EventType = "client_expiring_soon"
	EventClientExpired       EventType = "client_expired"
	EventTrafficLimitReached EventType = "traffic_limit_reached" // >90%
	EventProxyCoreCrashed    EventType = "proxy_core_crashed"
)

// Event is a notification payload.
type Event struct {
	Type    EventType
	Message string // human-readable description
}

// Config holds the notification delivery settings persisted in panel settings table.
type Config struct {
	TelegramToken  string // bot token
	TelegramChatID string // chat id (user or group)
	WebhookURL     string // generic HTTP POST endpoint

	// per-event toggles (all enabled by default when non-empty)
	EnableExpiringSoon  bool
	EnableExpired       bool
	EnableTrafficLimit  bool
	EnableProxyCrashed  bool
}

// Send dispatches an event according to the supplied config. Errors are logged
// but never returned — callers should treat notification as fire-and-forget.
func Send(cfg Config, ev Event) {
	if !isEnabled(cfg, ev.Type) {
		return
	}

	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		if err := sendTelegram(cfg.TelegramToken, cfg.TelegramChatID, formatTelegramMessage(ev)); err != nil {
			log.Printf("notify: Telegram delivery failed for %s: %v", ev.Type, err)
		}
	}

	if cfg.WebhookURL != "" {
		if err := sendWebhook(cfg.WebhookURL, ev); err != nil {
			log.Printf("notify: webhook delivery failed for %s: %v", ev.Type, err)
		}
	}
}

// SendTest sends a test message to verify delivery settings.
func SendTest(cfg Config) error {
	ev := Event{
		Type:    "test",
		Message: "ZenithPanel notification test — delivery is working.",
	}
	var errs []string
	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		if err := sendTelegram(cfg.TelegramToken, cfg.TelegramChatID, formatTelegramMessage(ev)); err != nil {
			errs = append(errs, fmt.Sprintf("Telegram: %v", err))
		}
	}
	if cfg.WebhookURL != "" {
		if err := sendWebhook(cfg.WebhookURL, ev); err != nil {
			errs = append(errs, fmt.Sprintf("Webhook: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func isEnabled(cfg Config, t EventType) bool {
	switch t {
	case EventClientExpiringSoon:
		return cfg.EnableExpiringSoon
	case EventClientExpired:
		return cfg.EnableExpired
	case EventTrafficLimitReached:
		return cfg.EnableTrafficLimit
	case EventProxyCoreCrashed:
		return cfg.EnableProxyCrashed
	}
	return true // unknown event types always sent
}

func formatTelegramMessage(ev Event) string {
	emoji := map[EventType]string{
		EventClientExpiringSoon:  "⏰",
		EventClientExpired:       "🔴",
		EventTrafficLimitReached: "📊",
		EventProxyCoreCrashed:    "💥",
	}
	e := emoji[ev.Type]
	if e == "" {
		e = "🔔"
	}
	return fmt.Sprintf("%s *ZenithPanel*\n\n%s", e, ev.Message)
}

func sendTelegram(token, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	return postJSON(url, body)
}

func sendWebhook(webhookURL string, ev Event) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"event":   string(ev.Type),
		"message": ev.Message,
		"ts":      time.Now().Unix(),
	})
	return postJSON(webhookURL, payload)
}

func postJSON(url string, body []byte) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
