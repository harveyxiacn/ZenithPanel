package proxy

import (
	"sort"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func normalizeRuleCSV(raw string) string {
	parts := splitAndTrimCSV(raw)
	if len(parts) == 0 {
		return ""
	}

	unique := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, part)
	}

	sort.Slice(unique, func(i, j int) bool {
		return strings.ToLower(unique[i]) < strings.ToLower(unique[j])
	})

	return strings.Join(unique, ",")
}

func NormalizeRoutingRule(rule model.RoutingRule) model.RoutingRule {
	rule.RuleTag = strings.TrimSpace(rule.RuleTag)
	rule.Domain = normalizeRuleCSV(rule.Domain)
	rule.IP = normalizeRuleCSV(rule.IP)
	rule.Port = normalizeRuleCSV(rule.Port)
	rule.OutboundTag = strings.TrimSpace(rule.OutboundTag)
	return rule
}

func RoutingRuleSignature(rule model.RoutingRule) string {
	normalized := NormalizeRoutingRule(rule)
	return strings.ToLower(strings.Join([]string{
		normalized.Domain,
		normalized.IP,
		normalized.Port,
		normalized.OutboundTag,
	}, "|"))
}

func UniqueRoutingRules(rules []model.RoutingRule) []model.RoutingRule {
	unique := make([]model.RoutingRule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))

	for _, rule := range rules {
		normalized := NormalizeRoutingRule(rule)
		signature := RoutingRuleSignature(normalized)
		if _, ok := seen[signature]; ok {
			continue
		}
		seen[signature] = struct{}{}
		unique = append(unique, normalized)
	}

	return unique
}

func CleanupDuplicateRoutingRules() (int, error) {
	var rules []model.RoutingRule
	if err := config.DB.Order("id ASC").Find(&rules).Error; err != nil {
		return 0, err
	}

	duplicateIDs := make([]uint, 0)
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		signature := RoutingRuleSignature(rule)
		if _, ok := seen[signature]; ok {
			duplicateIDs = append(duplicateIDs, rule.ID)
			continue
		}
		seen[signature] = struct{}{}
	}

	if len(duplicateIDs) == 0 {
		return 0, nil
	}

	if err := config.DB.Delete(&model.RoutingRule{}, duplicateIDs).Error; err != nil {
		return 0, err
	}

	return len(duplicateIDs), nil
}
