package firewall

import (
	"strings"
	"testing"
)

func TestCatchAllPosition(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want int
	}{
		{
			name: "oracle cloud image default",
			spec: `-P INPUT ACCEPT
-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -p icmp -j ACCEPT
-A INPUT -i lo -j ACCEPT
-A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
-A INPUT -j REJECT --reject-with icmp-host-prohibited
`,
			want: 5,
		},
		{
			name: "bare drop",
			spec: "-P INPUT ACCEPT\n-A INPUT -i lo -j ACCEPT\n-A INPUT -j DROP\n",
			want: 2,
		},
		{
			name: "conditional drop is not a catch-all",
			spec: "-P INPUT ACCEPT\n-A INPUT -p tcp -m tcp --dport 25 -j DROP\n-A INPUT -s 10.0.0.0/8 -j REJECT --reject-with icmp-port-unreachable\n",
			want: 0,
		},
		{
			name: "no rules",
			spec: "-P INPUT DROP\n",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := catchAllPosition(tc.spec); got != tc.want {
				t.Fatalf("catchAllPosition() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRuleComment(t *testing.T) {
	cases := map[string]string{
		"tcp dpt:443 /* Cloudflare */":      "Cloudflare",
		"tcp dpt:443 /* CF-Block-Others */": "CF-Block-Others",
		"/* my rule 1 */":                   "my rule 1",
		"tcp dpt:22":                        "",
		"":                                  "",
		"/* unterminated":                   "",
	}
	for in, want := range cases {
		if got := ruleComment(in); got != want {
			t.Errorf("ruleComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlocksByDefaultAndHasAccept(t *testing.T) {
	oracle := `-P INPUT ACCEPT
-A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 443 -m comment --comment vless-reality -j ACCEPT
-A INPUT -p udp -m udp --dport 8443 -j ACCEPT
-A INPUT -j REJECT --reject-with icmp-host-prohibited
`
	if !blocksByDefault(oracle) {
		t.Error("catch-all REJECT should count as blocking")
	}
	if !blocksByDefault("-P INPUT DROP\n-A INPUT -i lo -j ACCEPT\n") {
		t.Error("DROP policy should count as blocking")
	}
	if blocksByDefault("-P INPUT ACCEPT\n-A INPUT -p tcp --dport 25 -j DROP\n") {
		t.Error("open firewall with a targeted DROP is not blocking by default")
	}
	cases := []struct {
		proto, port string
		want        bool
	}{
		{"tcp", "443", true}, {"tcp", "22", true}, {"udp", "8443", true},
		{"udp", "443", false}, {"tcp", "8443", false}, {"tcp", "2096", false},
	}
	for _, c := range cases {
		if got := hasAccept(oracle, c.proto, c.port); got != c.want {
			t.Errorf("hasAccept(%s/%s) = %v, want %v", c.proto, c.port, got, c.want)
		}
	}
}

func TestStaleManagedRules(t *testing.T) {
	spec := `-P INPUT ACCEPT
-A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 443 -m comment --comment zenith-vless-reality -j ACCEPT
-A INPUT -p udp -m udp --dport 8443 -m comment --comment zenith-smart-weaknet-hy2 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 8080 -m comment --comment "my own rule" -j ACCEPT
-A INPUT -p tcp -m tcp --dport 9000 -j ACCEPT
-A INPUT -j REJECT --reject-with icmp-host-prohibited
`
	got := staleManagedRules(spec, map[string]bool{"tcp/443": true})
	if len(got) != 1 {
		t.Fatalf("want exactly the stale managed udp/8443 rule, got %v", got)
	}
	if want := "-p udp -m udp --dport 8443 -m comment --comment zenith-smart-weaknet-hy2 -j ACCEPT"; strings.Join(got[0], " ") != want {
		t.Fatalf("spec = %q", strings.Join(got[0], " "))
	}
}

func TestFirewallFamilySelection(t *testing.T) {
	for _, tc := range []struct{ family, want string }{
		{"", "iptables"}, {"ipv4", "iptables"}, {"ipv6", "ip6tables"},
	} {
		got, err := firewallBinary(tc.family)
		if err != nil || got != tc.want {
			t.Errorf("family %q: got %q, %v; want %q", tc.family, got, err, tc.want)
		}
	}
	if _, err := firewallBinary("both"); err == nil {
		t.Fatal("unknown family accepted")
	}
}

func TestRejectMismatchedSourceAndProtocolBeforeExecuting(t *testing.T) {
	for _, tc := range []struct{ family, protocol, source string }{
		{"ipv4", "tcp", "2001:db8::/32"},
		{"ipv6", "udp", "192.0.2.1"},
		{"ipv6", "icmp", ""},
		{"ipv4", "ipv6-icmp", ""},
	} {
		if err := AddRuleForFamily(tc.family, tc.protocol, "", "ACCEPT", tc.source, ""); err == nil {
			t.Errorf("accepted incompatible rule: %+v", tc)
		}
	}
}

func TestDeleteRejectsUnrepresentedMatches(t *testing.T) {
	base := Rule{Family: "ipv6", Chain: "INPUT", Options: "--", InInterface: "*", OutInterface: "*", Protocol: "tcp", Target: "ACCEPT", Source: "::/0", Destination: "::/0", Port: "443", Extra: "tcp dpt:443"}
	for _, modify := range []func(*Rule){
		func(r *Rule) { r.InInterface = "eth0" },
		func(r *Rule) { r.Extra += " ctstate NEW" },
		func(r *Rule) { r.Extra += " /* comment */" },
	} {
		rule := base
		modify(&rule)
		if err := DeleteRule(rule); err == nil {
			t.Fatalf("accepted rule with additional matches: %+v", rule)
		}
	}
}
