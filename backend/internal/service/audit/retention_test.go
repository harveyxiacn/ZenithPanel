package audit

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// stubSettings is a tiny implementation of the getSetting function the
// retention package expects. Tests pass an inline map instead of touching the
// real config singleton.
func stubSettings(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// setupDB returns a fresh in-memory sqlite with the AuditLog table migrated.
// Each test gets its own DSN so concurrent runs don't share state.
func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:audit_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestRetentionDaysFallsBackOnBadValues pins the input-validation contract:
// missing → default, non-numeric → default, out-of-range → default. The
// behavior matters because an operator pasting a bad value into the settings
// UI must not silently disable retention.
func TestRetentionDaysFallsBackOnBadValues(t *testing.T) {
	cases := map[string]int{
		"":      DefaultRetentionDays,
		"abc":   DefaultRetentionDays,
		"0":     DefaultRetentionDays,
		"-5":    DefaultRetentionDays,
		"99999": DefaultRetentionDays,
		"30":    30,
		"365":   365,
	}
	for in, want := range cases {
		got := retentionDays(stubSettings(map[string]string{"audit_retention_days": in}))
		if got != want {
			t.Errorf("retentionDays(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestPruneOnceDeletesPastRetention seeds a few rows older than the cutoff
// and a few inside the window, then asserts PruneOnce removes only the
// expired ones.
func TestPruneOnceDeletesPastRetention(t *testing.T) {
	db := setupDB(t)
	now := time.Now()

	old := now.AddDate(0, 0, -95)
	fresh := now.AddDate(0, 0, -10)

	// Three expired rows, two within-window.
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "test", "old1", "1.1.1.1", old)
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "test", "old2", "1.1.1.1", old)
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "test", "old3", "1.1.1.1", old)
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "test", "fresh1", "1.1.1.1", fresh)
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "test", "fresh2", "1.1.1.1", fresh)

	n, err := PruneOnce(db, stubSettings(nil), now)
	if err != nil {
		t.Fatalf("PruneOnce: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 rows pruned, got %d", n)
	}
	var remaining int64
	db.Model(&model.AuditLog{}).Count(&remaining)
	if remaining != 2 {
		t.Errorf("expected 2 rows remaining, got %d", remaining)
	}
}

// TestPruneOnceHonorsCustomRetention verifies the setting flows through the
// stub correctly.
func TestPruneOnceHonorsCustomRetention(t *testing.T) {
	db := setupDB(t)
	now := time.Now()
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "x", "20-day-old", "", now.AddDate(0, 0, -20))
	db.Exec("INSERT INTO audit_logs (username, action, detail, ip, created_at) VALUES (?,?,?,?,?)", "admin", "x", "100-day-old", "", now.AddDate(0, 0, -100))

	n, _ := PruneOnce(db, stubSettings(map[string]string{"audit_retention_days": "15"}), now)
	if n != 2 {
		t.Errorf("expected both rows pruned with 15-day retention, got %d", n)
	}
}

// TestNextRunAt verifies the 04:00-local scheduler: same day if before 4am,
// next day otherwise.
func TestNextRunAt(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		from string
		want string
	}{
		{"2026-05-15T01:00:00Z", "2026-05-15T04:00:00Z"},
		{"2026-05-15T04:00:00Z", "2026-05-16T04:00:00Z"}, // exactly 4am → next day
		{"2026-05-15T12:00:00Z", "2026-05-16T04:00:00Z"},
		{"2026-05-15T23:59:00Z", "2026-05-16T04:00:00Z"},
	}
	for _, c := range cases {
		from, _ := time.ParseInLocation(time.RFC3339, c.from, utc)
		got := nextRunAt(from).Format(time.RFC3339)
		if got != c.want {
			t.Errorf("nextRunAt(%s) = %s, want %s", c.from, got, c.want)
		}
	}
}
