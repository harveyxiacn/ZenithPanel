package firewall

import (
	"fmt"
	"os/exec"
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
			Source:       fields[8],
			Destination: fields[9],
		}
		// Extract dport if present
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

// AddRule appends a rule to the INPUT chain
func AddRule(protocol, port, action, source, comment string) error {
	args := []string{"-A", "INPUT"}
	if protocol != "" && protocol != "all" {
		args = append(args, "-p", protocol)
	}
	if port != "" {
		args = append(args, "--dport", port)
	}
	if source != "" {
		args = append(args, "-s", source)
	}
	if comment != "" {
		args = append(args, "-m", "comment", "--comment", comment)
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
	out, err := exec.Command("iptables", "-D", "INPUT", num).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
