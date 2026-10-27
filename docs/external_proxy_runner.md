# Panel-only updates with a persistent proxy runner

ZenithPanel normally starts Xray and sing-box as child processes of the panel
container. Replacing that container stops those processes and disconnects
existing VLESS and Hysteria2 sessions.

The optional external runner separates the data plane from the panel. Once it
is installed, a **panel-only** OTA replaces the panel container while the
runner keeps its existing core processes, listeners and connections. This is
an opt-in deployment mode, enabled only with:

```sh
ZENITH_PROXY_RUNTIME=external
ZENITH_PROXY_RUNNER_CONTAINER=zenith-proxy # optional; this is the default
```

Both containers must use Linux host networking and mount the exact same
writable `/opt/zenithpanel/data` volume. The updater checks these conditions,
the runner identity, and every engine required by enabled inbounds before it
stops the old panel. It refuses the update if any check fails. The runner name
must not be the panel name, `<panel>-old`, or `zenith-updater`.

## One-time migration

Migration cannot preserve existing sessions: the old panel owns the listening
processes, so starting a runner on the same ports requires a planned handoff.
Schedule a maintenance window and take a backup first.

1. Before pulling or updating anything, export a backup and record the running
   panel's image ID (`docker inspect -f '{{.Image}}' zenithpanel`), mounts,
   networking and environment. Keep that exact image for the first runner.
   This preserves the working proxy binaries and client compatibility.
2. Pull the new panel image, then copy its runner script from an **unstarted**
   helper container. Do not update the running panel yet:

   ```sh
   docker pull ghcr.io/harveyxiacn/zenithpanel:main
   docker create --name zenith-runner-script ghcr.io/harveyxiacn/zenithpanel:main
   docker cp zenith-runner-script:/opt/zenithpanel/scripts/proxy-runner.sh /opt/zenithpanel/proxy-runner.sh
   docker rm zenith-runner-script
   chmod 700 /opt/zenithpanel/proxy-runner.sh
   ```

   Stop the old panel during the maintenance window, then start an initially
   idle runner from the **recorded old image ID**, using host networking and
   the original writable data mount. Mounting the script also works with old
   images which do not contain it. The example assumes the original host data
   mount is `/opt/zenithpanel/data`. For a named volume, replace the data
   mount with its exact name (for example, `-v zenith-data:/opt/zenithpanel/data`).
   A different mount would create an empty panel database:

   ```sh
   docker run -d --name zenith-proxy --restart unless-stopped --network host \
     -v /opt/zenithpanel/data:/opt/zenithpanel/data \
     -v /opt/zenithpanel/proxy-runner.sh:/opt/zenithpanel/scripts/proxy-runner.sh:ro \
     --entrypoint /bin/sh <recorded-old-image-id> \
     /opt/zenithpanel/scripts/proxy-runner.sh
   ```

3. Rename the stopped old container and recreate the panel from the new image with its original
   host configuration, Docker socket, data mounts and environment, adding
   `-e ZENITH_PROXY_RUNTIME=external`. Retain the stopped old container for
   rollback. Reconstruct the original configuration carefully; Docker cannot
   add an environment variable to an existing container.
4. Run `zenithctl proxy apply` once while the idle runner is active. It writes
   explicit engine markers and starts the necessary cores; then verify
   `zenithctl proxy status` and `zenithctl proxy test all`. Retain the backup
   and old container until verification succeeds. This is a maintenance event
   and disconnects existing sessions.

## Operational boundaries

- Panel-only OTA preserves the runner and its current sessions. It does not
  upgrade Xray or sing-box inside the runner.
- `proxy apply`, backup reconciliation, and routing changes write validated
  shared configs and restart the runner. They may disconnect clients.
- Upgrading the runner image, Xray, sing-box, or changing inbound ports still
  requires a planned proxy restart. There is no single-host drain mechanism in
  this mode.
- Xray statistics and sing-box Clash API remain reachable over host loopback.
  Xray's cumulative accounting baseline is committed with client totals so
  panel replacement cannot charge the same traffic twice. The first adoption
  of this baseline seeds existing counters without charging historical bytes.
  Sing-box seeds live connections on its first post-replacement snapshot, so
  a newly opened connection that also closes before the following 2-second
  poll can remain unattributed rather than duplicate historical bytes.
  The optional host socket egress sampler can have reduced process visibility
  across container PID namespaces; treat it as best-effort in this deployment.
- Do not use this mode with a missing persistent data mount, bridge networking,
  or a manually managed second proxy on the same ports.
- If external apply reports `rollback_unverified`, old engine markers were
  restored but old listener readiness could not be confirmed; inspect the
  runner and verify connectivity before treating the service as recovered.

## Reality client compatibility

Xray 26.10.21 requires the `X25519MLKEM768` key share for Reality. The official
sing-box 1.14.x client removes that share, so it cannot authenticate to this
Xray version. This is an upstream protocol compatibility issue, independent
of IPv4/IPv6. The panel's Reality self-test uses Xray, while Hysteria2 uses
sing-box. See the [upstream report](https://github.com/SagerNet/sing-box/issues/4520).

If existing clients use sing-box or their core version is unknown, follow the
migration above to retain the existing runner image. A panel-only update then
preserves its core versions. Upgrade the runner separately only after testing
all deployed clients with the candidate core. Passing the bundled Xray-client
test does not establish compatibility for a different client implementation.
