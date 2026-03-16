package model

import (
	"time"

	"gorm.io/gorm"
)

// Setting stores key-value configuration that persists across restarts
type Setting struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Key   string `gorm:"uniqueIndex;not null" json:"key"`
	Value string `gorm:"type:text;not null" json:"value"`
}

// AdminUser stores the panel administrator credentials
type AdminUser struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	Username      string         `gorm:"uniqueIndex;not null" json:"username"`
	PasswordHash  string         `gorm:"not null" json:"-"`
	TOTPSecret    string         `gorm:"default:''" json:"-"`
	TOTPEnabled   bool           `gorm:"default:false" json:"totp_enabled"`
	RecoveryCodes string         `gorm:"type:text;default:''" json:"-"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// CronJob stores scheduled tasks
type CronJob struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	Schedule  string    `gorm:"not null" json:"schedule"`
	Command   string    `gorm:"not null" json:"command"`
	Enable    bool      `gorm:"default:true" json:"enable"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
