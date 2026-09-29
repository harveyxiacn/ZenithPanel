package config

// Expose models globally so we can AutoMigrate them in the main runner
import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

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
	log.Println("Database initialized and migrated successfully")
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
	if err := DB.Where("`key` = ?", key).First(&s).Error; err == nil {
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
	var s model.Setting
	result := DB.Where("`key` = ?", key).First(&s)
	if result.Error != nil {
		// Create new
		return DB.Create(&model.Setting{Key: key, Value: value}).Error
	}
	// Update
	s.Value = value
	return DB.Save(&s).Error
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
