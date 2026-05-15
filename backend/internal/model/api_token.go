package model

import (
	"time"

	"gorm.io/gorm"
)

// ApiToken stores a long-lived, scoped credential used by the headless CLI
// (`zenithctl`) and automation. The plaintext token is only known to the
// caller; the DB persists sha256(plaintext) so a leaked DB does not yield
// usable credentials.
type ApiToken struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	Name       string `gorm:"uniqueIndex;not null" json:"name"`
	TokenHash  string `gorm:"not null;index" json:"-"`
	Scopes     string `gorm:"default:'*'" json:"scopes"`
	ExpiresAt  int64  `gorm:"default:0" json:"expires_at"`
	LastUsedAt int64  `gorm:"default:0" json:"last_used_at"`
	Revoked    bool   `gorm:"default:false" json:"revoked"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
