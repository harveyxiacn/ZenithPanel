package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func TestParseThreeXUIImportRequestSupportsObjectAndArray(t *testing.T) {
	objectBody := []byte(`{
		"remark": "team",
		"port": 52599,
		"protocol": "vless",
		"settings": "{\"clients\":[]}",
		"streamSettings": {"network": "tcp", "security": "reality"}
	}`)

	items, err := parseThreeXUIImportRequest(objectBody)
	if err != nil {
		t.Fatalf("parse object body: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item for object payload, got %d", len(items))
	}
	settings, err := rawJSONToNormalizedString(items[0].Settings)
	if err != nil {
		t.Fatalf("normalize settings string: %v", err)
	}
	if settings == "" {
		t.Fatalf("expected normalized settings string")
	}

	arrayBody := []byte(`[{"remark":"a","port":1,"protocol":"vless"},{"remark":"b","port":2,"protocol":"vmess"}]`)
	items, err = parseThreeXUIImportRequest(arrayBody)
	if err != nil {
		t.Fatalf("parse array body: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items for array payload, got %d", len(items))
	}
}

func TestBuildThreeXUIInboundExportIncludesClientsAndStats(t *testing.T) {
	now := time.Unix(1773235698, 0)
	inbound := model.Inbound{
		ID:       1,
		Tag:      "team",
		Remark:   "team",
		Enable:   true,
		Listen:   "",
		Port:     52599,
		Protocol: "vless",
		Settings: `{"decryption":"none","encryption":"none"}`,
		Stream: `{
			"network":"tcp",
			"security":"reality",
			"realitySettings":{
				"target":"gateway.icloud.com:443",
				"serverNames":["gateway.icloud.com"],
				"privateKey":"private",
				"shortIds":["1d"],
				"settings":{
					"publicKey":"public",
					"fingerprint":"chrome"
				}
			}
		}`,
	}
	clients := []model.Client{
		{
			ID:         1,
			InboundID:  1,
			Email:      "z45kjhin",
			UUID:       "21478515-473b-423b-adc8-37b2012d3c4b",
			Enable:     true,
			UpLoad:     5231768605,
			DownLoad:   217478689148,
			Total:      0,
			ExpiryTime: 0,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}

	exported, err := buildThreeXUIInboundExport(inbound, clients)
	if err != nil {
		t.Fatalf("buildThreeXUIInboundExport() error = %v", err)
	}
	if exported.Tag != "inbound-52599" {
		t.Fatalf("expected 3x-ui style tag, got %q", exported.Tag)
	}
	if exported.Remark != "team" {
		t.Fatalf("expected remark team, got %q", exported.Remark)
	}
	if exported.Up != 5231768605 || exported.Down != 217478689148 {
		t.Fatalf("unexpected traffic totals: up=%d down=%d", exported.Up, exported.Down)
	}
	if len(exported.ClientStats) != 1 {
		t.Fatalf("expected 1 exported clientStat, got %d", len(exported.ClientStats))
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(exported.Settings), &settings); err != nil {
		t.Fatalf("unmarshal exported settings: %v", err)
	}
	clientsField, ok := settings["clients"].([]interface{})
	if !ok || len(clientsField) != 1 {
		t.Fatalf("expected settings.clients with 1 entry, got %#v", settings["clients"])
	}
	firstClient := clientsField[0].(map[string]interface{})
	if firstClient["id"] != "21478515-473b-423b-adc8-37b2012d3c4b" {
		t.Fatalf("expected exported client id, got %#v", firstClient["id"])
	}
}

func TestBuildInboundPayloadFromThreeXUIUsesRemarkAsImportedTag(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Inbound{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	src := threeXUIInboundImportPayload{
		Remark:         "team",
		Tag:            "inbound-52599",
		Port:           52599,
		Protocol:       "vless",
		Settings:       json.RawMessage(`"{\"clients\":[]}"`),
		StreamSettings: json.RawMessage(`{"network":"tcp","security":"reality"}`),
	}

	payload, tag, err := buildInboundPayloadFromThreeXUI(db, src)
	if err != nil {
		t.Fatalf("buildInboundPayloadFromThreeXUI() error = %v", err)
	}
	if tag != "team" {
		t.Fatalf("expected imported tag to use remark, got %q", tag)
	}
	if payload.Tag == nil || *payload.Tag != "team" {
		t.Fatalf("expected payload tag team, got %#v", payload.Tag)
	}
	if payload.Remark == nil || *payload.Remark != "team" {
		t.Fatalf("expected payload remark team, got %#v", payload.Remark)
	}
}
