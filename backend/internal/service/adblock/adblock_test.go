package adblock

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:adblock_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.RoutingRule{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestApplyCreatesRowWhenEnabled is the happy-path on→ first use.
func TestApplyCreatesRowWhenEnabled(t *testing.T) {
	db := setupDB(t)
	if err := Apply(db, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var count int64
	db.Model(&model.RoutingRule{}).Where("rule_tag = ?", managedTag).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 managed rule, got %d", count)
	}
}

// TestApplyIsIdempotentWhenAlreadyEnabled re-applies on. Should not create a
// duplicate row.
func TestApplyIsIdempotentWhenAlreadyEnabled(t *testing.T) {
	db := setupDB(t)
	if err := Apply(db, true); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, true); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.RoutingRule{}).Where("rule_tag = ?", managedTag).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row after two Apply(true) calls, got %d", count)
	}
}

// TestApplyDeletesRowWhenDisabled checks the off path.
func TestApplyDeletesRowWhenDisabled(t *testing.T) {
	db := setupDB(t)
	if err := Apply(db, true); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, false); err != nil {
		t.Fatalf("Apply(false): %v", err)
	}
	var count int64
	db.Model(&model.RoutingRule{}).Where("rule_tag = ?", managedTag).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 rows after Apply(false), got %d", count)
	}
}

// TestApplyReEnablesManuallyDisabledRow covers the edge where the user
// flipped the rule's Enable flag in the UI: re-enabling via toggle should
// restore it without creating a duplicate.
func TestApplyReEnablesManuallyDisabledRow(t *testing.T) {
	db := setupDB(t)
	if err := Apply(db, true); err != nil {
		t.Fatal(err)
	}
	// Simulate user turning off the row in the Web UI.
	db.Model(&model.RoutingRule{}).Where("rule_tag = ?", managedTag).Update("enable", false)

	if err := Apply(db, true); err != nil {
		t.Fatal(err)
	}
	var row model.RoutingRule
	db.Where("rule_tag = ?", managedTag).First(&row)
	if !row.Enable {
		t.Errorf("expected Apply(true) to re-enable the row")
	}
}

// TestApplyDisabledWhenAbsent is a no-op (no row to delete).
func TestApplyDisabledWhenAbsent(t *testing.T) {
	db := setupDB(t)
	if err := Apply(db, false); err != nil {
		t.Errorf("Apply(false) on empty table should be no-op, got error: %v", err)
	}
}

// TestIsEnabledReadsSetting verifies the on/off readback via the stub
// getter. "true" → on, anything else → off.
func TestIsEnabledReadsSetting(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"true":  true,
		"True":  false, // case-sensitive; matches "true" exactly
		"yes":   false,
		"1":     false,
		"false": false,
	}
	for in, want := range cases {
		got := IsEnabled(func(k string) string {
			if k == SettingKey {
				return in
			}
			return ""
		})
		if got != want {
			t.Errorf("IsEnabled(%q) = %v, want %v", in, got, want)
		}
	}
}
