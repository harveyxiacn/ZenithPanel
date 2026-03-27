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
