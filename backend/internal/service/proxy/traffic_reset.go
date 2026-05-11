package proxy

import (
	"log"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// RunDailyTrafficReset zeros up_load/down_load for all enabled clients whose
// reset_day matches today's day-of-month. Should be invoked once per day
// (typically just after midnight) from a background goroutine.
// Returns the number of rows affected so callers can decide whether to
// invalidate downstream caches (e.g. the subscription cache in `sub`).
func RunDailyTrafficReset(db *gorm.DB) int64 {
	day := time.Now().Day()
	result := db.Model(&model.Client{}).
		Where("reset_day = ? AND reset_day > 0", day).
		Updates(map[string]interface{}{"up_load": 0, "down_load": 0})

	if result.Error != nil {
		log.Printf("[traffic-reset] DB update failed: %v", result.Error)
		return 0
	}
	if result.RowsAffected > 0 {
		log.Printf("[traffic-reset] Reset %d client(s) for day %d", result.RowsAffected, day)
	}
	return result.RowsAffected
}
