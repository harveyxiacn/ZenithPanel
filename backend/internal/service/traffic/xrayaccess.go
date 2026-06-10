package traffic

import (
	"context"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

// xrayAccessPollInterval is how often the access-log tail checks for new lines.
const xrayAccessPollInterval = 2 * time.Second

var (
	// "accepted tcp:example.com:443" — host may be a domain (sniffing on) or an IP.
	reXrayDest  = regexp.MustCompile(`accepted\s+(?:tcp|udp):(\S+?):\d+`)
	reXrayEmail = regexp.MustCompile(`email:\s*(\S+)`)
)

// runXrayAccess tails a zenith-xray access.log (path from the
// traffic_egress_xray_access_path setting) and records per-(user, domain) hits
// for the "zenith-xray" instance. This is the only tier that yields DOMAINS for
// xray (Clash covers sing-box only); it carries hit counts, not bytes — byte
// volume for zenith-xray comes from the socket sampler (by IP) and per-user
// totals from the existing xray statsquery accountant. Opt-in: empty path = off.
func (e *EgressCollector) runXrayAccess(ctx context.Context) {
	var (
		curPath string
		offset  int64
		buf     string
	)
	ticker := time.NewTicker(xrayAccessPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !getBoolSetting(SettingEgressEnabled, true) {
				continue
			}
			path := strings.TrimSpace(config.GetSetting(SettingEgressXrayAccessPath))
			if path == "" {
				curPath, offset, buf = "", 0, ""
				continue
			}
			if path != curPath {
				// New path: start from END so we don't replay the whole history.
				curPath = path
				buf = ""
				if fi, err := os.Stat(path); err == nil {
					offset = fi.Size()
				} else {
					offset = 0
				}
				continue
			}
			offset, buf = e.tailXray(path, offset, buf)
		}
	}
}

func (e *EgressCollector) tailXray(path string, offset int64, buf string) (int64, string) {
	fi, err := os.Stat(path)
	if err != nil {
		return offset, buf
	}
	if fi.Size() < offset {
		// Rotated or truncated — restart from the top of the new file.
		offset, buf = 0, ""
	}
	if fi.Size() == offset {
		return offset, buf
	}
	f, err := os.Open(path)
	if err != nil {
		return offset, buf
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, buf
	}
	// Bound a single read so a huge backlog can't blow memory; the rest is
	// picked up on the next tick.
	chunk, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	if len(chunk) == 0 {
		return offset, buf
	}
	offset += int64(len(chunk))
	data := buf + string(chunk)
	lines := strings.Split(data, "\n")
	// Last element is an incomplete line; carry it to the next read.
	buf = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		e.parseXrayLine(line)
	}
	return offset, buf
}

func (e *EgressCollector) parseXrayLine(line string) {
	m := reXrayDest.FindStringSubmatch(line)
	if m == nil {
		return
	}
	dest := m[1]
	email := ""
	if em := reXrayEmail.FindStringSubmatch(line); em != nil {
		email = em[1]
	}
	// dest is a domain unless it parses as a literal IP (sniffing off).
	host, ip := dest, ""
	if net.ParseIP(dest) != nil {
		host, ip = "", dest
	}
	if isPrivateIP(ip) && ip != "" {
		return
	}
	// Labeled "xray" to match the xray process comm, so the access-log domain
	// rows merge with the socket sampler's byte rows for the same instance.
	e.Add("xray", email, host, ip, "egress", 0, 0, 1)
}
