package config

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func openConfiguredTestSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := configureSQLite(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func pragmaString(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	var value string
	if err := db.Raw("PRAGMA " + name).Scan(&value).Error; err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConfigureSQLiteDurabilityAndPoolPolicy(t *testing.T) {
	db := openConfiguredTestSQLite(t, filepath.Join(t.TempDir(), "panel.db"))
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if got := pragmaString(t, db, "journal_mode"); got != "wal" {
		t.Fatalf("journal_mode=%q, want wal", got)
	}
	if got := pragmaString(t, db, "foreign_keys"); got != "1" {
		t.Fatalf("foreign_keys=%q, want 1", got)
	}
	if got := pragmaString(t, db, "busy_timeout"); got != "5000" {
		t.Fatalf("busy_timeout=%q, want 5000", got)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections=%d, want 1", got)
	}
}

func TestConfiguredSQLiteSerializesConcurrentWritesAndReopensWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db := openConfiguredTestSQLite(t, path)
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- db.Create(&model.Setting{Key: fmt.Sprintf("key-%d", i), Value: "value"}).Error
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	sqlDB, _ := db.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openConfiguredTestSQLite(t, path)
	reopenedSQL, _ := reopened.DB()
	defer reopenedSQL.Close()
	var count int64
	if err := reopened.Model(&model.Setting{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != writers {
		t.Fatalf("reopened row count=%d, want %d", count, writers)
	}
}

func TestWALReadPoolReadsCommittedStateDuringWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.db")
	db := openConfiguredTestSQLite(t, path)
	old := DB
	DB = db
	t.Cleanup(func() { _ = closeReadPool(); p, _ := db.DB(); _ = p.Close(); DB = old })
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Setting{Key: "k", Value: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := configureReadPool(db, path); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	defer tx.Rollback()
	if err := tx.Model(&model.Setting{}).Where("key = ?", "k").Update("value", "after").Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var row model.Setting
	if err := ReadDB().WithContext(ctx).Where("key = ?", "k").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Value != "before" {
		t.Fatalf("dirty read: %+v", row)
	}
	if err := ReadDB().WithContext(ctx).Create(&model.Setting{Key: "forbidden"}).Error; err == nil {
		t.Fatal("reader accepted a write")
	}
}
