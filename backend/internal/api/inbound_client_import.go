package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

type threeXUIClientStatPayload struct {
	Enable     *bool  `json:"enable"`
	Email      string `json:"email"`
	UUID       string `json:"uuid"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	AllTime    int64  `json:"allTime"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
}

type threeXUISettingsClientPayload struct {
	Comment    string `json:"comment"`
	Email      string `json:"email"`
	Enable     *bool  `json:"enable"`
	ExpiryTime *int64 `json:"expiryTime"`
	ID         string `json:"id"`
	Password   string `json:"password"`
	Total      *int64 `json:"total"`
	TotalGB    *int64 `json:"totalGB"`
	UUID       string `json:"uuid"`
}

type threeXUISettingsPayload struct {
	Clients []threeXUISettingsClientPayload `json:"clients"`
}

type importedInboundClient struct {
	DownLoad   int64
	Email      string
	Enable     bool
	ExpiryTime int64
	Remark     string
	Total      int64
	UUID       string
	UpLoad     int64
}

func extractImportedInboundClients(protocol, settingsJSON string, stats []threeXUIClientStatPayload) ([]importedInboundClient, error) {
	settingsJSON = strings.TrimSpace(settingsJSON)
	if settingsJSON == "" || settingsJSON == "{}" {
		return nil, nil
	}

	var settings threeXUISettingsPayload
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, err
	}

	statsByEmail := make(map[string]threeXUIClientStatPayload, len(stats))
	statsByUUID := make(map[string]threeXUIClientStatPayload, len(stats))
	for _, stat := range stats {
		if email := strings.ToLower(strings.TrimSpace(stat.Email)); email != "" {
			statsByEmail[email] = stat
		}
		if uuid := strings.TrimSpace(stat.UUID); uuid != "" {
			statsByUUID[uuid] = stat
		}
	}

	imported := make([]importedInboundClient, 0, len(settings.Clients))
	for _, client := range settings.Clients {
		credential := importedClientCredential(protocol, client)
		if credential == "" {
			continue
		}

		email := strings.TrimSpace(client.Email)
		if email == "" {
			email = deriveImportedClientEmail(credential)
		}

		entry := importedInboundClient{
			Email:      email,
			UUID:       credential,
			Enable:     true,
			Remark:     strings.TrimSpace(client.Comment),
			ExpiryTime: 0,
			Total:      0,
		}
		if client.Enable != nil {
			entry.Enable = *client.Enable
		}
		if client.ExpiryTime != nil {
			entry.ExpiryTime = *client.ExpiryTime
		}
		switch {
		case client.Total != nil:
			entry.Total = *client.Total
		case client.TotalGB != nil:
			entry.Total = *client.TotalGB
		}

		if stat, ok := statsByUUID[credential]; ok {
			mergeImportedClientStat(&entry, stat)
		} else if stat, ok := statsByEmail[strings.ToLower(email)]; ok {
			mergeImportedClientStat(&entry, stat)
		}

		imported = append(imported, entry)
	}

	return imported, nil
}

func importedClientCredential(protocol string, client threeXUISettingsClientPayload) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "trojan", "hysteria2", "shadowsocks":
		if password := strings.TrimSpace(client.Password); password != "" {
			return password
		}
	}
	if id := strings.TrimSpace(client.ID); id != "" {
		return id
	}
	if uuid := strings.TrimSpace(client.UUID); uuid != "" {
		return uuid
	}
	if password := strings.TrimSpace(client.Password); password != "" {
		return password
	}
	return ""
}

func deriveImportedClientEmail(credential string) string {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "imported-client"
	}
	credential = strings.NewReplacer("@", "-", ":", "-", "/", "-", "\\", "-").Replace(credential)
	if len(credential) > 12 {
		credential = credential[:12]
	}
	return "imported-" + credential
}

func mergeImportedClientStat(target *importedInboundClient, stat threeXUIClientStatPayload) {
	target.UpLoad = stat.Up
	target.DownLoad = stat.Down
	if stat.Total > 0 || target.Total == 0 {
		target.Total = stat.Total
	}
	if stat.ExpiryTime > 0 || target.ExpiryTime == 0 {
		target.ExpiryTime = stat.ExpiryTime
	}
	if stat.Enable != nil {
		target.Enable = *stat.Enable
	}
}

func ensureUniqueClientEmail(tx *gorm.DB, inboundID uint, preferred string, currentID uint) (string, error) {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = "imported-client"
	}

	candidate := base
	for suffix := 1; ; suffix++ {
		var existing model.Client
		err := tx.Where("inbound_id = ? AND email = ?", inboundID, candidate).First(&existing).Error
		switch {
		case err == nil && existing.ID != currentID:
			candidate = fmt.Sprintf("%s-%d", base, suffix+1)
		case err == nil:
			return candidate, nil
		case err == gorm.ErrRecordNotFound:
			return candidate, nil
		default:
			return "", err
		}
	}
}

func syncImportedInboundClients(tx *gorm.DB, inbound model.Inbound, payload inboundPayload) error {
	var stats []threeXUIClientStatPayload
	if payload.ClientStats != nil {
		stats = *payload.ClientStats
	}

	importedClients, err := extractImportedInboundClients(inbound.Protocol, inbound.Settings, stats)
	if err != nil {
		return err
	}
	if len(importedClients) == 0 {
		return nil
	}

	for _, imported := range importedClients {
		var existing model.Client
		findQuery := tx.Where("inbound_id = ?", inbound.ID)
		findErr := gorm.ErrRecordNotFound
		if imported.UUID != "" {
			findErr = findQuery.Where("uuid = ?", imported.UUID).First(&existing).Error
		}
		if findErr == gorm.ErrRecordNotFound && imported.Email != "" {
			findErr = tx.Where("inbound_id = ? AND email = ?", inbound.ID, imported.Email).First(&existing).Error
		}
		if findErr != nil && findErr != gorm.ErrRecordNotFound {
			return findErr
		}

		uniqueEmail, err := ensureUniqueClientEmail(tx, inbound.ID, imported.Email, existing.ID)
		if err != nil {
			return err
		}

		if findErr == gorm.ErrRecordNotFound {
			client := model.Client{
				InboundID:  inbound.ID,
				Email:      uniqueEmail,
				UUID:       imported.UUID,
				Enable:     imported.Enable,
				UpLoad:     imported.UpLoad,
				DownLoad:   imported.DownLoad,
				Total:      imported.Total,
				ExpiryTime: imported.ExpiryTime,
				Remark:     imported.Remark,
			}
			if err := tx.Create(&client).Error; err != nil {
				return err
			}
			continue
		}

		existing.Email = uniqueEmail
		existing.UUID = imported.UUID
		existing.Enable = imported.Enable
		existing.UpLoad = imported.UpLoad
		existing.DownLoad = imported.DownLoad
		existing.Total = imported.Total
		existing.ExpiryTime = imported.ExpiryTime
		existing.Remark = imported.Remark
		if err := tx.Save(&existing).Error; err != nil {
			return err
		}
	}

	return nil
}
