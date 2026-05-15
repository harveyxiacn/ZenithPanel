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
	case "cert":
		return runCert(client, args, gf)
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
	// Default output: table when stdout is a terminal (a human is watching),
	// json otherwise (a pipe, file, or CI is collecting structured data).
	// Explicit --output beats the auto-detection in either direction.
	defaultOut := "json"
	if IsTTY() {
		defaultOut = "table"
	}
	g := globalFlags{output: defaultOut}
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
	switch gf.output {
	case "table":
		PrintAsTable(env)
	default:
		fmt.Println(Pretty(env))
	}
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
	case "rotate":
		return runTokenRotate(c, args, gf)
	default:
		fmt.Fprintln(os.Stderr, "unknown token subcommand:", args[0])
		return 1
	}
}

// runTokenRotate revokes the named token and mints a fresh one inheriting
// the old scopes. The new token gets a versioned name (`name-v2` etc.) so
// the unique-name constraint is respected and the audit log keeps the old
// row visibly revoked. Idempotent: rotating a name that doesn't exist yet
// just creates it fresh.
//
// Self-rotation safety: if the rotated token IS the one stored in the
// active profile, we update the profile in-memory and persist it before
// revoking. Otherwise the very next CLI call would 401 because the caller
// is holding a revoked token.
func runTokenRotate(c *Client, args []string, gf globalFlags) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl token rotate <name>")
		return 1
	}
	target := args[1]

	// List tokens to find the latest live (non-revoked) row whose base name
	// matches `target`. Versioned siblings (target, target-v2, target-v3 …)
	// are treated as the same logical token.
	env, _, err := c.Do("GET", "/api/v1/admin/api-tokens", nil)
	if err != nil || env == nil {
		fmt.Fprintln(os.Stderr, "list tokens:", err)
		return 1
	}
	var rows []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Scopes  string `json:"scopes"`
		Revoked bool   `json:"revoked"`
	}
	_ = json.Unmarshal(env.Data, &rows)

	// Pick the live row (if any) that shares the base name. Track the
	// highest version suffix seen so the new name doesn't collide.
	var current struct {
		ID     int
		Scopes string
		Found  bool
	}
	highest := 1
	for _, r := range rows {
		base, ver := splitVersionedName(r.Name)
		if base != target {
			continue
		}
		if ver > highest {
			highest = ver
		}
		if !r.Revoked {
			current.ID = r.ID
			current.Scopes = r.Scopes
			current.Found = true
		}
	}

	scopes := current.Scopes
	if scopes == "" {
		scopes = "*"
	}
	newName := fmt.Sprintf("%s-v%d", target, highest+1)
	// First mint, then revoke — if mint fails we still hold the old token.
	createRes, st, err := c.Do("POST", "/api/v1/admin/api-tokens", map[string]any{
		"name": newName, "scopes": scopes,
	})
	if err != nil || st != 200 {
		return exitFromEnvelope(st, createRes, err, gf)
	}

	// Parse the new plaintext out so we can swap the active profile before
	// revoking the old one (otherwise we'd revoke the token we're using and
	// the revoke call itself would 401 mid-flight).
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(createRes.Data, &minted)
	if minted.Token != "" {
		// If the active profile is using the about-to-be-revoked token,
		// rewrite ~/.config/zenithctl/config.toml in place so subsequent
		// calls (including the revoke we're about to issue) authenticate
		// with the new credential.
		if cfg, cerr := LoadConfig(); cerr == nil {
			swapped := false
			for name, p := range cfg.Profile {
				if p.Token != "" && p.Token == c.Profile.Token {
					p.Token = minted.Token
					cfg.Profile[name] = p
					swapped = true
				}
			}
			if swapped {
				_ = SaveConfig(cfg)
				// Update the in-process client so the revoke call below uses
				// the new credential.
				c.Profile.Token = minted.Token
			}
		}
	}

	if current.Found {
		revRes, revSt, err := c.Do("DELETE", fmt.Sprintf("/api/v1/admin/api-tokens/%d", current.ID), nil)
		if err != nil || revSt != 200 {
			fmt.Fprintln(os.Stderr, "WARNING: minted new token but failed to revoke old one (id="+strconv.Itoa(current.ID)+")")
			return exitFromEnvelope(revSt, revRes, err, gf)
		}
	}

	// Reuse the standard envelope path so --output flag still applies.
	return exitFromEnvelope(200, createRes, nil, gf)
}

// splitVersionedName splits "ci-runner-v3" → ("ci-runner", 3). A name without
// a `-v<N>` suffix returns (name, 1) so the rotation logic can treat the
// very first token as version 1.
func splitVersionedName(name string) (base string, version int) {
	idx := strings.LastIndex(name, "-v")
	if idx < 0 {
		return name, 1
	}
	suffix := name[idx+2:]
	if suffix == "" {
		return name, 1
	}
	v, err := strconv.Atoi(suffix)
	if err != nil || v < 1 {
		return name, 1
	}
	return name[:idx], v
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
	case "set-port":
		return runInboundSetPort(c, args[1:], gf)
	}
	return 1
}

// runInboundSetPort changes the listening port of one inbound and optionally
// reconciles the host firewall so the new port is open and the old port no
// longer hangs around. The flow is deliberately three small API calls instead
// of a single PATCH so it stays auditable and matches what an operator would
// do by hand:
//
//  1. GET the inbound (we need every field PUT-back-friendly)
//  2. PUT the inbound with the new port
//  3. (optional) UFW open new, close old via the existing firewall routes
//
// Use --sync-firewall to do step 3.
func runInboundSetPort(c *Client, args []string, gf globalFlags) int {
	fs := flag.NewFlagSet("inbound set-port", flag.ContinueOnError)
	syncFw := fs.Bool("sync-firewall", false, "also open the new port + close the old port in UFW")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl inbound set-port <id> <port> [--sync-firewall]")
		return 1
	}
	idNum, err := strconv.Atoi(rest[0])
	if err != nil || idNum <= 0 {
		fmt.Fprintln(os.Stderr, "id must be a positive integer")
		return 1
	}
	newPort, err := strconv.Atoi(rest[1])
	if err != nil || newPort <= 0 || newPort > 65535 {
		fmt.Fprintln(os.Stderr, "port must be an integer in [1, 65535]")
		return 1
	}

	// No GET-by-id endpoint exists; fetch the full list and filter. Decode
	// into a typed slice so we can compare IDs as integers and read port /
	// protocol without going through interface{} casts.
	listEnv, _, err := c.Do("GET", "/api/v1/inbounds", nil)
	if err != nil || listEnv == nil {
		fmt.Fprintln(os.Stderr, "list inbounds:", err)
		return 1
	}
	type inboundLite struct {
		ID       int    `json:"id"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	}
	var rowsLite []inboundLite
	if err := json.Unmarshal(listEnv.Data, &rowsLite); err != nil {
		fmt.Fprintln(os.Stderr, "decode inbounds:", err)
		return 1
	}
	var oldPort int
	var proto string
	found := false
	for _, r := range rowsLite {
		if r.ID == idNum {
			oldPort, proto, found = r.Port, r.Protocol, true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "no inbound with id %d\n", idNum)
		return 2
	}
	// Re-decode the same payload into a generic map for the PUT — the server
	// expects every field round-tripped, not just the few we typed above.
	var rows []map[string]any
	_ = json.Unmarshal(listEnv.Data, &rows)
	var target map[string]any
	for _, r := range rows {
		if fmt.Sprintf("%v", r["id"]) == rest[0] {
			target = r
			break
		}
	}
	target["port"] = newPort
	id := rest[0]

	// PUT the full row back. The handler enforces the port-uniqueness check,
	// so a collision returns 4xx with a clear message.
	putEnv, st, err := c.Do("PUT", "/api/v1/inbounds/"+url.PathEscape(id), target)
	if err != nil || st != 200 {
		return exitFromEnvelope(st, putEnv, err, gf)
	}
	fmt.Fprintf(os.Stderr, "inbound %s: port %d -> %d (apply with `zenithctl proxy apply` to take effect)\n",
		id, oldPort, newPort)

	if *syncFw {
		fwProto := "tcp"
		// QUIC-style protocols listen on UDP — match what the panel actually binds.
		if proto == "hysteria2" || proto == "tuic" {
			fwProto = "udp"
		}
		openEnv, openSt, err := c.Do("POST", "/api/v1/firewall/rules", map[string]any{
			"port": strconv.Itoa(newPort), "protocol": fwProto, "action": "ACCEPT",
		})
		if err != nil || openSt != 200 {
			fmt.Fprintln(os.Stderr, "WARNING: failed to open new firewall port:", openEnv.Msg)
		} else {
			fmt.Fprintf(os.Stderr, "firewall: opened %d/%s\n", newPort, fwProto)
		}
		// Closing the old port via line number is fragile from a script (line
		// numbers shift as rules are added/removed). We surface the
		// suggestion as a hint instead of doing it automatically.
		fmt.Fprintf(os.Stderr, "(old port %d/%s left in firewall; remove it manually if no other inbound uses it: `ufw delete allow %d/%s`)\n",
			oldPort, fwProto, oldPort, fwProto)
	}

	// Re-apply so the engines pick up the new port without a manual call.
	applyEnv, applySt, applyErr := c.Do("POST", "/api/v1/proxy/apply", nil)
	if applyErr != nil || applySt != 200 {
		fmt.Fprintln(os.Stderr, "WARNING: port updated but proxy apply failed:", applyEnv.Msg)
	}
	return exitFromEnvelope(200, putEnv, nil, gf)
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
			"inbound_id":  *inbound,
			"email":       *email,
			"total":       *total,
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
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zenithctl proxy test <inbound-id|all>")
			return 1
		}
		if args[1] == "all" {
			return runProxyTestAll(c, gf)
		}
		return runSimpleGet(c, "/api/v1/proxy/test/"+url.PathEscape(args[1]), gf)
	}
	return 1
}

// runProxyTestAll iterates every enabled inbound and asks the server-side
// prober to probe each one. Honors --output json|table; exit code is
// non-zero if any probe fails. See ProbeInbound in service/diagnostic.
func runProxyTestAll(c *Client, gf globalFlags) int {
	env, _, err := c.Do("GET", "/api/v1/inbounds", nil)
	if err != nil || env == nil {
		fmt.Fprintln(os.Stderr, "could not list inbounds:", err)
		return 1
	}
	var inbounds []struct {
		ID       int    `json:"id"`
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Enable   bool   `json:"enable"`
	}
	_ = json.Unmarshal(env.Data, &inbounds)
	type row struct {
		ID        int    `json:"id"`
		Tag       string `json:"tag"`
		Protocol  string `json:"protocol"`
		Transport string `json:"transport,omitempty"`
		OK        bool   `json:"ok"`
		Stage     string `json:"stage,omitempty"`
		ElapsedMs int64  `json:"elapsed_ms,omitempty"`
		Err       string `json:"err,omitempty"`
	}
	bad := 0
	results := make([]row, 0, len(inbounds))
	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}
		probeEnv, _, err := c.Do("GET", "/api/v1/proxy/test/"+strconv.Itoa(ib.ID), nil)
		r := row{ID: ib.ID, Tag: ib.Tag, Protocol: ib.Protocol}
		if err != nil || probeEnv == nil || probeEnv.Code != 200 {
			r.OK = false
			r.Stage = "request"
			results = append(results, r)
			bad++
			continue
		}
		var probe struct {
			Transport string `json:"transport"`
			OK        bool   `json:"ok"`
			Stage     string `json:"stage"`
			ElapsedMs int64  `json:"elapsed_ms"`
			Err       string `json:"err"`
		}
		_ = json.Unmarshal(probeEnv.Data, &probe)
		r.OK = probe.OK
		r.Stage = probe.Stage
		r.Transport = probe.Transport
		r.ElapsedMs = probe.ElapsedMs
		r.Err = probe.Err
		if !probe.OK {
			bad++
		}
		results = append(results, r)
	}
	// Wrap in an envelope so the global --output / -q handling kicks in.
	raw, _ := json.Marshal(results)
	env = &Envelope{Code: 200, Msg: "ok", Data: raw}
	if gf.quiet {
		fmt.Println(string(raw))
	} else if gf.output == "table" {
		PrintAsTable(env)
	} else {
		fmt.Println(Pretty(env))
	}
	if bad > 0 {
		return 2
	}
	return 0
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

func runBackup(c *Client, args []string, gf globalFlags) int {
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
		fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		file := fs.String("file", "", "path to backup .zip file")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *file == "" {
			fmt.Fprintln(os.Stderr, "backup restore: --file is required")
			return 1
		}
		// Read the whole zip — the server caps at 16 MB and rejects empty
		// uploads, so handing it raw bytes with a content-type-zip header
		// matches the existing UI flow.
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read backup file:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Uploading %d-byte backup… this replaces inbounds, clients, routing rules, and cron jobs.\n", len(data))
		env, st, err := c.DoRaw("POST", "/api/v1/admin/backup/restore", "application/zip", data)
		return exitFromEnvelope(st, env, err, gf)
	}
	return 1
}

// runCert exposes the ACME flow from the command line. `zenithctl cert issue
// --domain x.com --email me@y.com` is the headless equivalent of the Web UI
// button: it runs the HTTP-01 challenge (lego occupies port 80 during the
// handshake) and on success persists the cert + key under
// /opt/zenithpanel/data/certs/<domain>.{crt,key}. Renewal is automatic from
// then on via the background renewer in service/cert.
func runCert(c *Client, args []string, gf globalFlags) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: zenithctl cert issue --domain <name> --email <addr>")
		return 1
	}
	switch args[0] {
	case "issue":
		fs := flag.NewFlagSet("cert issue", flag.ContinueOnError)
		domain := fs.String("domain", "", "fully-qualified domain name resolving to this VPS")
		email := fs.String("email", "", "ACME account email (for renewal notices and recovery)")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if *domain == "" || *email == "" {
			fmt.Fprintln(os.Stderr, "cert issue: --domain and --email are required")
			return 1
		}
		fmt.Fprintf(os.Stderr, "Issuing certificate for %s (lego will bind :80 briefly for HTTP-01 challenge)…\n", *domain)
		env, st, err := c.Do("POST", "/api/v1/proxy/tls/issue", map[string]any{
			"domain": *domain, "email": *email,
		})
		return exitFromEnvelope(st, env, err, gf)
	default:
		fmt.Fprintln(os.Stderr, "unknown cert subcommand:", args[0])
		return 1
	}
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
  inbound list|show|create|update|delete|set-port
  client list|add|delete
  proxy status|apply|config xray|test <id|all>|reality-keys
  sub url <uuid>
  firewall list|add|delete
  backup export|restore --file <zip>
  cert issue --domain <name> --email <addr>
  raw <METHOD> <PATH> [--data @f|-]

Run 'zenithctl token bootstrap' on the panel host (as root) to mint a token
and write ~/.config/zenithctl/config.toml in one shot.`)
}
