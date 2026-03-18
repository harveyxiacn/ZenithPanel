package proxy

import (
	"reflect"
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func TestSplitAndTrimCSV(t *testing.T) {
	input := " geosite:cn, geoip:private , ,443 , 8443-9443 "
	got := splitAndTrimCSV(input)
	want := []string{"geosite:cn", "geoip:private", "443", "8443-9443"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitAndTrimCSV() = %#v, want %#v", got, want)
	}
}

func TestBuildXrayRoutingRuleIncludesPortAndSlices(t *testing.T) {
	rule := model.RoutingRule{
		OutboundTag: "block",
		Domain:      "geosite:category-ads-all, geosite:cn",
		IP:          "geoip:private, 1.1.1.1",
		Port:        "443, 8443-9443",
	}

	got := buildXrayRoutingRule(rule)

	if got["type"] != "field" {
		t.Fatalf("expected type=field, got %v", got["type"])
	}
	if got["outboundTag"] != "block" {
		t.Fatalf("expected outboundTag=block, got %v", got["outboundTag"])
	}

	wantDomains := []string{"geosite:category-ads-all", "geosite:cn"}
	if !reflect.DeepEqual(got["domain"], wantDomains) {
		t.Fatalf("expected domain=%#v, got %#v", wantDomains, got["domain"])
	}

	wantIPs := []string{"geoip:private", "1.1.1.1"}
	if !reflect.DeepEqual(got["ip"], wantIPs) {
		t.Fatalf("expected ip=%#v, got %#v", wantIPs, got["ip"])
	}

	if got["port"] != "443,8443-9443" {
		t.Fatalf("expected port=%q, got %#v", "443,8443-9443", got["port"])
	}
}

func TestBuildXrayRoutingRuleOmitsEmptyFields(t *testing.T) {
	rule := model.RoutingRule{OutboundTag: "direct"}
	got := buildXrayRoutingRule(rule)

	if _, ok := got["domain"]; ok {
		t.Fatalf("expected empty domain to be omitted")
	}
	if _, ok := got["ip"]; ok {
		t.Fatalf("expected empty ip to be omitted")
	}
	if _, ok := got["port"]; ok {
		t.Fatalf("expected empty port to be omitted")
	}
}
