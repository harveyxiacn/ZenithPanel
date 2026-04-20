// Package backup exports and restores ZenithPanel's full logical state
// (inbounds, clients, routing rules, cron jobs, and non-secret settings) as a
// single JSON-in-zip archive. The backup omits the JWT secret and admin
// password hashes so restored archives never overwrite the panel's identity —
// the operator keeps their current login after a restore.
package backup

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// FormatVersion is bumped whenever the archive schema changes in a backwards-
// incompatible way. Restore refuses unknown versions.
const FormatVersion = 1

// secretSettingKeys lists setting keys that must never be included in a backup
// export nor overwritten on restore. They identify the panel instance and
// rotating them out would lock the operator out of their own panel.
var secretSettingKeys = map[string]bool{
	"jwt_secret":        true,
	"setup_entry_path":  true,
	"setup_entry_token": true,
	"tls_cert_path":     true,
	"tls_key_path":      true,
}

// archive is the top-level JSON document embedded in the zip.
type archive struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Inbounds    []model.Inbound     `json:"inbounds"`
	Clients     []model.Client      `json:"clients"`
	Routing     []model.RoutingRule `json:"routing_rules"`
	CronJobs    []model.CronJob     `json:"cron_jobs"`
	Settings    []model.Setting     `json:"settings"`
}

// Export writes a zip archive containing one `backup.json` entry with the
// current logical state. Returns the number of objects exported per table so
// the caller can show a summary to the operator.
func Export(w io.Writer) (map[string]int, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	a := archive{
		Version:     FormatVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := config.DB.Find(&a.Inbounds).Error; err != nil {
		return nil, fmt.Errorf("load inbounds: %w", err)
	}
	if err := config.DB.Find(&a.Clients).Error; err != nil {
		return nil, fmt.Errorf("load clients: %w", err)
	}
	if err := config.DB.Find(&a.Routing).Error; err != nil {
		return nil, fmt.Errorf("load routing rules: %w", err)
	}
	if err := config.DB.Find(&a.CronJobs).Error; err != nil {
		return nil, fmt.Errorf("load cron jobs: %w", err)
	}

	var settings []model.Setting
	if err := config.DB.Find(&settings).Error; err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	for _, s := range settings {
		if secretSettingKeys[s.Key] {
			continue
		}
		a.Settings = append(a.Settings, s)
	}

	zw := zip.NewWriter(w)
	entry, err := zw.Create("backup.json")
	if err != nil {
		return nil, fmt.Errorf("zip create: %w", err)
	}
	enc := json.NewEncoder(entry)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, fmt.Errorf("json encode: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}

	return map[string]int{
		"inbounds":      len(a.Inbounds),
		"clients":       len(a.Clients),
		"routing_rules": len(a.Routing),
		"cron_jobs":     len(a.CronJobs),
		"settings":      len(a.Settings),
	}, nil
}

// ReadArchive parses a previously-exported archive from the given zip body.
// Returns the archive (for inspection or dry-run) plus a descriptive error.
func ReadArchive(body []byte) (*archive, error) {
	zr, err := zip.NewReader(newByteReaderAt(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip archive: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "backup.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open backup.json: %w", err)
		}
		defer rc.Close()
		var a archive
		if err := json.NewDecoder(rc).Decode(&a); err != nil {
			return nil, fmt.Errorf("parse backup.json: %w", err)
		}
		if a.Version != FormatVersion {
			return nil, fmt.Errorf("unsupported archive version %d (expected %d)", a.Version, FormatVersion)
		}
		return &a, nil
	}
	return nil, fmt.Errorf("archive does not contain backup.json")
}

// Restore replaces the logical state of the panel with the contents of the
// archive. Admin accounts, JWT secret, and identifying settings are preserved
// so the operator keeps their login. The operation runs in a single
// transaction — either everything lands, or nothing does.
func Restore(body []byte) (map[string]int, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	a, err := ReadArchive(body)
	if err != nil {
		return nil, err
	}

	counts := map[string]int{
		"inbounds":      len(a.Inbounds),
		"clients":       len(a.Clients),
		"routing_rules": len(a.Routing),
		"cron_jobs":     len(a.CronJobs),
		"settings":      len(a.Settings),
	}

	txErr := config.DB.Transaction(func(tx *gorm.DB) error {
		// Wipe existing logical state. Admins and secret settings are kept.
		if err := tx.Exec("DELETE FROM clients").Error; err != nil {
			return fmt.Errorf("clear clients: %w", err)
		}
		if err := tx.Exec("DELETE FROM inbounds").Error; err != nil {
			return fmt.Errorf("clear inbounds: %w", err)
		}
		if err := tx.Exec("DELETE FROM routing_rules").Error; err != nil {
			return fmt.Errorf("clear routing rules: %w", err)
		}
		if err := tx.Exec("DELETE FROM cron_jobs").Error; err != nil {
			return fmt.Errorf("clear cron jobs: %w", err)
		}

		// Restore in order: inbounds first (other tables reference them).
		if len(a.Inbounds) > 0 {
			if err := tx.CreateInBatches(a.Inbounds, 100).Error; err != nil {
				return fmt.Errorf("restore inbounds: %w", err)
			}
		}
		if len(a.Clients) > 0 {
			if err := tx.CreateInBatches(a.Clients, 200).Error; err != nil {
				return fmt.Errorf("restore clients: %w", err)
			}
		}
		if len(a.Routing) > 0 {
			if err := tx.CreateInBatches(a.Routing, 100).Error; err != nil {
				return fmt.Errorf("restore routing rules: %w", err)
			}
		}
		if len(a.CronJobs) > 0 {
			if err := tx.CreateInBatches(a.CronJobs, 100).Error; err != nil {
				return fmt.Errorf("restore cron jobs: %w", err)
			}
		}

		// Settings: upsert only non-secret keys.
		for _, s := range a.Settings {
			if secretSettingKeys[s.Key] {
				continue
			}
			if err := tx.Save(&s).Error; err != nil {
				return fmt.Errorf("restore setting %s: %w", s.Key, err)
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return counts, nil
}

// byteReaderAt adapts a []byte to io.ReaderAt so archive/zip can read without
// requiring the caller to write the body to a temp file.
type byteReaderAt struct {
	data []byte
}

func newByteReaderAt(b []byte) *byteReaderAt { return &byteReaderAt{data: b} }

func (b *byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
