package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// PrintAsTable inspects the envelope's `data` field and renders it as a
// human-readable table for the well-known shapes (inbound list, client list,
// token list, status). If the data shape isn't recognized, falls back to
// pretty-printed JSON so the user still sees something useful.
//
// The table renderer is intentionally minimal: tab-aligned columns, no
// borders, no color. Operators piping the output into grep/awk get a stable
// layout; humans get a layout that fits in a terminal at 120 columns.
func PrintAsTable(env *Envelope) {
	if env == nil || len(env.Data) == 0 {
		// Some endpoints (e.g. `proxy apply`) return just msg with no data.
		if env != nil && env.Msg != "" {
			fmt.Println(env.Msg)
		}
		return
	}

	// Try each known shape in order. Probe-all results overlap with inbound
	// rows on id/tag/protocol; they're tried first so the dedicated probe
	// renderer wins. Token rows have unique fields (scopes/revoked) so
	// ordering after probe is safe. Inbound and client rows are mutually
	// exclusive on inbound_id.
	if rendered := tryProbeAllArray(env.Data); rendered {
		return
	}
	if rendered := tryTokenList(env.Data); rendered {
		return
	}
	if rendered := tryClientList(env.Data); rendered {
		return
	}
	if rendered := tryInboundList(env.Data); rendered {
		return
	}
	if rendered := tryProxyStatus(env.Data); rendered {
		return
	}
	// Unknown shape — pretty-print the JSON.
	fmt.Println(Pretty(env))
}

// IsTTY reports whether stdout looks like a real terminal. Used to flip the
// default output format from json to table when a human is watching.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// tw wraps fmt.Fprintln so the body of each renderer reads like printf.
type tw struct{ *tabwriter.Writer }

func newTabWriter() tw {
	return tw{tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)}
}

func (w tw) row(cells ...any) {
	parts := make([]string, 0, len(cells))
	for _, c := range cells {
		parts = append(parts, fmt.Sprint(c))
	}
	_, _ = io.WriteString(w, strings.Join(parts, "\t")+"\n")
}

// --- renderers per data shape --------------------------------------------

func tryInboundList(raw json.RawMessage) bool {
	var rows []struct {
		ID            int    `json:"id"`
		Tag           string `json:"tag"`
		Protocol      string `json:"protocol"`
		Port          int    `json:"port"`
		Network       string `json:"network"`
		ServerAddress string `json:"server_address"`
		Enable        bool   `json:"enable"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return false
	}
	// Heuristic: must have at least one row with both `tag` and `protocol`
	// to count as an inbound list — protects against unrelated `[]obj` shapes.
	if rows[0].Tag == "" && rows[0].Protocol == "" {
		return false
	}
	w := newTabWriter()
	w.row("ID", "TAG", "PROTOCOL", "PORT", "NETWORK", "ADDRESS", "ENABLED")
	for _, r := range rows {
		w.row(r.ID, dash(r.Tag), dash(r.Protocol), r.Port, dash(r.Network), dash(r.ServerAddress), boolMark(r.Enable))
	}
	_ = w.Flush()
	return true
}

func tryClientList(raw json.RawMessage) bool {
	var rows []struct {
		ID        int    `json:"id"`
		InboundID int    `json:"inbound_id"`
		Email     string `json:"email"`
		UUID      string `json:"uuid"`
		Enable    bool   `json:"enable"`
		UpLoad    int64  `json:"up_load"`
		DownLoad  int64  `json:"down_load"`
		Total     int64  `json:"total"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return false
	}
	// Clients always carry inbound_id; that's our discriminator vs the
	// inbound shape which doesn't have it.
	if rows[0].InboundID == 0 && rows[0].Email == "" {
		return false
	}
	w := newTabWriter()
	w.row("ID", "INBOUND", "EMAIL", "UUID", "ENABLED", "UP", "DOWN", "LIMIT")
	for _, r := range rows {
		w.row(r.ID, r.InboundID, dash(r.Email), short(r.UUID, 12), boolMark(r.Enable), humanBytes(r.UpLoad), humanBytes(r.DownLoad), humanBytes(r.Total))
	}
	_ = w.Flush()
	return true
}

func tryTokenList(raw json.RawMessage) bool {
	var rows []struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		Scopes     string `json:"scopes"`
		ExpiresAt  int64  `json:"expires_at"`
		LastUsedAt int64  `json:"last_used_at"`
		Revoked    bool   `json:"revoked"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return false
	}
	if rows[0].Name == "" && rows[0].Scopes == "" {
		return false
	}
	w := newTabWriter()
	w.row("ID", "NAME", "SCOPES", "EXPIRES", "LAST USED", "STATUS")
	for _, r := range rows {
		status := "active"
		if r.Revoked {
			status = "revoked"
		}
		w.row(r.ID, dash(r.Name), dash(r.Scopes), unixHuman(r.ExpiresAt), unixHuman(r.LastUsedAt), status)
	}
	_ = w.Flush()
	return true
}

func tryProxyStatus(raw json.RawMessage) bool {
	var s struct {
		XrayRunning          bool     `json:"xray_running"`
		SingboxRunning       bool     `json:"singbox_running"`
		DualMode             bool     `json:"dual_mode"`
		EnabledInbounds      int      `json:"enabled_inbounds"`
		EnabledClients       int      `json:"enabled_clients"`
		EnabledRules         int      `json:"enabled_rules"`
		XraySkippedProtocols []string `json:"xray_skipped_protocols"`
		XrayHandedOffSingbox []string `json:"xray_handed_off_to_singbox"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	// Must have at least one of these fields to count as a proxy status.
	if !s.XrayRunning && !s.SingboxRunning && s.EnabledInbounds == 0 && s.EnabledClients == 0 {
		return false
	}
	w := newTabWriter()
	w.row("FIELD", "VALUE")
	w.row("xray_running", boolMark(s.XrayRunning))
	w.row("singbox_running", boolMark(s.SingboxRunning))
	w.row("dual_mode", boolMark(s.DualMode))
	w.row("enabled_inbounds", s.EnabledInbounds)
	w.row("enabled_clients", s.EnabledClients)
	w.row("enabled_rules", s.EnabledRules)
	if len(s.XraySkippedProtocols) > 0 {
		w.row("xray_skipped", strings.Join(s.XraySkippedProtocols, ", "))
	}
	if len(s.XrayHandedOffSingbox) > 0 {
		w.row("handed_off_to_singbox", strings.Join(s.XrayHandedOffSingbox, ", "))
	}
	_ = w.Flush()
	return true
}

func tryProbeAllArray(raw json.RawMessage) bool {
	var rows []struct {
		ID        int    `json:"id"`
		Tag       string `json:"tag"`
		Protocol  string `json:"protocol"`
		Transport string `json:"transport"`
		OK        bool   `json:"ok"`
		Stage     string `json:"stage"`
		ElapsedMs int64  `json:"elapsed_ms"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return false
	}
	// Probe-row discriminator: at least one of `transport`, `stage`, or
	// `elapsed_ms` set (inbound rows never have those). `ok` alone is too
	// weak — JSON unmarshal would leave it false either way.
	hasProbeShape := false
	for _, r := range rows {
		if r.Transport != "" || r.Stage != "" || r.ElapsedMs > 0 {
			hasProbeShape = true
			break
		}
	}
	if !hasProbeShape {
		return false
	}
	// Sort by id for stable output.
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	w := newTabWriter()
	w.row("ID", "TAG", "PROTOCOL", "TRANSPORT", "OK", "STAGE", "ELAPSED")
	for _, r := range rows {
		mark := "✓"
		if !r.OK {
			mark = "✗"
		}
		w.row(r.ID, dash(r.Tag), dash(r.Protocol), dash(r.Transport), mark, dash(r.Stage), fmt.Sprintf("%dms", r.ElapsedMs))
	}
	_ = w.Flush()
	return true
}

// --- formatters ----------------------------------------------------------

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func boolMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

func unixHuman(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return timeFormat(ts)
}
