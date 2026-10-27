package api

import (
	"context"
	"errors"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// Compare the version that was read while checking the password/TOTP. A
// concurrent security operation must never overwrite a newer session version.
func updateAdminSecurity(ctx context.Context, admin model.AdminUser, updates map[string]any) error {
	updates["session_version"] = gorm.Expr("session_version + 1")
	result := config.DB.WithContext(ctx).Model(&model.AdminUser{}).
		Where("id = ? AND session_version = ?", admin.ID, admin.SessionVersion).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("administrator security settings changed; sign in again")
	}
	return nil
}
