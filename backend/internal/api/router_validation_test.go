package api

import (
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

func TestValidateInboundRequiresServerAddressWhenNoSafePublicHostExists(t *testing.T) {
	inbound := model.Inbound{
		Tag:      "vless-reality",
		Protocol: "vless",
		Port:     443,
		Settings: `{"decryption":"none","flow":"xtls-rprx-vision"}`,
		Stream: `{
			"network":"tcp",
			"security":"reality",
			"realitySettings":{
				"target":"www.microsoft.com:443",
				"serverNames":["www.microsoft.com"],
				"privateKey":"priv",
				"shortIds":["ab"]
			}
		}`,
	}

	if msg := validateInbound(inbound); msg == "" {
		t.Fatal("expected validation error when inbound has no explicit or derivable public host")
	}
}

func TestValidateInboundAllowsTLSDerivedPublicHostWithoutServerAddress(t *testing.T) {
	inbound := model.Inbound{
		Tag:      "trojan-tls",
		Protocol: "trojan",
		Port:     443,
		Settings: `{}`,
		Stream: `{
			"network":"tcp",
			"security":"tls",
			"tlsSettings":{
				"serverName":"edge.example.com"
			}
		}`,
	}

	if msg := validateInbound(inbound); msg != "" {
		t.Fatalf("expected inbound with TLS serverName to pass validation, got %q", msg)
	}
}
