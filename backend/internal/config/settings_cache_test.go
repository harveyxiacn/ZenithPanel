package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func newSettingsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:settings_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGetSettingCacheSeesSetSettingImmediately(t *testing.T) {
	orig := DB
	t.Cleanup(func() { DB = orig })
	DB = newSettingsDB(t)

	if got := GetSetting("k"); got != "" { // caches the miss
		t.Fatalf("want empty, got %q", got)
	}
	if err := SetSetting("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if got := GetSetting("k"); got != "v1" {
		t.Fatalf("after SetSetting want v1, got %q", got)
	}
	if err := SetSetting("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if got := GetSetting("k"); got != "v2" {
		t.Fatalf("after update want v2, got %q", got)
	}
}

func TestGetSettingCacheResetsOnDBSwap(t *testing.T) {
	orig := DB
	t.Cleanup(func() { DB = orig })

	DB = newSettingsDB(t)
	if err := SetSetting("k", "old-db"); err != nil {
		t.Fatal(err)
	}
	_ = GetSetting("k")

	DB = newSettingsDB(t)
	if got := GetSetting("k"); got != "" {
		t.Fatalf("value leaked across DB handles: %q", got)
	}
}

func TestGetSettingCacheExpiresForDirectWrites(t *testing.T) {
	orig := DB
	t.Cleanup(func() { DB = orig })
	DB = newSettingsDB(t)

	_ = GetSetting("direct") // cache the miss
	if err := DB.Create(&model.Setting{Key: "direct", Value: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate TTL expiry rather than sleeping 3 s.
	settingCache.Lock()
	e := settingCache.m["direct"]
	e.at = e.at.Add(-settingCacheTTL)
	settingCache.m["direct"] = e
	settingCache.Unlock()
	if got := GetSetting("direct"); got != "x" {
		t.Fatalf("direct write not visible after TTL: %q", got)
	}
}

func TestPurgeSoftDeletedProxyRowsFreesTag(t *testing.T) {
	db := newSettingsDB(t)
	if err := db.AutoMigrate(&model.Inbound{}, &model.Client{}, &model.Outbound{}); err != nil {
		t.Fatal(err)
	}
	in := model.Inbound{Tag: "node", Protocol: "vless", Port: 443}
	if err := db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	db.Delete(&in) // legacy soft delete
	if err := db.Create(&model.Inbound{Tag: "node", Protocol: "vless", Port: 8443}).Error; err == nil {
		t.Fatal("precondition: soft-deleted row should still block the tag")
	}
	purgeSoftDeletedProxyRows(db)
	if err := db.Create(&model.Inbound{Tag: "node", Protocol: "vless", Port: 8443}).Error; err != nil {
		t.Fatalf("tag still blocked after purge: %v", err)
	}
}
