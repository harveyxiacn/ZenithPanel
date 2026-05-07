package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// WARPConfig holds the WireGuard keys and endpoint returned by the WARP registration API.
type WARPConfig struct {
	PrivateKey   string `json:"private_key"`
	PublicKey    string `json:"public_key"`     // peer public key
	Endpoint     string `json:"endpoint"`       // WARP anycast endpoint
	EndpointPort int    `json:"endpoint_port"`
	Address      string `json:"address"`        // assigned IPv4/IPv6 inside WARP
	ReservedHex  string `json:"reserved_hex"`   // reserved bytes (3 octets hex, for some clients)
}

// FetchWARPConfig retrieves WireGuard credentials from the Cloudflare WARP
// registration endpoint using the provided account ID and access token.
// The returned WARPConfig can be serialised to JSON and stored as Outbound.Config.
func FetchWARPConfig(accountID, token string) (*WARPConfig, error) {
	if accountID == "" || token == "" {
		return nil, fmt.Errorf("accountID and token are required")
	}

	url := fmt.Sprintf("https://api.cloudflareclient.com/v0a4005/reg/%s/devices", accountID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "okhttp/3.12.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WARP API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WARP API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Config struct {
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					V4 string `json:"v4"`
				} `json:"endpoint"`
			} `json:"peers"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
				} `json:"addresses"`
				PrivateKey string `json:"private_key"`
			} `json:"interface"`
		} `json:"config"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse WARP response: %w", err)
	}

	cfg := &WARPConfig{
		PrivateKey:   result.Config.Interface.PrivateKey,
		Address:      result.Config.Interface.Addresses.V4,
		Endpoint:     "engage.cloudflareclient.com",
		EndpointPort: 2408,
	}
	if len(result.Config.Peers) > 0 {
		cfg.PublicKey = result.Config.Peers[0].PublicKey
		if ep := result.Config.Peers[0].Endpoint.V4; ep != "" {
			cfg.Endpoint = ep
		}
	}

	return cfg, nil
}
