package config

import (
	"fmt"
	"log"
	"strings"

	"gorm.io/gorm"
)

type sqliteIndexListRow struct {
	Seq     int    `gorm:"column:seq"`
	Name    string `gorm:"column:name"`
	Unique  int    `gorm:"column:unique"`
	Origin  string `gorm:"column:origin"`
	Partial int    `gorm:"column:partial"`
}

type sqliteIndexInfoRow struct {
	SeqNo int    `gorm:"column:seqno"`
	CID   int    `gorm:"column:cid"`
	Name  string `gorm:"column:name"`
}

// preMigrateClientColumns adds missing columns to the clients table BEFORE
// GORM AutoMigrate runs. This prevents the "NOT NULL constraint failed" crash
// when upgrading from an older schema that lacked these columns.
func preMigrateClientColumns(database *gorm.DB) {
	// Only run if the clients table exists
	if !database.Migrator().HasTable("clients") {
		return
	}

	// Columns that must exist before AutoMigrate can rebuild the table.
	// Each entry: column name → ALTER TABLE statement with a safe default.
	columns := []struct {
		name string
		ddl  string
	}{
		{"inbound_id", "ALTER TABLE clients ADD COLUMN inbound_id integer NOT NULL DEFAULT 0"},
		{"uuid", "ALTER TABLE clients ADD COLUMN uuid text NOT NULL DEFAULT ''"},
		{"up_load", "ALTER TABLE clients ADD COLUMN up_load integer DEFAULT 0"},
		{"down_load", "ALTER TABLE clients ADD COLUMN down_load integer DEFAULT 0"},
		{"total", "ALTER TABLE clients ADD COLUMN total integer DEFAULT 0"},
		{"expiry_time", "ALTER TABLE clients ADD COLUMN expiry_time integer DEFAULT 0"},
		{"remark", "ALTER TABLE clients ADD COLUMN remark text DEFAULT ''"},
		{"updated_at", "ALTER TABLE clients ADD COLUMN updated_at datetime"},
		{"speed_limit", "ALTER TABLE clients ADD COLUMN speed_limit integer DEFAULT 0"},
		{"reset_day", "ALTER TABLE clients ADD COLUMN reset_day integer DEFAULT 0"},
	}

	for _, col := range columns {
		if !database.Migrator().HasColumn(&gorm.Model{}, col.name) {
			// HasColumn with a model won't work for raw table names,
			// so we use a raw PRAGMA check instead.
			var count int64
			database.Raw("SELECT COUNT(*) FROM pragma_table_info('clients') WHERE name = ?", col.name).Scan(&count)
			if count == 0 {
				log.Printf("Pre-migration: adding missing column 'clients.%s'", col.name)
				if err := database.Exec(col.ddl).Error; err != nil {
					log.Printf("Pre-migration: column '%s' may already exist: %v", col.name, err)
				}
			}
		}
	}
}

func migrateClientSchema(database *gorm.DB) error {
	if err := ensureScopedClientEmailUniqueness(database); err != nil {
		return err
	}
	if err := database.Exec("CREATE INDEX IF NOT EXISTS idx_clients_uuid ON clients(uuid)").Error; err != nil {
		return err
	}
	return nil
}

func ensureScopedClientEmailUniqueness(database *gorm.DB) error {
	indexes, err := listSQLiteIndexes(database, "clients")
	if err != nil {
		return err
	}

	hasScopedUnique := false
	hasLegacyEmailUnique := false
	for _, idx := range indexes {
		if idx.Unique == 0 {
			continue
		}
		columns, err := listSQLiteIndexColumns(database, idx.Name)
		if err != nil {
			return err
		}
		switch {
		case len(columns) == 2 && columns[0] == "inbound_id" && columns[1] == "email":
			hasScopedUnique = true
		case len(columns) == 1 && columns[0] == "email":
			hasLegacyEmailUnique = true
		}
	}

	if hasScopedUnique && !hasLegacyEmailUnique {
		return nil
	}

	log.Printf("Migrating clients table to scoped email uniqueness (legacyUnique=%v, scopedUnique=%v)", hasLegacyEmailUnique, hasScopedUnique)
	return rebuildClientsTable(database)
}

func listSQLiteIndexes(database *gorm.DB, table string) ([]sqliteIndexListRow, error) {
	var rows []sqliteIndexListRow
	if err := database.Raw(fmt.Sprintf("PRAGMA index_list(%q)", table)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func listSQLiteIndexColumns(database *gorm.DB, indexName string) ([]string, error) {
	var rows []sqliteIndexInfoRow
	if err := database.Raw(fmt.Sprintf("PRAGMA index_info(%q)", indexName)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, strings.ToLower(strings.TrimSpace(row.Name)))
	}
	return columns, nil
}

func rebuildClientsTable(database *gorm.DB) error {
	return database.Transaction(func(tx *gorm.DB) error {
		createTableSQL := `
CREATE TABLE clients_new (
	id integer PRIMARY KEY AUTOINCREMENT,
	inbound_id integer NOT NULL,
	email text NOT NULL,
	uuid text NOT NULL,
	enable numeric DEFAULT true,
	up_load integer DEFAULT 0,
	down_load integer DEFAULT 0,
	total integer DEFAULT 0,
	expiry_time integer DEFAULT 0,
	remark text,
	created_at datetime,
	updated_at datetime,
	deleted_at datetime
)`
		if err := tx.Exec(createTableSQL).Error; err != nil {
			return err
		}

		copyDataSQL := `
INSERT INTO clients_new (
	id, inbound_id, email, uuid, enable, up_load, down_load, total, expiry_time, remark, created_at, updated_at, deleted_at
)
SELECT
	id, inbound_id, email, uuid, enable, up_load, down_load, total, expiry_time, remark, created_at, updated_at, deleted_at
FROM clients`
		if err := tx.Exec(copyDataSQL).Error; err != nil {
			return err
		}

		if err := tx.Exec("DROP TABLE clients").Error; err != nil {
			return err
		}
		if err := tx.Exec("ALTER TABLE clients_new RENAME TO clients").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_clients_inbound_id ON clients(inbound_id)").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_clients_inbound_email ON clients(inbound_id, email)").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_clients_uuid ON clients(uuid)").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_clients_deleted_at ON clients(deleted_at)").Error; err != nil {
			return err
		}
		return nil
	})
}
