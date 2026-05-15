// Package adblock manages the panel-controlled ad-block routing rule. When
// enabled, a single routing rule sends `geosite:category-ads-all` traffic to
// the `block` outbound. When disabled, that rule is removed.
//
// The rule is identified by its rule_tag (`Block Ads (panel-managed)`) so
// user-added rules with similar content aren't accidentally touched. Idempotent
// on both directions: re-applying the same state is a no-op.
package adblock

import (
	"fmt"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// managedTag pins the row that this package owns. Users editing routing rules
// in the Web UI won't pick this string, so the package can safely upsert and
// delete it without trampling user rules.
const managedTag = "Block Ads (panel-managed)"

// SettingKey is the persisted on/off flag. Lives alongside other Settings rows
// so the existing config.GetSetting/SetSetting helpers can read/write it.
const SettingKey = "adblock_enabled"

// adsRuleDomain expands at runtime to whatever geosite-category-ads-all
// resolves to on the active engine. Xray reads it from local geosite.dat;
// Sing-box pulls it from SagerNet's rule-set via the existing rule_set
// machinery. Either way, the panel just passes the geosite: prefix through.
const adsRuleDomain = "geosite:category-ads-all"

// IsEnabled returns whether ad-block is currently on according to the
// settings store. Defaults to false on first boot so brand-new panels don't
// surprise operators with unsolicited blocking.
func IsEnabled(getSetting func(string) string) bool {
	return getSetting(SettingKey) == "true"
}

// Apply makes the on-disk routing-rules table match the desired state. Returns
// nil if no change was required so callers can decide whether to trigger a
// proxy.apply on top.
func Apply(db *gorm.DB, enabled bool) error {
	var existing model.RoutingRule
	found := db.Where("rule_tag = ?", managedTag).First(&existing).Error == nil

	switch {
	case enabled && !found:
		row := &model.RoutingRule{
			RuleTag:     managedTag,
			Domain:      adsRuleDomain,
			OutboundTag: "block",
			Enable:      true,
		}
		if err := db.Create(row).Error; err != nil {
			return fmt.Errorf("create adblock rule: %w", err)
		}
	case enabled && found && !existing.Enable:
		// Row exists but was disabled manually; re-enable.
		existing.Enable = true
		if err := db.Save(&existing).Error; err != nil {
			return fmt.Errorf("re-enable adblock rule: %w", err)
		}
	case !enabled && found:
		// Hard-delete so the row count matches user expectations: when you
		// turn ad-block off the row is gone, not just disabled. The audit
		// trail is in audit_logs anyway.
		if err := db.Unscoped().Delete(&existing).Error; err != nil {
			return fmt.Errorf("delete adblock rule: %w", err)
		}
	}
	return nil
}
