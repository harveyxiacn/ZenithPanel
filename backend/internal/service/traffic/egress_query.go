package traffic

import (
	"sort"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

// EgressFilter is the common filter set for egress queries. Start/End are unix
// seconds; an empty string filter means "all".
type EgressFilter struct {
	Start, End int64
	Instance   string
	User       string
	Direction  string
	Limit      int
}

// EgressRow is one detail row returned by the list endpoint. DestIP/DestRDNS
// are empty when the query was served from the hourly rollup (which drops the
// IP column and folds the rDNS name into dest_host).
type EgressRow struct {
	Bucket    int64  `json:"bucket"`
	Instance  string `json:"instance"`
	UserEmail string `json:"user_email"`
	DestHost  string `json:"dest_host"`
	DestIP    string `json:"dest_ip"`
	DestRDNS  string `json:"dest_rdns"`
	ASN       string `json:"asn"`
	ASOrg     string `json:"as_org"`
	Country   string `json:"country"`
	Direction string `json:"direction"`
	BytesUp   int64  `json:"bytes_up"`
	BytesDown int64  `json:"bytes_down"`
	Hits      int64  `json:"hits"`
}

// EgressSummaryRow is one aggregated group (by domain/asn/instance/user/...).
// Kind is only set for the "dest" dimension: "domain" (sniffed), "rdns"
// (reverse-DNS / learned, best-effort) or "ip" (unresolved), so the UI can
// mark how trustworthy the displayed name is.
type EgressSummaryRow struct {
	Key        string `json:"key"`
	Kind       string `json:"kind,omitempty"`
	KindRank   int    `json:"-"`
	ASOrg      string `json:"as_org,omitempty"`
	Country    string `json:"country,omitempty"`
	BytesUp    int64  `json:"bytes_up"`
	BytesDown  int64  `json:"bytes_down"`
	BytesTotal int64  `json:"bytes_total"`
	Hits       int64  `json:"hits"`
}

// SeriesPoint is one time-bucketed total for the stacked chart.
type SeriesPoint struct {
	Bucket    int64  `json:"bucket"`
	Instance  string `json:"instance,omitempty"`
	BytesUp   int64  `json:"bytes_up"`
	BytesDown int64  `json:"bytes_down"`
}

// InstanceCoverage describes, per egress instance, what fidelity is ACTUALLY
// present — computed from observed data (and live-discovered programs), never a
// hard-coded list — surfaced as honesty badges in the UI so IP-only instances
// aren't mistaken for missing data.
type InstanceCoverage struct {
	Instance string `json:"instance"`
	Domain   bool   `json:"domain"`
	PerUser  bool   `json:"per_user"`
	Bytes    bool   `json:"bytes"`
	Source   string `json:"source"` // "observed" (from data) | "discovered" (running, no traffic yet)
	Note     string `json:"note"`
}

// tableFor picks the hot 5-min table for recent windows and the hourly rollup
// for windows that reach past the hot-retention horizon.
func (e *EgressCollector) tableFor(start int64) string {
	days := getIntSetting(SettingEgressRetentionDays, defaultHotDays, 1, 365)
	if start < time.Now().AddDate(0, 0, -days).Unix() {
		return "traffic_egress_hourly"
	}
	return "traffic_egress"
}

func (e *EgressCollector) base(f EgressFilter) *gorm.DB {
	q := e.db.Table(e.tableFor(f.Start)).Where("bucket >= ? AND bucket < ?", f.Start, f.End)
	if f.Instance != "" {
		q = q.Where("instance = ?", f.Instance)
	}
	if f.User != "" {
		q = q.Where("user_email = ?", f.User)
	}
	if f.Direction != "" {
		q = q.Where("direction = ?", f.Direction)
	}
	return q
}

// Query returns detail rows, newest bucket first, capped at Limit (default 1000).
func (e *EgressCollector) Query(f EgressFilter) []EgressRow {
	if e == nil || e.db == nil {
		return nil
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	var rows []EgressRow
	e.base(f).Order("bucket desc").Limit(limit).Scan(&rows)
	return rows
}

// Export returns all detail rows matching the filter (no UI row cap), oldest
// bucket first, for CSV download. Hard-bounded to avoid unbounded memory.
func (e *EgressCollector) Export(f EgressFilter) []EgressRow {
	if e == nil || e.db == nil {
		return nil
	}
	var rows []EgressRow
	e.base(f).Order("bucket asc").Limit(200000).Scan(&rows)
	return rows
}

var summaryCols = map[string]string{
	"domain":    "dest_host",
	"host":      "dest_host",
	"asn":       "asn",
	"instance":  "instance",
	"user":      "user_email",
	"country":   "country",
	"direction": "direction",
}

// Summary aggregates bytes by the requested dimension, biggest first (top 500).
// The special "dest" dimension coalesces sniffed-domain → rDNS name → IP so it
// works uniformly across domain-capable and IP-only instances (the hourly
// rollup drops the IP column and pre-folds rDNS into dest_host, so there it
// falls back to dest_host only).
func (e *EgressCollector) Summary(f EgressFilter, groupBy string) []EgressSummaryRow {
	if e == nil || e.db == nil {
		return nil
	}
	var groupExpr string
	destKind := false
	switch {
	case groupBy == "dest":
		if e.tableFor(f.Start) == "traffic_egress_hourly" {
			groupExpr = "dest_host"
		} else {
			groupExpr = "COALESCE(NULLIF(dest_host,''), NULLIF(dest_rdns,''), dest_ip)"
			destKind = true
		}
	default:
		col, ok := summaryCols[groupBy]
		if !ok {
			col = "dest_host"
		}
		groupExpr = col
	}
	sel := groupExpr + " as key, SUM(bytes_up) as bytes_up, SUM(bytes_down) as bytes_down, " +
		"SUM(bytes_up + bytes_down) as bytes_total, SUM(hits) as hits"
	if groupBy == "asn" {
		sel += ", MAX(as_org) as as_org, MAX(country) as country"
	}
	if destKind {
		// A sniffed domain anywhere in the group beats an rDNS guess beats a
		// bare IP — MAX over the rank picks the best evidence seen.
		sel += ", MAX(CASE WHEN dest_host <> '' THEN 2 WHEN dest_rdns <> '' THEN 1 ELSE 0 END) as kind_rank"
	}
	var rows []EgressSummaryRow
	e.base(f).Select(sel).Group(groupExpr).Order("bytes_total desc").Limit(500).Scan(&rows)
	if destKind {
		for i := range rows {
			switch rows[i].KindRank {
			case 2:
				rows[i].Kind = "domain"
			case 1:
				rows[i].Kind = "rdns"
			default:
				rows[i].Kind = "ip"
			}
		}
	}
	return rows
}

// Series returns per-bucket byte totals for the time chart, optionally split by
// instance so the UI can render a stacked area per proxy.
func (e *EgressCollector) Series(f EgressFilter, splitInstance bool) []SeriesPoint {
	if e == nil || e.db == nil {
		return nil
	}
	sel := "bucket, SUM(bytes_up) as bytes_up, SUM(bytes_down) as bytes_down"
	grp := "bucket"
	if splitInstance {
		sel = "bucket, instance, SUM(bytes_up) as bytes_up, SUM(bytes_down) as bytes_down"
		grp = "bucket, instance"
	}
	var pts []SeriesPoint
	e.base(f).Select(sel).Group(grp).Order("bucket asc").Scan(&pts)
	return pts
}

// Coverage reports, per instance, what fidelity is actually present — computed
// from the observed data (which instances have domains, users, bytes) and
// unioned with live-discovered egress programs that haven't moved traffic yet.
// Nothing about the instance set or its capabilities is hard-coded.
func (e *EgressCollector) Coverage() []InstanceCoverage {
	if e == nil || e.db == nil {
		return nil
	}
	type aggRow struct {
		Instance  string
		HasDomain int
		HasUser   int
		HasBytes  int
	}
	var rows []aggRow
	since := time.Now().AddDate(0, 0, -getIntSetting(SettingEgressRetentionDays, defaultHotDays, 1, 365)).Unix()
	e.db.Model(&model.TrafficEgress{}).
		Select("instance, " +
			"MAX(CASE WHEN dest_host <> '' THEN 1 ELSE 0 END) as has_domain, " +
			"MAX(CASE WHEN user_email <> '' THEN 1 ELSE 0 END) as has_user, " +
			"MAX(CASE WHEN bytes_up + bytes_down > 0 THEN 1 ELSE 0 END) as has_bytes").
		Where("bucket >= ?", since).
		Group("instance").Scan(&rows)

	byInst := map[string]InstanceCoverage{}
	for _, r := range rows {
		if r.Instance == "" {
			continue
		}
		byInst[r.Instance] = InstanceCoverage{
			Instance: r.Instance,
			Domain:   r.HasDomain == 1,
			PerUser:  r.HasUser == 1,
			Bytes:    r.HasBytes == 1,
			Source:   "observed",
			Note:     coverageNote(r.HasDomain == 1, r.HasUser == 1, r.HasBytes == 1),
		}
	}
	// Union in egress programs discovered live on the host that haven't been
	// observed in the data yet, so the operator sees them proactively.
	for _, inst := range e.DiscoverInstances() {
		if _, ok := byInst[inst]; ok {
			continue
		}
		byInst[inst] = InstanceCoverage{
			Instance: inst,
			Source:   "discovered",
			Note:     "运行中，尚未观测到出口流量",
		}
	}

	out := make([]InstanceCoverage, 0, len(byInst))
	for _, c := range byInst {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// coverageNote describes an instance's fidelity from the dimensions actually
// present in its data — no per-instance prose, derived purely from the flags.
func coverageNote(domain, user, bytes bool) string {
	switch {
	case domain && user && bytes:
		return "域名 + 用户 + 精确字节"
	case bytes && user:
		return "IP 级字节 + 按用户（未启用域名嗅探）"
	case domain && !bytes:
		return "域名 / 命中（无精确字节）"
	case bytes:
		return "仅 IP 级字节（无用户；域名由 rDNS / 嗅探学习尽力补全）"
	default:
		return "仅目的地记录"
	}
}
