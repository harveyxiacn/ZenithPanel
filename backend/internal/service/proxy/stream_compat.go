package proxy

import "strings"

type RealityStreamInfo struct {
	Target      string
	ServerNames []string
	ShortIDs    []string
	PublicKey   string
	Fingerprint string
	ServerName  string
	SpiderX     string
}

func normalizeStringSlice(raw interface{}) []string {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return splitAndTrimCSV(v)
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmptyString(values ...interface{}) string {
	for _, value := range values {
		if s, ok := value.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func ReadRealityStreamInfo(stream map[string]interface{}) RealityStreamInfo {
	info := RealityStreamInfo{}
	reality, ok := stream["realitySettings"].(map[string]interface{})
	if !ok {
		return info
	}

	info.Target = firstNonEmptyString(reality["target"], reality["dest"])
	info.ServerNames = normalizeStringSlice(reality["serverNames"])
	info.ShortIDs = normalizeStringSlice(reality["shortIds"])
	info.PublicKey = firstNonEmptyString(reality["publicKey"])
	info.Fingerprint = firstNonEmptyString(reality["fingerprint"])

	if settings, ok := reality["settings"].(map[string]interface{}); ok {
		if info.PublicKey == "" {
			info.PublicKey = firstNonEmptyString(settings["publicKey"])
		}
		if info.Fingerprint == "" {
			info.Fingerprint = firstNonEmptyString(settings["fingerprint"])
		}
		info.ServerName = firstNonEmptyString(settings["serverName"])
		info.SpiderX = firstNonEmptyString(settings["spiderX"])
	}

	return info
}

func NormalizeXrayStreamSettings(stream map[string]interface{}) map[string]interface{} {
	if stream == nil {
		return nil
	}

	network, _ := stream["network"].(string)
	if network == "tcp" {
		if _, ok := stream["tcpSettings"]; !ok {
			stream["tcpSettings"] = map[string]interface{}{
				"acceptProxyProtocol": false,
				"header": map[string]interface{}{
					"type": "none",
				},
			}
		}
	}

	security, _ := stream["security"].(string)
	if security != "reality" {
		return stream
	}

	reality, ok := stream["realitySettings"].(map[string]interface{})
	if !ok {
		return stream
	}

	info := ReadRealityStreamInfo(stream)
	if info.Target != "" {
		reality["dest"] = info.Target // Xray-core uses "dest", not "target"
	}
	if len(info.ServerNames) > 0 {
		reality["serverNames"] = info.ServerNames
	}
	if len(info.ShortIDs) > 0 {
		reality["shortIds"] = info.ShortIDs
	}

	delete(reality, "target") // normalize legacy "target" → "dest"
	delete(reality, "publicKey")
	delete(reality, "fingerprint")
	delete(reality, "serverName")
	delete(reality, "spiderX")
	delete(reality, "settings")

	stream["realitySettings"] = reality
	return stream
}
