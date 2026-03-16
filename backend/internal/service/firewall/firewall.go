package firewall

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Rule represents a single iptables rule
type Rule struct {
	Chain       string `json:"chain"`
	Num         string `json:"num"`
	Target      string `json:"target"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Port        string `json:"port"`
	Extra       string `json:"extra"`
}

var (
	validProtocols = map[string]bool{"tcp": true, "udp": true, "icmp": true, "all": true}
	validActions   = map[string]bool{"ACCEPT": true, "DROP": true, "REJECT": true}
	portRangeRe    = regexp.MustCompile(`^\d+(?::\d+)?$`)
	ruleNumRe      = regexp.MustCompile(`^\d+$`)
)

// validateRule checks all user-supplied parameters before passing them to iptables.
func validateRule(protocol, port, action, source string) error {
	if protocol != "" && !validProtocols[strings.ToLower(protocol)] {
		return fmt.Errorf("invalid protocol: must be tcp, udp, icmp, or all")
	}
	if port != "" {
		if !portRangeRe.MatchString(port) {
			return fmt.Errorf("invalid port: must be a number (80) or range (80:90)")
		}
		for _, p := range strings.SplitN(port, ":", 2) {
			n, _ := strconv.Atoi(p)
			if n < 1 || n > 65535 {
				return fmt.Errorf("invalid port: must be between 1 and 65535")
			}
		}
	}
	if !validActions[strings.ToUpper(action)] {
		return fmt.Errorf("invalid action: must be ACCEPT, DROP, or REJECT")
	}
	if source != "" {
		if _, _, err := net.ParseCIDR(source); err != nil {
			if net.ParseIP(source) == nil {
				return fmt.Errorf("invalid source: must be a valid IP or CIDR (e.g. 1.2.3.4 or 1.2.3.0/24)")
			}
		}
	}
	return nil
}

// ListRules returns the current INPUT chain rules from iptables
func ListRules() ([]Rule, error) {
	out, err := exec.Command("iptables", "-L", "INPUT", "-n", "--line-numbers", "-v").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("iptables: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	var rules []Rule
	lines := strings.Split(string(out), "\n")
	// Skip header lines (first 2 lines)
	for i, line := range lines {
		if i < 2 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		r := Rule{
			Chain:       "INPUT",
			Num:         fields[0],
			Target:      fields[3],
			Protocol:    fields[4],
			Source:      fields[8],
			Destination: fields[9],
		}
		rest := strings.Join(fields[10:], " ")
		if idx := strings.Index(rest, "dpt:"); idx != -1 {
			port := rest[idx+4:]
			if sp := strings.IndexByte(port, ' '); sp != -1 {
				port = port[:sp]
			}
			r.Port = port
		}
		r.Extra = rest
		rules = append(rules, r)
	}
	return rules, nil
}

// AddRule appends a validated rule to the INPUT chain
func AddRule(protocol, port, action, source, comment string) error {
	if err := validateRule(protocol, port, action, source); err != nil {
		return err
	}

	args := []string{"-A", "INPUT"}
	if protocol != "" && strings.ToLower(protocol) != "all" {
		args = append(args, "-p", strings.ToLower(protocol))
	}
	if port != "" {
		args = append(args, "--dport", port)
	}
	if source != "" {
		args = append(args, "-s", source)
	}
	if comment != "" {
		// Allowlist safe characters only (alphanumeric, spaces, hyphens, underscores)
		var safe []byte
		for _, c := range []byte(comment) {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ' ' || c == '-' || c == '_' || c == '.' {
				safe = append(safe, c)
			}
		}
		comment = string(safe)
		if len(comment) > 64 {
			comment = comment[:64]
		}
		if comment != "" {
			args = append(args, "-m", "comment", "--comment", comment)
		}
	}
	args = append(args, "-j", strings.ToUpper(action))

	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CloudflareIPv4Ranges contains the official Cloudflare IPv4 ranges.
// Source: https://www.cloudflare.com/ips-v4/
var CloudflareIPv4Ranges = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
}

// ApplyCloudflareProtection adds iptables rules to only allow Cloudflare IPs
// on the specified port, then drops all other traffic to that port.
// It first removes any existing Cloudflare rules for that port.
func ApplyCloudflareProtection(port string) error {
	if port == "" {
		return fmt.Errorf("port is required")
	}
	if err := validateRule("tcp", port, "ACCEPT", ""); err != nil {
		return err
	}

	// Remove existing Cloudflare rules for this port first
	RemoveCloudflareProtection(port)

	// Add ACCEPT rules for each Cloudflare IP range
	for _, cidr := range CloudflareIPv4Ranges {
		if err := AddRule("tcp", port, "ACCEPT", cidr, "Cloudflare"); err != nil {
			return fmt.Errorf("failed to add rule for %s: %w", cidr, err)
		}
	}

	// Add final DROP rule for all other traffic on this port
	if err := AddRule("tcp", port, "DROP", "", "CF-Block-Others"); err != nil {
		return fmt.Errorf("failed to add drop rule: %w", err)
	}

	return nil
}

// RemoveCloudflareProtection removes all Cloudflare-related firewall rules for the given port.
func RemoveCloudflareProtection(port string) {
	// List rules and remove matching ones in reverse order (to preserve numbering)
	rules, err := ListRules()
	if err != nil {
		return
	}
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		if r.Port == port && (strings.Contains(r.Extra, "Cloudflare") || strings.Contains(r.Extra, "CF-Block-Others")) {
			DeleteRule(r.Num)
		}
	}
}

// IsCloudflareProtected checks if Cloudflare protection rules exist for the given port.
func IsCloudflareProtected(port string) bool {
	rules, err := ListRules()
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Port == port && strings.Contains(r.Extra, "Cloudflare") {
			return true
		}
	}
	return false
}

// DeleteRule removes a rule from the INPUT chain by line number
func DeleteRule(num string) error {
	if !ruleNumRe.MatchString(num) {
		return fmt.Errorf("invalid rule number: must be a positive integer")
	}
	out, err := exec.Command("iptables", "-D", "INPUT", num).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
