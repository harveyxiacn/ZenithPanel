package firewall

import (
	"fmt"
	"log"
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
		if len(fields) < 10 {
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

// catchAllPosition returns the 1-based INPUT position of the first
// unconditional DROP/REJECT rule in `iptables -S INPUT` output, or 0 if the
// chain has none. Cloud images (Oracle Cloud's Ubuntu/OL images in
// particular) end INPUT with `-j REJECT --reject-with icmp-host-prohibited`;
// anything appended after it never matches.
func catchAllPosition(spec string) int {
	pos := 0
	for _, line := range strings.Split(spec, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "-A" {
			continue
		}
		pos++
		rest := fields[2:]
		if len(rest) < 2 || rest[0] != "-j" || (rest[1] != "DROP" && rest[1] != "REJECT") {
			continue
		}
		// Only `-j REJECT [--reject-with X]` / `-j DROP` counts as a catch-all.
		if len(rest) == 2 || (len(rest) == 4 && rest[2] == "--reject-with") {
			return pos
		}
	}
	return 0
}

// AddRule adds a validated rule to the INPUT chain. It is inserted just
// before any catch-all DROP/REJECT rule so it actually takes effect;
// otherwise it is appended.
func AddRule(protocol, port, action, source, comment string) error {
	if err := validateRule(protocol, port, action, source); err != nil {
		return err
	}
	proto := strings.ToLower(protocol)
	if port != "" && proto != "tcp" && proto != "udp" {
		return fmt.Errorf("invalid rule: a port requires protocol tcp or udp")
	}

	args := []string{"-A", "INPUT"}
	if out, err := exec.Command("iptables", "-S", "INPUT").Output(); err == nil {
		if pos := catchAllPosition(string(out)); pos > 0 {
			args = []string{"-I", "INPUT", strconv.Itoa(pos)}
		}
	}
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

// blocksByDefault reports whether INPUT would reject traffic that no rule
// accepts: a DROP policy or a catch-all DROP/REJECT rule.
func blocksByDefault(spec string) bool {
	if catchAllPosition(spec) > 0 {
		return true
	}
	for _, line := range strings.Split(spec, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "-P" && f[1] == "INPUT" && f[2] == "DROP" {
			return true
		}
	}
	return false
}

// hasAccept reports whether spec has an ACCEPT rule for proto/port
// (as printed by `iptables -S`, e.g. "-p tcp -m tcp --dport 443 … -j ACCEPT").
func hasAccept(spec, proto, port string) bool {
	for _, line := range strings.Split(spec, "\n") {
		f := strings.Fields(line)
		var p, dport string
		accept := false
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "-p":
				p = f[i+1]
			case "--dport":
				dport = f[i+1]
			case "-j":
				accept = f[i+1] == "ACCEPT"
			}
		}
		if accept && p == proto && dport == port {
			return true
		}
	}
	return false
}

// EnsureOpen makes sure proto/port is accepted on a host whose INPUT chain
// blocks by default (e.g. Oracle Cloud images). It is a no-op when the
// firewall is open anyway or the rule already exists. The panel calls it
// for its own listeners at startup and after applying proxy configs: rules
// added through iptables don't survive a host reboot, and the containerised
// panel can't write the host's persistence files. Returns whether a rule
// was added.
func EnsureOpen(proto, port, comment string) (bool, error) {
	out, err := exec.Command("iptables", "-S", "INPUT").Output()
	if err != nil {
		return false, fmt.Errorf("iptables: %w", err)
	}
	spec := string(out)
	if !blocksByDefault(spec) || hasAccept(spec, proto, port) {
		return false, nil
	}
	return true, AddRule(proto, port, "ACCEPT", "", comment)
}

// ManagedCommentPrefix marks INPUT rules the panel opened for its own
// listeners (see EnsureOpen callers). Only rules carrying it are ever
// pruned automatically; rules an operator added are left alone.
const ManagedCommentPrefix = "zenith-"

// staleManagedRules returns the `iptables -S` rule specs (without the
// leading "-A INPUT") of panel-managed ACCEPT rules whose proto/port is not
// in keep (keys "tcp/443").
func staleManagedRules(spec string, keep map[string]bool) [][]string {
	var out [][]string
	for _, line := range strings.Split(spec, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "-A" || f[1] != "INPUT" {
			continue
		}
		var proto, port, comment string
		for i := 2; i+1 < len(f); i++ {
			switch f[i] {
			case "-p":
				proto = f[i+1]
			case "--dport":
				port = f[i+1]
			case "--comment":
				comment = strings.Trim(f[i+1], `"`)
			}
		}
		if strings.HasPrefix(comment, ManagedCommentPrefix) && proto != "" && port != "" && !keep[proto+"/"+port] {
			out = append(out, f[2:])
		}
	}
	return out
}

// PruneManaged deletes panel-managed ACCEPT rules for ports no enabled
// listener uses any more (e.g. after a node is deleted or moved), so the
// host doesn't keep unused ports open. Returns how many rules were removed.
func PruneManaged(keep map[string]bool) (int, error) {
	out, err := exec.Command("iptables", "-S", "INPUT").Output()
	if err != nil {
		return 0, fmt.Errorf("iptables: %w", err)
	}
	n := 0
	for _, rule := range staleManagedRules(string(out), keep) {
		args := append([]string{"-D", "INPUT"}, rule...)
		if b, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
			return n, fmt.Errorf("iptables %s: %s", strings.Join(args, " "), strings.TrimSpace(string(b)))
		}
		n++
	}
	return n, nil
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
			// Cleanup pass — keep going past a failed delete so we still
			// remove every remaining Cloudflare/Block rule. Per-rule failure
			// is logged but doesn't abort the loop.
			if err := DeleteRule(r); err != nil {
				log.Printf("firewall.DeleteRule(%s): %v", r.Extra, err)
			}
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

// DeleteRule removes a rule from the INPUT chain by reconstructing its full spec.
// This avoids the line-number shift bug where inserting rules changes line numbers
// and causes subsequent deletions to target the wrong rule.
func DeleteRule(r Rule) error {
	args := []string{"-D", "INPUT"}
	proto := strings.ToLower(r.Protocol)
	if proto != "" && proto != "all" {
		args = append(args, "-p", proto)
	}
	if r.Source != "" && r.Source != "0.0.0.0/0" {
		args = append(args, "-s", r.Source)
	}
	if r.Port != "" {
		args = append(args, "--dport", r.Port)
	}
	// `iptables -D` matches the full rule spec, so a rule carrying a comment
	// can only be deleted if the comment match is included too.
	if c := ruleComment(r.Extra); c != "" {
		args = append(args, "-m", "comment", "--comment", c)
	}
	if r.Target == "" {
		return fmt.Errorf("rule target is required")
	}
	args = append(args, "-j", strings.ToUpper(r.Target))
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ruleComment extracts the `/* comment */` annotation from the trailing
// column of `iptables -L -v` output.
func ruleComment(extra string) string {
	start := strings.Index(extra, "/* ")
	if start == -1 {
		return ""
	}
	end := strings.Index(extra[start+3:], " */")
	if end == -1 {
		return ""
	}
	return extra[start+3 : start+3+end]
}
