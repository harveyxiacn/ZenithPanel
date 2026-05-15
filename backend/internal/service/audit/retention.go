// Package audit handles maintenance of the audit_logs table — primarily
// applying the retention policy so the table doesn't grow without bound on
// long-lived panels.
package audit

import (
	"log"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// DefaultRetentionDays is the fallback when no setting is configured. 90 days
// matches the typical compliance window for SOC2-style audits and keeps the
// table at <100MB even for very active panels.
const DefaultRetentionDays = 90

// retentionDays reads the configured retention window from the settings
// table. Out-of-range or non-numeric values fall back to the default and
// surface a warning so the operator can fix the setting via the UI.
//
// The settings reader is parameterized so we can drive this from tests with
// a stub; production wires it to config.GetSetting.
func retentionDays(getSetting func(string) string) int {
	raw := getSetting("audit_retention_days")
	if raw == "" {
		return DefaultRetentionDays
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 3650 {
		log.Printf("audit: ignoring out-of-range audit_retention_days=%q, using default %d", raw, DefaultRetentionDays)
		return DefaultRetentionDays
	}
	return n
}

// PruneOnce deletes audit_log rows older than the configured retention. It
// is idempotent — running it twice in a row removes nothing the second time —
// so the daily ticker can call it freely without coordination. Returns the
// row count for telemetry/logging.
func PruneOnce(db *gorm.DB, getSetting func(string) string, now time.Time) (int64, error) {
	days := retentionDays(getSetting)
	cutoff := now.AddDate(0, 0, -days)
	res := db.Exec("DELETE FROM audit_logs WHERE created_at < ?", cutoff)
	return res.RowsAffected, res.Error
}

// Start spins up a daily ticker that prunes the audit log just after 04:00
// local time. The first run happens once on startup so a panel that's been
// off for a long time catches up immediately. Returns the cancel function so
// graceful shutdown can stop the loop.
func Start(db *gorm.DB, getSetting func(string) string) (cancel func()) {
	stop := make(chan struct{})

	// One immediate pass on boot to amortize a long-offline panel.
	if n, err := PruneOnce(db, getSetting, time.Now()); err != nil {
		log.Printf("audit: initial prune failed: %v", err)
	} else if n > 0 {
		log.Printf("audit: pruned %d rows past retention on startup", n)
	}

	go func() {
		for {
			next := nextRunAt(time.Now())
			select {
			case <-time.After(time.Until(next)):
				if n, err := PruneOnce(db, getSetting, time.Now()); err != nil {
					log.Printf("audit: daily prune failed: %v", err)
				} else if n > 0 {
					log.Printf("audit: pruned %d rows past retention", n)
				}
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

// nextRunAt returns the next 04:00 local time strictly after `from`. Pulled
// out so tests can deterministically check scheduling.
func nextRunAt(from time.Time) time.Time {
	loc := from.Location()
	next := time.Date(from.Year(), from.Month(), from.Day(), 4, 0, 0, 0, loc)
	if !next.After(from) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
