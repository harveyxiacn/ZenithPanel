package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/monitor"
	"gorm.io/gorm"
)

// BotPoller polls the Telegram getUpdates API in a background goroutine and
// dispatches commands sent by the configured chat owner. Commands from other
// chats are silently ignored.
type BotPoller struct {
	token    string
	chatID   string
	db       *gorm.DB
	onMutate func() // optional callback fired after mutating commands

	mu      sync.Mutex
	offset  int64
	running bool
	stopCh  chan struct{}
}

// NewBotPoller creates a poller bound to a specific Telegram bot token + chat.
// The optional onMutate callback fires after operations that change client
// state (e.g. traffic reset) so callers can invalidate downstream caches.
func NewBotPoller(token, chatID string, db *gorm.DB, onMutate func()) *BotPoller {
	return &BotPoller{
		token:    token,
		chatID:   chatID,
		db:       db,
		onMutate: onMutate,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the polling goroutine. Calling Start twice is a no-op.
func (b *BotPoller) Start() {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.mu.Unlock()

	go b.loop()
	log.Printf("[notify-bot] started polling for chat %s", b.chatID)
}

// Stop signals the polling goroutine to exit. Safe to call multiple times.
func (b *BotPoller) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return
	}
	b.running = false
	close(b.stopCh)
	b.stopCh = make(chan struct{})
	log.Printf("[notify-bot] stopped")
}

func (b *BotPoller) loop() {
	for {
		b.mu.Lock()
		stopCh := b.stopCh
		b.mu.Unlock()

		select {
		case <-stopCh:
			return
		default:
		}

		b.poll()
	}
}

// telegramUpdate matches the subset of getUpdates response we care about.
type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

func (b *BotPoller) poll() {
	q := url.Values{}
	q.Set("timeout", "25")
	if b.offset > 0 {
		q.Set("offset", strconv.FormatInt(b.offset, 10))
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", b.token, q.Encode())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		log.Printf("[notify-bot] getUpdates error: %v", err)
		time.Sleep(10 * time.Second)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.OK {
		log.Printf("[notify-bot] unexpected response: %s", string(body))
		time.Sleep(10 * time.Second)
		return
	}

	for _, u := range payload.Result {
		if u.UpdateID >= b.offset {
			b.offset = u.UpdateID + 1
		}
		if u.Message == nil {
			continue
		}
		// Only respond to the configured chat ID — this is the auth boundary.
		if strconv.FormatInt(u.Message.Chat.ID, 10) != b.chatID {
			continue
		}
		b.handleCommand(strings.TrimSpace(u.Message.Text))
	}
}

func (b *BotPoller) handleCommand(text string) {
	if text == "" {
		return
	}
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	var reply string
	switch cmd {
	case "/start", "/help":
		reply = "*ZenithPanel Bot*\n\n" +
			"Available commands:\n" +
			"`/status` — system metrics\n" +
			"`/clients` — top 5 clients by traffic\n" +
			"`/reset_traffic <email>` — zero a client's traffic\n"
	case "/status":
		reply = b.cmdStatus()
	case "/clients":
		reply = b.cmdClients()
	case "/reset_traffic":
		if len(parts) < 2 {
			reply = "Usage: `/reset_traffic <email>`"
		} else {
			reply = b.cmdResetTraffic(parts[1])
		}
	default:
		reply = "Unknown command. Send `/help` for available commands."
	}

	if err := sendTelegram(b.token, b.chatID, reply); err != nil {
		log.Printf("[notify-bot] failed to send reply: %v", err)
	}
}

func (b *BotPoller) cmdStatus() string {
	stats, err := monitor.GetSystemStats()
	if err != nil {
		return "Failed to read system stats."
	}
	uptime := time.Duration(stats.UptimeSeconds) * time.Second
	return fmt.Sprintf("*System Status*\n\nCPU: %.1f%%\nMem: %.1f%% (%d MB / %d MB)\nDisk: %.1f%%\nUptime: %s\nHost: `%s`",
		stats.CPUPercent,
		stats.MemPercent,
		stats.MemUsed/(1024*1024),
		stats.MemTotal/(1024*1024),
		stats.DiskPercent,
		uptime.Truncate(time.Second).String(),
		stats.Hostname,
	)
}

func (b *BotPoller) cmdClients() string {
	var enabled int64
	b.db.Model(&model.Client{}).Where("enable = ?", true).Count(&enabled)

	var topClients []model.Client
	b.db.Where("enable = ?", true).Order("(up_load + down_load) desc").Limit(5).Find(&topClients)

	sb := strings.Builder{}
	fmt.Fprintf(&sb, "*Clients*\n\nEnabled: %d\n\n*Top 5 by traffic:*\n", enabled)
	for i, c := range topClients {
		used := c.UpLoad + c.DownLoad
		fmt.Fprintf(&sb, "%d. `%s` — %.2f GB\n", i+1, c.Email, float64(used)/(1024*1024*1024))
	}
	if len(topClients) == 0 {
		sb.WriteString("(no clients yet)")
	}
	return sb.String()
}

func (b *BotPoller) cmdResetTraffic(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "Email is required."
	}
	var client model.Client
	if err := b.db.Where("email = ?", email).First(&client).Error; err != nil {
		return fmt.Sprintf("No client found with email `%s`.", email)
	}
	if err := b.db.Model(&client).Updates(map[string]any{"up_load": 0, "down_load": 0}).Error; err != nil {
		return fmt.Sprintf("Failed to reset traffic: %v", err)
	}
	if b.onMutate != nil {
		b.onMutate()
	}
	return fmt.Sprintf("✅ Reset traffic for `%s`.", email)
}
