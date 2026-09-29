package firewall

import "testing"

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
