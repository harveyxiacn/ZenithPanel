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
