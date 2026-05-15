package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Run is the CLI entry point. It returns the process exit code:
//
//	0 success
//	1 generic CLI failure (parse error, validation, etc.)
//	2 HTTP 4xx
//	3 HTTP 5xx
//	4 transport failure (DNS/refused/timeout)
func Run(argv []string) int {
	if len(argv) < 2 {
		printHelp()
		return 1
	}
	// Global flags must come before the subcommand on the CLI line. We do
	// a single pre-pass to extract them so each subcommand handler gets a
	// clean argv.
	gf := parseGlobalFlags(argv[1:])

	if gf.showHelp || len(gf.rest) == 0 {
		printHelp()
		if gf.showHelp {
			return 0
		}
		return 1
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	profile, err := resolveProfile(cfg, gf.host, gf.token, gf.profile)
	if err != nil && !isInfraCmd(gf.rest[0]) {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	client := NewClient(profile)

	cmd := gf.rest[0]
	args := gf.rest[1:]
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
		return 0
	case "version":
		fmt.Println("zenithctl v1")
		return 0
	case "status":
		return runSimpleGet(client, "/api/v1/health", gf)
	case "login":
		return runLogin(client, cfg, gf)
	case "logout":
		return runLogout(cfg, gf)
	case "token":
		return runToken(client, cfg, args, gf)
	case "system":
		return runSystem(client, args, gf)
	case "inbound":
		return runInbound(client, args, gf)
	case "client":
		return runClient(client, args, gf)
	case "proxy":
		return runProxy(client, args, gf)
	case "sub":
		return runSub(client, args, gf)
	case "firewall":
		return runFirewall(client, args, gf)
	case "backup":
		return runBackup(client, args, gf)
	case "raw":
		return runRaw(client, args, gf)
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		printHelp()
		return 1
	}
}

type globalFlags struct {
	host     string
	socket   string
	token    string
	profile  string
	output   string
	quiet    bool
	showHelp bool
	rest     []string
}

func parseGlobalFlags(args []string) globalFlags {
	g := globalFlags{output: "json"}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--host" && i+1 < len(args):
			g.host = args[i+1]
			i++
		case strings.HasPrefix(a, "--host="):
			g.host = strings.TrimPrefix(a, "--host=")
		case a == "--socket" && i+1 < len(args):
			g.socket = args[i+1]
			i++
		case strings.HasPrefix(a, "--socket="):
			g.socket = strings.TrimPrefix(a, "--socket=")
		case a == "--token" && i+1 < len(args):
			g.token = args[i+1]
			i++
		case strings.HasPrefix(a, "--token="):
			g.token = strings.TrimPrefix(a, "--token=")
		case a == "--profile" && i+1 < len(args):
			g.profile = args[i+1]
			i++
		case strings.HasPrefix(a, "--profile="):
			g.profile = strings.TrimPrefix(a, "--profile=")
		case a == "--output" && i+1 < len(args):
			g.output = args[i+1]
			i++
		case strings.HasPrefix(a, "--output="):
			g.output = strings.TrimPrefix(a, "--output=")
		case a == "-q" || a == "--quiet":
			g.quiet = true
		case a == "-h" || a == "--help":
			g.showHelp = true
		default:
			out = append(out, a)
		}
	}
	if g.socket != "" && g.host == "" {
		g.host = "unix://" + g.socket
	}
	g.rest = out
	return g
}

// isInfraCmd is true for commands that should work even without a fully
// resolved profile (printing help, asking for bootstrap, etc.).
func isInfraCmd(c string) bool {
	switch c {
	case "help", "-h", "--help", "version":
		return true
	}
	return false
}

// exitFromEnvelope maps HTTP status into the documented exit codes.
func exitFromEnvelope(status int, env *Envelope, err error, gf globalFlags) int {
	if err != nil {
		if errors.Is(err, ErrTransport) {
			fmt.Fprintln(os.Stderr, "transport:", err)
			return 4
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if status >= 500 {
		fmt.Fprintf(os.Stderr, "server error %d: %s\n", status, env.Msg)
		return 3
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "client error %d: %s\n", status, env.Msg)
		return 2
	}
	if gf.quiet {
		// Emit just the `data` value (raw JSON) so scripts can pipe it.
		if env != nil && len(env.Data) > 0 {
			fmt.Println(string(env.Data))
		}
		return 0
	}
	fmt.Println(Pretty(env))
	return 0
}

func runSimpleGet(c *Client, path string, gf globalFlags) int {
	env, st, err := c.Do("GET", path, nil)
	return exitFromEnvelope(st, env, err, gf)
}

// --- token group -----------------------------------------------------------

func runToken(c *Client, cfg *Config, args []string, gf globalFlags) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl token (list|create|revoke|bootstrap) ...")
		return 1
	}
	switch args[0] {
	case "list":
		return runSimpleGet(c, "/api/v1/admin/api-tokens", gf)
	case "create":
		fs := flag.NewFlagSet("token create", flag.ContinueOnError)
		name := fs.String("name", "", "token name (required)")
		scopes := fs.String("scopes", "*", "comma-separated scopes")
		days := fs.Int("expires-in", 0, "expire in N days; 0 = never")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *name == "" {
			fmt.Fprintln(os.Stderr, "missing --name")
			return 1
		}
		env, st, err := c.Do("POST", "/api/v1/admin/api-tokens", map[string]any{
			"name": *name, "scopes": *scopes, "expires_in_days": *days,
		})
		return exitFromEnvelope(st, env, err, gf)
	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zenithctl token revoke <id>")
			return 1
		}
		id := args[1]
		if _, err := strconv.Atoi(id); err != nil {
			// Resolve name → id via list.
			env, _, err := c.Do("GET", "/api/v1/admin/api-tokens", nil)
			if err != nil || env == nil {
				fmt.Fprintln(os.Stderr, "could not look up token by name")
				return 1
			}
			var rows []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			}
			_ = json.Unmarshal(env.Data, &rows)
			found := false
			for _, r := range rows {
				if r.Name == id {
					id = strconv.Itoa(r.ID)
					found = true
					break
				}
			}
			if !found {
				fmt.Fprintln(os.Stderr, "no token with that name")
				return 2
			}
		}
		env, st, err := c.Do("DELETE", "/api/v1/admin/api-tokens/"+url.PathEscape(id), nil)
		return exitFromEnvelope(st, env, err, gf)
	case "bootstrap":
		return runBootstrap(c, cfg, gf)
	default:
		fmt.Fprintln(os.Stderr, "unknown token subcommand:", args[0])
		return 1
	}
}

func runBootstrap(c *Client, cfg *Config, gf globalFlags) int {
	if !isUnixHost(c.Profile.Host) {
		fmt.Fprintln(os.Stderr, "bootstrap requires the unix socket; run this on the panel host")
		return 1
	}
	env, st, err := c.Do("POST", "/api/v1/admin/api-tokens/bootstrap", nil)
	if err != nil || st != 200 {
		return exitFromEnvelope(st, env, err, gf)
	}
	var data struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	_ = json.Unmarshal(env.Data, &data)
	if data.Token == "" {
		fmt.Fprintln(os.Stderr, "bootstrap returned no token")
		return 1
	}
	// Persist a `local-http` profile so subsequent invocations from a non-root
	// shell on this same host (or from a remote SSH tunnel) can use the token.
	if cfg.Profile == nil {
		cfg.Profile = map[string]Profile{}
	}
	cfg.Profile["local"] = Profile{Host: "unix:///run/zenithpanel.sock"}
	cfg.Profile["bootstrap"] = Profile{Host: "http://127.0.0.1", Token: data.Token}
	if cfg.Default == "" {
		cfg.Default = "local"
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save config:", err)
	}
	fmt.Println("Token created and saved to", ConfigPath())
	fmt.Println("Name:  ", data.Name)
	fmt.Println("Token: ", data.Token)
	fmt.Println("Keep this token safe — it grants full panel access.")
	return 0
}

// --- login (password-based fallback for remote use) -----------------------

func runLogin(c *Client, cfg *Config, gf globalFlags) int {
	if isUnixHost(c.Profile.Host) {
		fmt.Fprintln(os.Stderr, "login isn't needed on the unix socket; use `token bootstrap` to mint a long-lived token")
		return 1
	}
	rdr := bufio.NewReader(os.Stdin)
	fmt.Print("Username: ")
	user, _ := rdr.ReadString('\n')
	fmt.Print("Password: ")
	pass, _ := rdr.ReadString('\n')
	fmt.Print("TOTP (blank if disabled): ")
	totp, _ := rdr.ReadString('\n')
	user = strings.TrimSpace(user)
	pass = strings.TrimSpace(pass)
	totp = strings.TrimSpace(totp)
	body := map[string]string{"username": user, "password": pass}
	if totp != "" {
		body["totp_code"] = totp
	}
	env, st, err := c.Do("POST", "/api/v1/login", body)
	if err != nil || st != 200 || env.Code != 200 {
		return exitFromEnvelope(st, env, err, gf)
	}
	var data struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(env.Data, &data)
	if data.Token == "" {
		fmt.Fprintln(os.Stderr, "login did not return a token")
		return 1
	}
	name := gf.profile
	if name == "" {
		name = "remote"
	}
	if cfg.Profile == nil {
		cfg.Profile = map[string]Profile{}
	}
	p := cfg.Profile[name]
	p.Host = c.Profile.Host
	p.Token = data.Token
	cfg.Profile[name] = p
	if cfg.Default == "" {
		cfg.Default = name
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	fmt.Println("Logged in. JWT cached under profile", name)
	return 0
}

func runLogout(cfg *Config, gf globalFlags) int {
	name := gf.profile
	if name == "" {
		name = cfg.Default
	}
	if p, ok := cfg.Profile[name]; ok {
		p.Token = ""
		cfg.Profile[name] = p
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println("Cleared cached token for profile", name)
	return 0
}

// --- system / inbound / client / proxy / sub / firewall / backup ---------

func runSystem(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl system (info|bbr|swap|sysctl|cleanup) ...")
		return 1
	}
	switch args[0] {
	case "info":
		return runSimpleGet(c, "/api/v1/system/monitor", gf)
	case "bbr":
		if len(args) < 2 {
			return 1
		}
		switch args[1] {
		case "status":
			return runSimpleGet(c, "/api/v1/system/bbr/status", gf)
		case "enable":
			env, st, err := c.Do("POST", "/api/v1/system/bbr/enable", nil)
			return exitFromEnvelope(st, env, err, gf)
		case "disable":
			env, st, err := c.Do("POST", "/api/v1/system/bbr/disable", nil)
			return exitFromEnvelope(st, env, err, gf)
		}
	case "swap":
		if len(args) < 2 {
			return 1
		}
		switch args[1] {
		case "status":
			return runSimpleGet(c, "/api/v1/system/swap/status", gf)
		case "create":
			fs := flag.NewFlagSet("swap create", flag.ContinueOnError)
			size := fs.String("size", "1G", "swap file size (e.g. 1G)")
			if err := fs.Parse(args[2:]); err != nil {
				return 1
			}
			env, st, err := c.Do("POST", "/api/v1/system/swap/create", map[string]string{"size": *size})
			return exitFromEnvelope(st, env, err, gf)
		case "remove":
			env, st, err := c.Do("POST", "/api/v1/system/swap/remove", nil)
			return exitFromEnvelope(st, env, err, gf)
		}
	case "cleanup":
		return runSimpleGet(c, "/api/v1/system/cleanup/info", gf)
	}
	fmt.Fprintln(os.Stderr, "unknown system subcommand")
	return 1
}

func runInbound(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 {
		return runSimpleGet(c, "/api/v1/inbounds", gf)
	}
	switch args[0] {
	case "list":
		return runSimpleGet(c, "/api/v1/inbounds", gf)
	case "show":
		if len(args) < 2 {
			return 1
		}
		return runSimpleGet(c, "/api/v1/inbounds?id="+url.QueryEscape(args[1]), gf)
	case "create", "update":
		fs := flag.NewFlagSet("inbound "+args[0], flag.ContinueOnError)
		file := fs.String("file", "", "path to JSON file")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		body, err := readJSONFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		path := "/api/v1/inbounds"
		method := "POST"
		if args[0] == "update" {
			rest := fs.Args()
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "update requires <id>")
				return 1
			}
			path = path + "/" + rest[0]
			method = "PUT"
		}
		env, st, err := c.Do(method, path, body)
		return exitFromEnvelope(st, env, err, gf)
	case "delete":
		if len(args) < 2 {
			return 1
		}
		env, st, err := c.Do("DELETE", "/api/v1/inbounds/"+url.PathEscape(args[1]), nil)
		return exitFromEnvelope(st, env, err, gf)
	}
	return 1
}

func runClient(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 {
		return runSimpleGet(c, "/api/v1/clients", gf)
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("client list", flag.ContinueOnError)
		inbound := fs.Int("inbound", 0, "filter by inbound id")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		path := "/api/v1/clients"
		if *inbound > 0 {
			path += "?inbound_id=" + strconv.Itoa(*inbound)
		}
		return runSimpleGet(c, path, gf)
	case "add":
		fs := flag.NewFlagSet("client add", flag.ContinueOnError)
		inbound := fs.Int("inbound", 0, "inbound id (required)")
		email := fs.String("email", "", "client email/label (required)")
		uuid := fs.String("uuid", "", "explicit uuid; auto-generated if empty")
		total := fs.Int64("total", 0, "traffic cap in bytes (0=unlimited)")
		expires := fs.Int64("expires", 0, "expiry unix seconds (0=never)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *inbound == 0 || *email == "" {
			fmt.Fprintln(os.Stderr, "--inbound and --email required")
			return 1
		}
		body := map[string]any{
			"inbound_id": *inbound,
			"email":      *email,
			"total":      *total,
			"expiry_time": *expires,
		}
		if *uuid != "" {
			body["uuid"] = *uuid
		}
		env, st, err := c.Do("POST", "/api/v1/clients", body)
		return exitFromEnvelope(st, env, err, gf)
	case "delete":
		if len(args) < 2 {
			return 1
		}
		env, st, err := c.Do("DELETE", "/api/v1/clients/"+url.PathEscape(args[1]), nil)
		return exitFromEnvelope(st, env, err, gf)
	}
	return 1
}

func runProxy(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 {
		return runSimpleGet(c, "/api/v1/proxy/status", gf)
	}
	switch args[0] {
	case "status":
		return runSimpleGet(c, "/api/v1/proxy/status", gf)
	case "apply":
		env, st, err := c.Do("POST", "/api/v1/proxy/apply", nil)
		return exitFromEnvelope(st, env, err, gf)
	case "config":
		if len(args) < 2 {
			return 1
		}
		switch args[1] {
		case "xray":
			return runSimpleGet(c, "/api/v1/proxy/config/xray", gf)
		case "singbox":
			return runSimpleGet(c, "/api/v1/proxy/config/singbox", gf)
		}
	case "reality-keys":
		env, st, err := c.Do("POST", "/api/v1/proxy/generate-reality-keys", nil)
		return exitFromEnvelope(st, env, err, gf)
	case "test":
		// Server-side probe is designed in docs/cli_api_spec.md §2.5 but
		// not yet implemented; until then run scripts/proto_sweep.sh on
		// the panel host for end-to-end coverage.
		fmt.Fprintln(os.Stderr, "proxy test: server-side prober not implemented yet — use scripts/proto_sweep.sh on the panel host")
		return 1
	}
	return 1
}

func runSub(c *Client, args []string, _ globalFlags) int {
	if len(args) < 2 || args[0] != "url" {
		fmt.Fprintln(os.Stderr, "usage: zenithctl sub url <client-uuid>")
		return 1
	}
	host := c.Profile.Host
	if isUnixHost(host) {
		host = "http://<your-panel-host>"
	}
	fmt.Printf("%s/api/v1/sub/%s\n", strings.TrimRight(host, "/"), args[1])
	return 0
}

func runFirewall(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 || args[0] == "list" {
		return runSimpleGet(c, "/api/v1/firewall/rules", gf)
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("firewall add", flag.ContinueOnError)
		port := fs.String("port", "", "port or range (e.g. 443)")
		proto := fs.String("proto", "tcp", "tcp|udp")
		action := fs.String("action", "ACCEPT", "ACCEPT|DROP|REJECT")
		source := fs.String("source", "", "optional source CIDR")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *port == "" {
			fmt.Fprintln(os.Stderr, "--port required")
			return 1
		}
		body := map[string]any{
			"port": *port, "protocol": *proto, "action": *action, "source": *source,
		}
		env, st, err := c.Do("POST", "/api/v1/firewall/rules", body)
		return exitFromEnvelope(st, env, err, gf)
	case "delete":
		if len(args) < 2 {
			return 1
		}
		env, st, err := c.Do("DELETE", "/api/v1/firewall/rules?line="+url.QueryEscape(args[1]), nil)
		return exitFromEnvelope(st, env, err, gf)
	}
	return 1
}

func runBackup(c *Client, args []string, _ globalFlags) int {
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("backup export", flag.ContinueOnError)
		out := fs.String("out", "backup.zip", "output path")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		env, st, err := c.Do("GET", "/api/v1/admin/backup/export", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 4
		}
		if st != 200 {
			fmt.Fprintf(os.Stderr, "server returned %d: %s\n", st, env.Msg)
			return 2
		}
		// backup export returns binary; the envelope's Data holds the raw bytes
		// because the JSON decode failed.
		if err := os.WriteFile(*out, env.Data, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("wrote", *out)
		return 0
	case "restore":
		fmt.Fprintln(os.Stderr, "restore via CLI not implemented in v1; use the Web UI")
		return 1
	}
	return 1
}

func runRaw(c *Client, args []string, gf globalFlags) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl raw <METHOD> <PATH> [--data @file|-]")
		return 1
	}
	method := strings.ToUpper(args[0])
	path := args[1]
	var body any
	if len(args) >= 4 && args[2] == "--data" {
		if args[3] == "-" {
			b, _ := io.ReadAll(os.Stdin)
			if err := json.Unmarshal(b, &body); err != nil {
				body = string(b)
			}
		} else if strings.HasPrefix(args[3], "@") {
			b, err := os.ReadFile(args[3][1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := json.Unmarshal(b, &body); err != nil {
				body = string(b)
			}
		} else {
			if err := json.Unmarshal([]byte(args[3]), &body); err != nil {
				body = args[3]
			}
		}
	}
	env, st, err := c.Do(method, path, body)
	return exitFromEnvelope(st, env, err, gf)
}

func readJSONFile(path string) (any, error) {
	if path == "" {
		return nil, errors.New("--file required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func printHelp() {
	fmt.Fprintln(os.Stderr, `zenithctl — headless ZenithPanel CLI

Usage:
  zenithctl [global flags] <command> [args]

Global flags:
  --host <url>          unix:///run/zenithpanel.sock | http(s)://host
  --socket <path>       shortcut for --host unix://<path>
  --token <ztk_...>     API token (overrides config)
  --profile <name>      profile from ~/.config/zenithctl/config.toml
  --output json|table   (default json)
  -q, --quiet           print only data
  -h, --help

Common commands:
  status                                  ping panel
  token list|create|revoke|bootstrap
  system info|bbr|swap|cleanup
  inbound list|show|create|update|delete
  client list|add|delete
  proxy status|apply|config xray|test <id|all>|reality-keys
  sub url <uuid>
  firewall list|add|delete
  backup export
  raw <METHOD> <PATH> [--data @f|-]

Run 'zenithctl token bootstrap' on the panel host (as root) to mint a token
and write ~/.config/zenithctl/config.toml in one shot.`)
}
