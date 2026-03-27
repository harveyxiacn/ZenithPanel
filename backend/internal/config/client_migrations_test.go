package config

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMigrateClientSchemaScopesEmailUniquenessPerInbound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	createLegacyTable := `
CREATE TABLE clients (
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
	if err := db.Exec(createLegacyTable).Error; err != nil {
		t.Fatalf("create legacy clients table: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_clients_email ON clients(email)").Error; err != nil {
		t.Fatalf("create legacy email index: %v", err)
	}
	if err := db.Exec("CREATE INDEX idx_clients_inbound_id ON clients(inbound_id)").Error; err != nil {
		t.Fatalf("create legacy inbound index: %v", err)
	}

	if err := migrateClientSchema(db); err != nil {
		t.Fatalf("migrateClientSchema() error = %v", err)
	}

	insertA := db.Exec(`INSERT INTO clients (inbound_id, email, uuid, enable) VALUES (1, 'team', 'uuid-a', true)`).Error
	if insertA != nil {
		t.Fatalf("insert first client: %v", insertA)
	}
	insertB := db.Exec(`INSERT INTO clients (inbound_id, email, uuid, enable) VALUES (2, 'team', 'uuid-b', true)`).Error
	if insertB != nil {
		t.Fatalf("expected same email on different inbound to succeed, got %v", insertB)
	}
	insertC := db.Exec(`INSERT INTO clients (inbound_id, email, uuid, enable) VALUES (1, 'team', 'uuid-c', true)`).Error
	if insertC == nil {
		t.Fatalf("expected duplicate email on same inbound to fail")
	}
}
