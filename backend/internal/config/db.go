package config

// Expose models globally so we can AutoMigrate them in the main runner
import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// CloseDB waits for in-flight database/sql operations and closes the shared
// SQLite pool. It is safe to call during graceful shutdown after background
// producers have been cancelled.
func CloseDB() error {
	if err := closeReadPool(); err != nil {
		return err
	}
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get sql connection: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close sqlite: %w", err)
	}
	return nil
}

// gormLogger builds the GORM logger used by the panel's DB connection.
//
// It runs at Warn level with record-not-found errors ignored. This matters:
// the traffic monitor polls every 2s (see service/traffic/monitor.go) and calls
// GetSetting() for many optional keys that legitimately don't exist yet. GORM's
// default logger logs every ErrRecordNotFound together with the full SQL, so
// those misses flooded the container's stdout — in production a single
// zenithpanel json.log grew to 2GB and filled a 10GB disk, hanging the host.
// Ignoring record-not-found (a normal, expected outcome here) while keeping
// real errors and slow queries removes the noise without hiding problems.
//
// Override with ZENITH_DB_LOG_LEVEL=silent|error|warn|info when debugging.
func gormLogger() logger.Interface {
	level := logger.Warn
	switch strings.ToLower(os.Getenv("ZENITH_DB_LOG_LEVEL")) {
	case "silent":
		level = logger.Silent
	case "error":
		level = logger.Error
	case "warn":
		level = logger.Warn
	case "info":
		level = logger.Info
	}
	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  level,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
}

// InitDB initializes the SQLite database and performs auto-migration
func InitDB(dbPath string) {
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger(),
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := configureSQLite(database); err != nil {
		log.Fatalf("Failed to configure SQLite: %v", err)
	}

	// Pre-migration: add missing columns to existing tables BEFORE AutoMigrate.
	// SQLite's ALTER TABLE can't add NOT NULL columns without defaults, and GORM's
	// AutoMigrate recreates the table — which fails if existing rows have NULL in
	// NOT NULL columns. This step ensures columns exist so AutoMigrate succeeds.
	preMigrateClientColumns(database)

	err = database.AutoMigrate(
		&model.Inbound{},
		&model.Client{},
		&model.RoutingRule{},
		&model.Setting{},
		&model.AdminUser{},
		&model.CronJob{},
	)
	if err != nil {
		log.Fatalf("Failed to auto migrate database: %v", err)
	}
	if err := migrateClientSchema(database); err != nil {
		log.Fatalf("Failed to migrate client schema: %v", err)
	}

	// Audit log migration is non-fatal — don't block startup if it fails
	if err := database.AutoMigrate(&model.AuditLog{}); err != nil {
		log.Printf("Warning: failed to migrate AuditLog table: %v", err)
	}

	// Smart Deploy tables (Phase 1). Non-fatal: an older panel should still
	// boot if these migrations fail; smart deploy simply becomes unavailable.
	if err := database.AutoMigrate(&model.Deployment{}, &model.DeploymentOp{}); err != nil {
		log.Printf("Warning: failed to migrate Smart Deploy tables: %v", err)
	}

	// Outbound table (Phase E). Non-fatal for backwards compat with older DBs.
	if err := database.AutoMigrate(&model.Outbound{}); err != nil {
		log.Printf("Warning: failed to migrate Outbound table: %v", err)
	}

	// Site table (Phase J). Non-fatal for backwards compat with older DBs.
	if err := database.AutoMigrate(&model.Site{}); err != nil {
		log.Printf("Warning: failed to migrate Site table: %v", err)
	}

	// NetworkMetric table for persistent hourly history. Non-fatal.
	if err := database.AutoMigrate(&model.NetworkMetric{}); err != nil {
		log.Printf("Warning: failed to migrate NetworkMetric table: %v", err)
	}

	// ApiToken table for CLI / headless automation credentials. Non-fatal so
	// the panel keeps booting even if the migration fails on an exotic DB.
	if err := database.AutoMigrate(&model.ApiToken{}); err != nil {
		log.Printf("Warning: failed to migrate ApiToken table: %v", err)
	}

	// Traffic-egress logging tables (hot 5-min buckets + hourly rollup). Non-fatal
	// so the panel still boots if the migration fails on an older/exotic DB; the
	// egress feature simply has nowhere to write until the table exists.
	if err := database.AutoMigrate(&model.TrafficEgress{}, &model.TrafficEgressHourly{}); err != nil {
		log.Printf("Warning: failed to migrate traffic-egress tables: %v", err)
	}

	purgeSoftDeletedProxyRows(database)

	DB = database
	if err := configureReadPool(database, dbPath); err != nil {
		log.Fatalf("Failed to configure SQLite read pool: %v", err)
	}
	log.Println("Database initialized and migrated successfully")
}

// configureSQLite applies the durability and contention policy used by every
// production connection. SQLite only permits one writer; limiting the pool to
// one connection makes database/sql the process-wide write coordinator instead
// of letting unrelated background goroutines race until SQLITE_BUSY. WAL still
// provides crash recovery and lets external read-only tooling inspect the DB.
func configureSQLite(database *gorm.DB) error {
	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("get sql connection: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, statement := range pragmas {
		if err := database.Exec(statement).Error; err != nil {
			return fmt.Errorf("%s: %w", statement, err)
		}
	}
	var mode string
	if err := database.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		return err
	}
	if mode != "wal" && mode != "memory" {
		return fmt.Errorf("SQLite journal mode is %q, expected wal", mode)
	}
	log.Printf("SQLite effective journal mode: %s", mode)
	return nil
}

// purgeSoftDeletedProxyRows hard-deletes inbound/client/outbound rows that
// older versions soft-deleted. Those rows kept their tag / (inbound, email)
// in the unique indexes, so a deleted node or user could never be recreated
// under the same name. Nothing reads soft-deleted rows.
func purgeSoftDeletedProxyRows(db *gorm.DB) {
	for _, m := range []any{&model.Client{}, &model.Inbound{}, &model.Outbound{}} {
		res := db.Unscoped().Where("deleted_at IS NOT NULL").Delete(m)
		if res.Error != nil {
			log.Printf("Warning: purge soft-deleted %T: %v", m, res.Error)
		} else if res.RowsAffected > 0 {
			log.Printf("Purged %d soft-deleted %T row(s)", res.RowsAffected, m)
		}
	}
	// Clients whose inbound no longer exists (older Smart Deploy rollbacks
	// removed the inbound but left its users behind).
	res := db.Unscoped().Where("inbound_id NOT IN (?)", db.Unscoped().Model(&model.Inbound{}).Select("id")).Delete(&model.Client{})
	if res.Error == nil && res.RowsAffected > 0 {
		log.Printf("Purged %d orphaned client row(s)", res.RowsAffected)
	}
}

// settingCacheTTL bounds how stale GetSetting can be for writes that bypass
// SetSetting (a few transactions write rows directly). Background loops read
// the same handful of keys every 2–10 s; without the cache each read was a
// SQLite query, most of them for keys that don't exist.
const settingCacheTTL = 3 * time.Second

type settingEntry struct {
	value string
	at    time.Time
}

var settingCache = struct {
	sync.Mutex
	db *gorm.DB // cache is only valid for this DB handle (tests swap DB)
	m  map[string]settingEntry
}{m: map[string]settingEntry{}}

// GetSetting retrieves a setting value by key, returns empty string if not found
func GetSetting(key string) string {
	if DB == nil {
		return ""
	}
	settingCache.Lock()
	if settingCache.db != DB {
		settingCache.db, settingCache.m = DB, map[string]settingEntry{}
	}
	if e, ok := settingCache.m[key]; ok && time.Since(e.at) < settingCacheTTL {
		settingCache.Unlock()
		return e.value
	}
	settingCache.Unlock()

	var s model.Setting
	value := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ReadDB().WithContext(ctx).Where("`key` = ?", key).First(&s).Error; err == nil {
		value = s.Value
	}
	settingCache.Lock()
	if settingCache.db == DB {
		settingCache.m[key] = settingEntry{value: value, at: time.Now()}
	}
	settingCache.Unlock()
	return value
}

// invalidateSetting drops a cached key so the next GetSetting re-reads it.
func invalidateSetting(key string) {
	settingCache.Lock()
	delete(settingCache.m, key)
	settingCache.Unlock()
}

// SetSetting upserts a setting key-value pair
func SetSetting(key, value string) error {
	defer invalidateSetting(key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&model.Setting{Key: key, Value: value}).Error
}

// EnsureJWTSecret generates and persists a random JWT secret if one doesn't exist
func EnsureJWTSecret() []byte {
	existing := GetSetting("jwt_secret")
	if existing != "" {
		decoded, err := base64.StdEncoding.DecodeString(existing)
		if err == nil && len(decoded) >= 32 {
			return decoded
		}
	}
	// Generate a new 32-byte random secret
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	if err != nil {
		log.Fatalf("Failed to generate JWT secret: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(secret)
	if err := SetSetting("jwt_secret", encoded); err != nil {
		log.Fatalf("Failed to persist JWT secret: %v", err)
	}
	log.Println("Generated and persisted new JWT secret")
	return secret
}

// IsSetupDone checks the DB for setup completion status
func IsSetupDone() bool {
	return GetSetting("setup_complete") == "true"
}

// MarkSetupDone persists setup completion to the DB
func MarkSetupDone() error {
	return SetSetting("setup_complete", "true")
}

// EnsurePort returns the panel's listen port.
// Priority: ZENITH_PORT env var > DB setting > random generation (10000-65535).
// The env var allows Docker users to align the internal port with their -p mapping.
func EnsurePort() string {
	// Highest priority: environment variable override
	if envPort := os.Getenv("ZENITH_PORT"); envPort != "" {
		// Persist to DB so it's consistent across restarts
		if err := SetSetting("port", envPort); err != nil {
			log.Fatalf("Failed to persist port: %v", err)
		}
		return envPort
	}

	existing := GetSetting("port")
	if existing != "" {
		return existing
	}

	// Generate 2 random bytes to derive a port in [10000, 65535]
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("Failed to generate random port: %v", err)
	}
	n := int(b[0])<<8 | int(b[1])
	port := 10000 + (n % 55536) // range: 10000–65535
	portStr := strconv.Itoa(port)
	if err := SetSetting("port", portStr); err != nil {
		log.Fatalf("Failed to persist port: %v", err)
	}
	log.Printf("Generated random listen port: %s (saved to DB)", portStr)
	return portStr
}
