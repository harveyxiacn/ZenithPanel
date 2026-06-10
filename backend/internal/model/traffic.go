package model

// TrafficEgress is a 5-minute-bucketed aggregate of bytes moved between one
// proxy instance's user and one destination, in one direction. It is the
// "hot" table the traffic collector writes on every accountant flush (30s) via
// an UPSERT that increments the byte counters for the current bucket, so the
// table holds at most one row per (bucket, instance, user, dest, direction).
//
// Field coverage varies by source instance and is surfaced honestly in the UI:
//   - zenith-singbox: domain + user + exact bytes (Clash API)
//   - zenith-xray:    user + bytes (xray statsquery); domain only if the
//     opt-in access-log tier is enabled
//   - 3x-ui/wireproxy/cpa: IP-only, no per-user (socket sampler)
//   - sub2api:        upstream host + user (opt-in Postgres scraper)
//
// Direction is "egress" (proxy -> upstream destination) or "return" (proxy ->
// client machine), so the panel can separate outbound from return-to-user.
type TrafficEgress struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Bucket    int64  `gorm:"not null;uniqueIndex:idx_te_uniq,priority:1;index:idx_te_bucket" json:"bucket"` // unix secs floored to 300
	Instance  string `gorm:"size:32;not null;uniqueIndex:idx_te_uniq,priority:2;index:idx_te_instance" json:"instance"`
	UserEmail string `gorm:"size:128;default:'';uniqueIndex:idx_te_uniq,priority:3;index:idx_te_user" json:"user_email"`
	DestHost  string `gorm:"size:255;default:'';uniqueIndex:idx_te_uniq,priority:4;index:idx_te_host" json:"dest_host"`
	DestIP    string `gorm:"size:45;default:'';uniqueIndex:idx_te_uniq,priority:5" json:"dest_ip"`
	Direction string `gorm:"size:8;not null;uniqueIndex:idx_te_uniq,priority:6;index:idx_te_dir" json:"direction"`
	// DestRDNS is a best-effort display name for IP-only rows (DestHost == ""):
	// a domain learned from the Clash tier's sniffed (host, IP) pairs, or a PTR
	// lookup. Derived metadata like ASN — backfilled async, NOT part of the
	// unique key, and never counted as real domain coverage.
	DestRDNS  string `gorm:"column:dest_rdns;size:255;default:''" json:"dest_rdns"`
	ASN       string `gorm:"size:16;default:'';index:idx_te_asn" json:"asn"`
	ASOrg     string `gorm:"size:128;default:''" json:"as_org"`
	Country   string `gorm:"size:2;default:''" json:"country"`
	BytesUp   int64  `gorm:"default:0" json:"bytes_up"`   // bytes the box SENT toward DestHost/IP (or, for return, toward the client)
	BytesDown int64  `gorm:"default:0" json:"bytes_down"` // bytes the box RECEIVED from that peer
	Hits      int64  `gorm:"default:0" json:"hits"`       // number of flush observations folded into this row
}

// TableName pins the table name so renames of the Go type don't migrate data.
func (TrafficEgress) TableName() string { return "traffic_egress" }

// TrafficEgressHourly is the 1-hour rollup of TrafficEgress, kept far longer
// than the hot table (default 90 days vs 7). DestIP is dropped so cardinality
// collapses to per-(host, ASN); rows are produced by a daily rollup job that
// sums hot rows older than the hot-retention window before they are pruned.
type TrafficEgressHourly struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Bucket    int64  `gorm:"not null;uniqueIndex:idx_teh_uniq,priority:1;index:idx_teh_bucket" json:"bucket"` // unix secs floored to 3600
	Instance  string `gorm:"size:32;not null;uniqueIndex:idx_teh_uniq,priority:2;index:idx_teh_instance" json:"instance"`
	UserEmail string `gorm:"size:128;default:'';uniqueIndex:idx_teh_uniq,priority:3" json:"user_email"`
	DestHost  string `gorm:"size:255;default:'';uniqueIndex:idx_teh_uniq,priority:4;index:idx_teh_host" json:"dest_host"`
	ASN       string `gorm:"size:16;default:'';uniqueIndex:idx_teh_uniq,priority:5;index:idx_teh_asn" json:"asn"`
	Direction string `gorm:"size:8;not null;uniqueIndex:idx_teh_uniq,priority:6" json:"direction"`
	ASOrg     string `gorm:"size:128;default:''" json:"as_org"`
	Country   string `gorm:"size:2;default:''" json:"country"`
	BytesUp   int64  `gorm:"default:0" json:"bytes_up"`
	BytesDown int64  `gorm:"default:0" json:"bytes_down"`
	Hits      int64  `gorm:"default:0" json:"hits"`
}

func (TrafficEgressHourly) TableName() string { return "traffic_egress_hourly" }
