package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

type threeXUIInboundImportPayload struct {
	ID                   uint                        `json:"id"`
	UserID               uint                        `json:"userId"`
	Up                   int64                       `json:"up"`
	Down                 int64                       `json:"down"`
	Total                int64                       `json:"total"`
	AllTime              int64                       `json:"allTime"`
	Remark               string                      `json:"remark"`
	Enable               *bool                       `json:"enable"`
	ExpiryTime           int64                       `json:"expiryTime"`
	TrafficReset         string                      `json:"trafficReset"`
	LastTrafficResetTime int64                       `json:"lastTrafficResetTime"`
	Listen               string                      `json:"listen"`
	Port                 int                         `json:"port"`
	Protocol             string                      `json:"protocol"`
	Settings             json.RawMessage             `json:"settings"`
	StreamSettings       json.RawMessage             `json:"streamSettings"`
	Tag                  string                      `json:"tag"`
	Sniffing             json.RawMessage             `json:"sniffing"`
	ClientStats          []threeXUIClientStatPayload `json:"clientStats"`
}

type threeXUISettingsClientExport struct {
	Comment    string `json:"comment,omitempty"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	ID         string `json:"id,omitempty"`
	Password   string `json:"password,omitempty"`
	Reset      int64  `json:"reset,omitempty"`
	TotalGB    int64  `json:"totalGB"`
	UpdatedAt  int64  `json:"updated_at,omitempty"`
}

type threeXUIClientStatExport struct {
	ID         uint   `json:"id"`
	InboundID  uint   `json:"inboundId"`
	Enable     bool   `json:"enable"`
	Email      string `json:"email"`
	UUID       string `json:"uuid"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	AllTime    int64  `json:"allTime"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
	Reset      int64  `json:"reset"`
}

type threeXUIInboundExportPayload struct {
	ID                   uint                       `json:"id"`
	UserID               uint                       `json:"userId"`
	Up                   int64                      `json:"up"`
	Down                 int64                      `json:"down"`
	Total                int64                      `json:"total"`
	AllTime              int64                      `json:"allTime"`
	Remark               string                     `json:"remark"`
	Enable               bool                       `json:"enable"`
	ExpiryTime           int64                      `json:"expiryTime"`
	TrafficReset         string                     `json:"trafficReset"`
	LastTrafficResetTime int64                      `json:"lastTrafficResetTime"`
	Listen               string                     `json:"listen"`
	Port                 int                        `json:"port"`
	Protocol             string                     `json:"protocol"`
	Settings             string                     `json:"settings"`
	StreamSettings       string                     `json:"streamSettings"`
	Tag                  string                     `json:"tag"`
	Sniffing             string                     `json:"sniffing"`
	ClientStats          []threeXUIClientStatExport `json:"clientStats"`
}

func parseThreeXUIImportRequest(body []byte) ([]threeXUIInboundImportPayload, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	if trimmed[0] == '[' {
		var items []threeXUIInboundImportPayload
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
		return items, nil
	}

	var item threeXUIInboundImportPayload
	if err := json.Unmarshal(trimmed, &item); err != nil {
		return nil, err
	}
	return []threeXUIInboundImportPayload{item}, nil
}

func rawJSONToNormalizedString(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}

	if trimmed[0] == '"' {
		var decoded string
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			return "", err
		}
		decoded = strings.TrimSpace(decoded)
		if decoded == "" {
			return "", nil
		}
		return prettyJSONString(decoded)
	}

	return prettyJSONString(string(trimmed))
}

func prettyJSONString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return "", err
	}
	return string(pretty), nil
}

func ensureUniqueInboundTag(tx *gorm.DB, preferred string) (string, error) {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = "imported-inbound"
	}

	candidate := base
	for suffix := 1; ; suffix++ {
		var existing model.Inbound
		err := tx.Where("tag = ?", candidate).First(&existing).Error
		switch {
		case err == nil:
			candidate = fmt.Sprintf("%s-%d", base, suffix+1)
		case err == gorm.ErrRecordNotFound:
			return candidate, nil
		default:
			return "", err
		}
	}
}

func buildInboundPayloadFromThreeXUI(tx *gorm.DB, src threeXUIInboundImportPayload) (inboundPayload, string, error) {
	protocol := strings.TrimSpace(src.Protocol)
	if protocol == "" {
		return inboundPayload{}, "", fmt.Errorf("protocol is required")
	}
	if src.Port <= 0 || src.Port > 65535 {
		return inboundPayload{}, "", fmt.Errorf("port must be between 1 and 65535")
	}

	settings, err := rawJSONToNormalizedString(src.Settings)
	if err != nil {
		return inboundPayload{}, "", fmt.Errorf("invalid settings: %w", err)
	}
	streamSettings, err := rawJSONToNormalizedString(src.StreamSettings)
	if err != nil {
		return inboundPayload{}, "", fmt.Errorf("invalid streamSettings: %w", err)
	}

	baseTag := strings.TrimSpace(src.Remark)
	if baseTag == "" {
		baseTag = strings.TrimSpace(src.Tag)
	}
	if baseTag == "" {
		baseTag = fmt.Sprintf("%s-%d", protocol, src.Port)
	}
	tag, err := ensureUniqueInboundTag(tx, baseTag)
	if err != nil {
		return inboundPayload{}, "", err
	}

	enable := true
	if src.Enable != nil {
		enable = *src.Enable
	}

	remark := strings.TrimSpace(src.Remark)
	if remark == "" {
		remark = strings.TrimSpace(src.Tag)
	}

	payload := inboundPayload{
		Tag:         &tag,
		Protocol:    &protocol,
		Listen:      &src.Listen,
		Port:        &src.Port,
		Settings:    &settings,
		Stream:      &streamSettings,
		ClientStats: &src.ClientStats,
		Enable:      &enable,
		Remark:      &remark,
	}

	return payload, tag, nil
}

func importThreeXUIInbound(tx *gorm.DB, src threeXUIInboundImportPayload) (model.Inbound, int, error) {
	payload, _, err := buildInboundPayloadFromThreeXUI(tx, src)
	if err != nil {
		return model.Inbound{}, 0, err
	}

	inbound := model.Inbound{Enable: true}
	applyInboundPayload(&inbound, payload)
	if inbound.Settings == "" {
		inbound.Settings = "{}"
	}
	if inbound.Stream == "" {
		inbound.Stream = "{}"
	}

	if err := tx.Create(&inbound).Error; err != nil {
		return model.Inbound{}, 0, err
	}
	if err := syncImportedInboundClients(tx, inbound, payload); err != nil {
		return model.Inbound{}, 0, err
	}

	importedUsers := 0
	if payload.ClientStats != nil {
		importedUsers = len(*payload.ClientStats)
	} else if strings.TrimSpace(inbound.Settings) != "" && inbound.Settings != "{}" {
		if clients, err := extractImportedInboundClients(inbound.Protocol, inbound.Settings, nil); err == nil {
			importedUsers = len(clients)
		}
	}
	return inbound, importedUsers, nil
}

func buildThreeXUIInboundExport(inbound model.Inbound, clients []model.Client) (threeXUIInboundExportPayload, error) {
	settingsMap := map[string]any{}
	if strings.TrimSpace(inbound.Settings) != "" && inbound.Settings != "{}" {
		if err := json.Unmarshal([]byte(inbound.Settings), &settingsMap); err != nil {
			return threeXUIInboundExportPayload{}, err
		}
	}
	settingsMap["clients"] = buildThreeXUISettingsClients(inbound.Protocol, clients)
	settingsJSON, err := json.MarshalIndent(settingsMap, "", "  ")
	if err != nil {
		return threeXUIInboundExportPayload{}, err
	}

	streamJSON := "{}"
	if pretty, err := prettyJSONString(inbound.Stream); err == nil && strings.TrimSpace(pretty) != "" {
		streamJSON = pretty
	} else if strings.TrimSpace(inbound.Stream) != "" && inbound.Stream != "{}" {
		return threeXUIInboundExportPayload{}, err
	}

	sniffingJSON := "{\n  \"enabled\": false,\n  \"destOverride\": [\n    \"http\",\n    \"tls\",\n    \"quic\",\n    \"fakedns\"\n  ],\n  \"metadataOnly\": false,\n  \"routeOnly\": false\n}"
	if inbound.Remark == "" {
		inbound.Remark = inbound.Tag
	}

	clientStats := buildThreeXUIClientStats(clients, inbound.ID)
	var up, down, total, allTime int64
	for _, stat := range clientStats {
		up += stat.Up
		down += stat.Down
		total += stat.Total
		allTime += stat.AllTime
	}

	return threeXUIInboundExportPayload{
		ID:                   inbound.ID,
		UserID:               0,
		Up:                   up,
		Down:                 down,
		Total:                total,
		AllTime:              allTime,
		Remark:               inbound.Remark,
		Enable:               inbound.Enable,
		ExpiryTime:           0,
		TrafficReset:         "never",
		LastTrafficResetTime: 0,
		Listen:               inbound.Listen,
		Port:                 inbound.Port,
		Protocol:             inbound.Protocol,
		Settings:             string(settingsJSON),
		StreamSettings:       streamJSON,
		Tag:                  fmt.Sprintf("inbound-%d", inbound.Port),
		Sniffing:             sniffingJSON,
		ClientStats:          clientStats,
	}, nil
}

func buildThreeXUISettingsClients(protocol string, clients []model.Client) []threeXUISettingsClientExport {
	out := make([]threeXUISettingsClientExport, 0, len(clients))
	for _, client := range clients {
		item := threeXUISettingsClientExport{
			Comment:    client.Remark,
			CreatedAt:  client.CreatedAt.UnixMilli(),
			Email:      client.Email,
			Enable:     client.Enable,
			ExpiryTime: client.ExpiryTime,
			Reset:      0,
			TotalGB:    client.Total,
			UpdatedAt:  client.UpdatedAt.UnixMilli(),
		}

		switch strings.ToLower(strings.TrimSpace(protocol)) {
		case "trojan", "hysteria2", "shadowsocks":
			item.Password = client.UUID
		default:
			item.ID = client.UUID
		}

		out = append(out, item)
	}
	return out
}

func buildThreeXUIClientStats(clients []model.Client, inboundID uint) []threeXUIClientStatExport {
	out := make([]threeXUIClientStatExport, 0, len(clients))
	for _, client := range clients {
		out = append(out, threeXUIClientStatExport{
			ID:         client.ID,
			InboundID:  inboundID,
			Enable:     client.Enable,
			Email:      client.Email,
			UUID:       client.UUID,
			Up:         client.UpLoad,
			Down:       client.DownLoad,
			AllTime:    client.UpLoad + client.DownLoad,
			ExpiryTime: client.ExpiryTime,
			Total:      client.Total,
			Reset:      0,
		})
	}
	return out
}
