package deploy

import (
	"encoding/json"
	"fmt"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// Extract credentials from either engine's preset shape, removing inline
// copies so disabling/deleting a persisted client cannot reactivate a template.
func presetClients(settings map[string]any) ([]model.Client, map[string]any, error) {
	clean := make(map[string]any, len(settings))
	for k, v := range settings {
		clean[k] = v
	}
	var clients []model.Client
	for _, key := range []string{"clients", "users"} {
		raw, ok := clean[key]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, nil, err
		}
		var rows []struct{ ID, UUID, Password, Email, Name, Flow string }
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			credential := r.ID
			if credential == "" {
				credential = r.UUID
			}
			if credential == "" {
				credential = r.Password
			}
			if credential == "" {
				return nil, nil, fmt.Errorf("preset client is missing credentials")
			}
			email := r.Email
			if email == "" {
				email = r.Name
			}
			if email == "" {
				email = "default"
			}
			clients = append(clients, model.Client{Email: email, UUID: credential, Enable: true})
			if r.Flow != "" {
				clean["flow"] = r.Flow
			}
		}
		delete(clean, key)
	}
	return clients, clean, nil
}

// Store QUIC presets in the same transport schema used by the visual editor,
// subscription generator and inbound probes.
func canonicalStream(spec InboundSpec) map[string]any {
	native, ok := spec.Stream["tls"].(map[string]any)
	if !ok {
		return spec.Stream
	}
	tls := map[string]any{"serverName": native["server_name"], "alpn": []string{"h3"}, "certificates": []map[string]any{{"certificateFile": native["certificate_path"], "keyFile": native["key_path"]}}}
	if insecure, ok := native["insecure"].(bool); ok && insecure {
		tls["allowInsecure"] = true
	}
	return map[string]any{"network": "udp", "security": "tls", "tlsSettings": tls}
}
