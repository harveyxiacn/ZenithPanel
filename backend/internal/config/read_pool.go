package config

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var readPoolState struct {
	sync.RWMutex
	owner, reader *gorm.DB
}

// ReadDB uses a separate read-only WAL pool. Tests replacing DB automatically
// fall back to their own database rather than reading a previous test's pool.
func ReadDB() *gorm.DB {
	readPoolState.RLock()
	defer readPoolState.RUnlock()
	if readPoolState.owner == DB && readPoolState.reader != nil {
		return readPoolState.reader
	}
	return DB
}

func configureReadPool(owner *gorm.DB, path string) error {
	if path == ":memory:" || strings.Contains(path, "mode=memory") {
		return nil
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	dsn := path + separator + "mode=ro&_pragma=" + url.QueryEscape("busy_timeout(2000)") + "&_pragma=" + url.QueryEscape("query_only(ON)")
	reader, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormLogger()})
	if err != nil {
		return err
	}
	pool, err := reader.DB()
	if err != nil {
		return err
	}
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mode string
	if err := reader.WithContext(ctx).Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		_ = pool.Close()
		return err
	}
	if mode != "wal" {
		_ = pool.Close()
		return fmt.Errorf("read pool requires WAL; effective mode is %s", mode)
	}
	readPoolState.Lock()
	old := readPoolState.reader
	readPoolState.owner, readPoolState.reader = owner, reader
	readPoolState.Unlock()
	if old != nil {
		if p, err := old.DB(); err == nil {
			_ = p.Close()
		}
	}
	return nil
}

func closeReadPool() error {
	readPoolState.Lock()
	reader := readPoolState.reader
	readPoolState.owner, readPoolState.reader = nil, nil
	readPoolState.Unlock()
	if reader == nil {
		return nil
	}
	pool, err := reader.DB()
	if err != nil {
		return err
	}
	return pool.Close()
}
