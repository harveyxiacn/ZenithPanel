package model

import (
	"time"

	"gorm.io/gorm"
)

// Inbound represents a proxy entry point (e.g., VLESS, VMess, Trojan, Hysteria2)
type Inbound struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Tag       string         `gorm:"uniqueIndex;not null" json:"tag"` // Unique identifier for the inbound
	Protocol  string         `gorm:"not null" json:"protocol"`        // vless, vmess, trojan, hysteria2, wireguard
	Listen    string         `gorm:"default:'0.0.0.0'" json:"listen"` // Bind address
	Port      int            `gorm:"not null" json:"port"`            // Listening port
	Network   string         `gorm:"default:'tcp'" json:"network"`    // tcp, wss, grpc
	Settings  string         `gorm:"type:text;not null" json:"settings"` // JSON string of protocol specific settings
	Stream    string         `gorm:"type:text" json:"stream"`         // JSON string of TLS/Transport settings
	Enable    bool           `gorm:"default:true" json:"enable"`      // Is inbound active
	Remark    string         `json:"remark"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Client represents a proxy user with traffic tracking
type Client struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	InboundID uint           `gorm:"index;not null" json:"inbound_id"` // Matches Inbound.ID
	Email     string         `gorm:"uniqueIndex;not null" json:"email"` // User identifier (standard in xray/v2ray)
	UUID      string         `gorm:"not null" json:"uuid"`              // Password / UUID for the user
	Enable    bool           `gorm:"default:true" json:"enable"`
	UpLoad    int64          `gorm:"default:0" json:"up_load"`          // Bytes uploaded
	DownLoad  int64          `gorm:"default:0" json:"down_load"`        // Bytes downloaded
	Total     int64          `gorm:"default:0" json:"total"`            // Traffic limit (0 = unlimited)
	ExpiryTime int64         `gorm:"default:0" json:"expiry_time"`      // Unix timestamp (0 = never expires)
	Remark    string         `json:"remark"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// RoutingRule represents custom routing (e.g., block ads, route AI to warp)
type RoutingRule struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	RuleTag   string         `gorm:"not null" json:"rule_tag"`        // Description
	Domain    string         `gorm:"type:text" json:"domain"`         // Comma separated domains or geosite
	IP        string         `gorm:"type:text" json:"ip"`             // Comma separated IP CIDR or geoip
	Port      string         `json:"port"`                            // External port match
	OutboundTag string       `gorm:"not null" json:"outbound_tag"`    // Which outbound to route to (direct, block, warp)
	Enable    bool           `gorm:"default:true" json:"enable"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
