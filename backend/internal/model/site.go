package model

import (
	"time"

	"gorm.io/gorm"
)

// Site represents a virtual host managed by the built-in web server.
// The web server serves sites on ports 80/443 independently of the panel port.
type Site struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Name          string         `gorm:"uniqueIndex;not null" json:"name"`
	Domain        string         `gorm:"not null" json:"domain"`
	Type          string         `gorm:"not null" json:"type"`          // "reverse_proxy"|"static"|"redirect"
	UpstreamURL   string         `json:"upstream_url"`                  // http://127.0.0.1:3000
	RootPath      string         `json:"root_path"`                     // /var/www/mysite
	RedirectURL   string         `json:"redirect_url"`                  // https://example.com
	TLSMode       string         `gorm:"default:'none'" json:"tls_mode"` // "none"|"acme"|"custom"
	CertPath      string         `json:"cert_path"`
	KeyPath       string         `json:"key_path"`
	TLSEmail      string         `json:"tls_email"` // for ACME registration
	CustomHeaders string         `gorm:"type:text" json:"custom_headers"` // JSON [{key,value}] pairs
	Enable        bool           `gorm:"default:true" json:"enable"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}
