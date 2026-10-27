# DMIT ZenithPanel reconnect / timeout incident report

- Inspection window: 2026-08-25 11:25–11:37 SGT (03:25–03:37 UTC)
- Target: DMIT VPS `64.186.238.234` (`DMIT-LPZqoMyy17`)
- Access: SSH public-key authentication; read-only diagnostics
- Panel build: `v1.0.0-3-g7f5a49b`
- Scope: host resources, Docker lifecycle, ZenithPanel/Xray, SQLite, firewall and network counters

## Executive conclusion

The reconnects and timeouts are real. The primary confirmed cause is host-wide memory exhaustion: at 2026-08-25 11:22:56 SGT the Linux OOM killer terminated `zenithpanel`, Docker recorded exit code 137 and restarted the container about one second later. Because Xray runs inside the same container, this restart forcibly dropped all proxy sessions.

The service degraded before the hard restart. In the minutes leading up to the OOM, the kernel repeatedly reported memory pressure and ZenithPanel logged ordinary SQLite reads taking 60–241 seconds. At the OOM point, the 2 GiB VPS had only about 61 MiB free RAM and 156 KiB free swap. The `zenithpanel` process alone occupied about 1.11 GiB resident memory plus about 928 MiB of swapped pages.

The most likely amplification path is the default-enabled destination-level egress collector combined with a single-connection SQLite pool and an unexpectedly active `DELETE` journal mode. The 58 MiB database is dominated by egress rows and indexes; it currently contains 80,514 hot egress rows for the last seven days. A 30-second flush performs one GORM upsert per pending destination inside a transaction, while all reads share the same single database connection. This explains the observed queueing and slow SQL, but a Go heap profile was not available, so the exact retained allocation responsible for the long-term memory growth is not yet proven.

The network path is a secondary contributor, not the main outage cause. One of the first 25 client-to-VPS pings was lost (4%), but a follow-up 50-ping run had no loss; combined, that is 1/75 (1.33%). During a 30-second server-side interval there were only three TCP retransmissions and zero new TCP timeouts, SYN retransmits, UDP receive errors or IP output discards. The VPS NIC has zero RX/TX errors and drops, BBR+fq is enabled, conntrack usage is low, and VPS-to-1.1.1.1 showed 0% loss at 1.2 ms average. This points to intermittent long-haul/client-route jitter rather than a continuously faulty DMIT NIC or firewall.

## Confirmed findings

### 1. OOM killed the panel and forced a reconnect

Docker events:

```text
03:22:57 UTC  oom
03:22:57 UTC  die exit=137
03:22:58 UTC  start
```

Kernel evidence:

```text
Out of memory: Killed process 1186 (zenithpanel)
anon-rss: 1158796 kB
swapents: 237640 pages (~928 MiB)
Free swap: 156 kB of 1 GiB
```

The host itself did not reboot; uptime was 26 days. The container restart count is 1, directly matching this incident.

### 2. Severe database stalls preceded the OOM

Representative log latencies observed immediately before the kill:

```text
SELECT clients ...                    66.4 s
SELECT setting xray_stats_port        60.9 s
SELECT setting traffic_egress...      44.0 s
SELECT inbounds ...                  238.4 s
SELECT setting xray_stats_port       241.2 s
```

The retained Docker log also contains at least 380 timed SQL records, 315 over one second, 30 over ten seconds and 24 over one minute. The worst retained record was about 2,201.9 seconds. The parser reached a malformed/truncated historical JSON-log entry, so these are lower bounds rather than complete lifetime totals.

### 3. Egress observability is active by default and dominates SQLite

All `traffic_egress_*` settings are absent/empty. In this build, empty means the collector, socket sampler, ASN lookup and reverse DNS are enabled by default.

Database state:

- Integrity check: `ok`
- File size: 58 MiB
- Hot egress rows: 80,514
- Hot range: 2026-08-18 to 2026-08-25 UTC
- Distinct destination IPs: 4,513
- `traffic_egress` data: 8.95 MiB
- `traffic_egress_hourly` data: 11.95 MiB
- Egress indexes consume most of the remaining database pages

Relevant implementation details:

- SQLite pool is restricted to one connection: `backend/internal/config/db.go:160`.
- Each 30-second egress flush loops over every pending destination and upserts it inside one transaction: `backend/internal/service/traffic/egress.go:170-172`.
- HTTP responses may stay alive for up to five minutes: `backend/main.go:436`, allowing handlers blocked behind SQLite to accumulate during a long write stall.

### 4. Runtime journal mode does not match the source's WAL policy

The running database reports:

```text
journal_mode = delete
locking_mode = normal
```

There is no `zenith.db-wal` sidecar. The checked-in build explicitly executes `PRAGMA journal_mode=WAL` at startup and has a regression test for it. The production mismatch should be treated as a separate defect because `DELETE` mode increases read/write blocking and contradicts the intended concurrency policy.

### 5. Limited host headroom and co-located services amplify the incident

The VPS has 1.9 GiB RAM and 1 GiB swap. It also runs PostgreSQL/sub2api, x-ui/Xray, OpenResty, Redis, wireproxy and other services. At the OOM event, many PostgreSQL backends were resident, but `zenithpanel` was by far the largest process and was correctly selected by the kernel as the OOM victim.

The container has no memory limit. This lets a panel leak consume all host RAM and swap, degrading unrelated services before the global OOM killer intervenes.

### 6. Current service state is healthy after the automatic restart

At the end of inspection:

- Health API: `status=ok`, `db=ok`, `proxy=running`
- Xray: running
- Sing-box: stopped, expected because no enabled Sing-box protocol exists
- Enabled inbounds: 1 (`vless-reality`, TCP+Reality, port 52888)
- Enabled clients: 3
- Inbound probe: pass
- Active VLESS TCP sessions observed: 42
- Container memory: about 95 MiB; panel RSS about 48 MiB
- 66-second memory sample: 89.9–97.4 MiB, no immediate monotonic leak

The stable short sample means the problem accumulates over time or is triggered by a workload burst; it does not negate the confirmed OOM.

## Network assessment

Healthy indicators:

- Primary NIC: 0 RX errors, 0 RX drops, 0 TX errors, 0 TX drops across about 599 million received packets.
- BBR congestion control with `fq` qdisc is active.
- Conntrack: 514 / 65,536.
- Firewall permits the panel and VLESS ports; no listen overflow was observed during the sample.
- Thirty-second delta: 3 retransmitted TCP segments, no TCP timeouts, no SYN retransmissions, no UDP receive-buffer errors, no IP output discards.
- VPS to 1.1.1.1: 25/25 replies, 1.214 ms average.

Degraded/path-jitter indicators:

- Current workstation to VPS: first run 1/25 loss, second run 0/50 loss; average RTT about 159–160 ms.
- Live VLESS sockets showed RTTs roughly 150–213 ms, out-of-order delivery on several sessions and occasional retransmission.
- Since the last host boot, the kernel has accumulated about 9.15 million TCP retransmitted segments and 6.08 million TCP timeouts across all services. These lifetime counters are not protocol-specific and cannot establish a current loss rate, but they justify ongoing route monitoring.

## Prioritized recommendations

1. **Immediate containment:** disable destination-level egress logging and its socket sampler on this low-memory production VPS, then observe RSS/swap for 24–72 hours. Preserve aggregate client byte accounting if needed; it is much cheaper than per-destination enrichment.
2. **Fix SQLite runtime mode:** determine why production remains in `DELETE` mode despite the startup WAL pragma. Add startup logging that records the effective journal mode and fail or alert when it is not `wal`.
3. **Remove the single-connection read bottleneck:** retain a serialized writer, but serve reads through a separate WAL read pool. Batch egress upserts rather than issuing one GORM statement per destination.
4. **Bound memory and wait queues:** add Go heap/RSS/goroutine metrics, a sensible `GOMEMLIMIT`, short database/query contexts, and lower HTTP handler timeouts for polling endpoints. A container memory ceiling protects the host, but should be paired with leak remediation because a ceiling alone merely converts host OOM into earlier container restarts.
5. **Increase headroom:** upgrade this multi-service VPS to at least 4 GiB RAM or move PostgreSQL/sub2api/x-ui workloads elsewhere. Increasing swap to 2 GiB may soften bursts but is not a fix for retained-memory growth.
6. **Control PostgreSQL fan-out:** inspect sub2api connection pooling and cap idle/max PostgreSQL connections; many backends were present during the OOM and reduced the safety margin.
7. **Add alerts:** warn when ZenithPanel RSS exceeds 400–500 MiB, swap usage exceeds 70%, memory PSI `full` stays nonzero, or Docker emits `oom`, `die` or restart events.
8. **Measure the client route over time:** run MTR/packet-loss sampling from the actually affected client/ISP during a reconnect episode. The present snapshot does not support blaming the DMIT NIC or firewall.

## Suggested validation criteria after remediation

- No Docker OOM/restart events for 72 hours.
- ZenithPanel RSS reaches a stable plateau below 250 MiB under normal traffic.
- Swap usage does not trend upward and memory PSI `full` remains zero.
- No ordinary `settings`, `clients` or `inbounds` query exceeds one second.
- Effective SQLite journal mode reports `wal` after every restart.
- `zenithctl proxy test all` remains green and established VLESS sessions survive panel UI polling/egress maintenance.
- Route monitoring from affected clients shows packet loss below 0.5% over representative peak-hour windows.

## Change statement

No VPS configuration, firewall rule, database value, container lifecycle, panel setting or proxy setting was changed during this inspection. Temporary counter snapshots under `/tmp` were removed automatically.
