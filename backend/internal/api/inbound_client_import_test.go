package api

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func TestExtractImportedInboundClientsParsesThreeXUIPayload(t *testing.T) {
	settings := `{
		"clients": [
			{
				"comment": "team",
				"email": "z45kjhin",
				"enable": true,
				"expiryTime": 0,
				"id": "21478515-473b-423b-adc8-37b2012d3c4b",
				"totalGB": 0
			}
		]
	}`
	stats := []threeXUIClientStatPayload{
		{
			Email:   "z45kjhin",
			UUID:    "21478515-473b-423b-adc8-37b2012d3c4b",
			Up:      5231768605,
			Down:    217478689148,
			AllTime: 222710457753,
			Total:   0,
		},
	}

	got, err := extractImportedInboundClients("vless", settings, stats)
	if err != nil {
		t.Fatalf("extractImportedInboundClients() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 imported client, got %d", len(got))
	}
	if got[0].Email != "z45kjhin" {
		t.Fatalf("expected email z45kjhin, got %q", got[0].Email)
	}
	if got[0].UUID != "21478515-473b-423b-adc8-37b2012d3c4b" {
		t.Fatalf("expected imported uuid, got %q", got[0].UUID)
	}
	if got[0].Remark != "team" {
		t.Fatalf("expected remark team, got %q", got[0].Remark)
	}
	if got[0].UpLoad != 5231768605 || got[0].DownLoad != 217478689148 {
		t.Fatalf("expected traffic from clientStats, got up=%d down=%d", got[0].UpLoad, got[0].DownLoad)
	}
}

func TestSyncImportedInboundClientsKeepsSameEmailAcrossDifferentInbounds(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Inbound{}, &model.Client{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	existing := model.Client{
		InboundID: 999,
		Email:     "team",
		UUID:      "existing-uuid",
		Enable:    true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing client: %v", err)
	}

	inbound := model.Inbound{
		Tag:      "imported-node",
		Protocol: "vless",
		Port:     443,
		Settings: `{"clients":[{"email":"team","id":"new-uuid","enable":true}]}`,
		Stream:   "{}",
		Enable:   true,
	}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	payload := inboundPayload{}
	if err := syncImportedInboundClients(db, inbound, payload); err != nil {
		t.Fatalf("syncImportedInboundClients() error = %v", err)
	}

	var imported model.Client
	if err := db.Where("uuid = ?", "new-uuid").First(&imported).Error; err != nil {
		t.Fatalf("expected imported client row: %v", err)
	}
	if imported.Email != "team" {
		t.Fatalf("expected same email to be allowed across inbounds, got %q", imported.Email)
	}
	if imported.InboundID != inbound.ID {
		t.Fatalf("expected inbound id %d, got %d", inbound.ID, imported.InboundID)
	}
}

func TestSyncImportedInboundClientsUpdatesMatchingEmailWithinSameInbound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Inbound{}, &model.Client{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	inbound := model.Inbound{
		Tag:      "same-inbound",
		Protocol: "vless",
		Port:     443,
		Settings: `{"clients":[{"email":"team","id":"new-uuid","enable":true}]}`,
		Stream:   "{}",
		Enable:   true,
	}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	existing := model.Client{
		InboundID: inbound.ID,
		Email:     "team",
		UUID:      "existing-uuid",
		Enable:    true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing client: %v", err)
	}

	payload := inboundPayload{}
	if err := syncImportedInboundClients(db, inbound, payload); err != nil {
		t.Fatalf("syncImportedInboundClients() error = %v", err)
	}

	var imported model.Client
	if err := db.Where("id = ?", existing.ID).First(&imported).Error; err != nil {
		t.Fatalf("expected existing client row: %v", err)
	}
	if imported.Email != "team" {
		t.Fatalf("expected same inbound import to preserve email, got %q", imported.Email)
	}
	if imported.UUID != "new-uuid" {
		t.Fatalf("expected same inbound import to update uuid, got %q", imported.UUID)
	}
}
