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

1. Update the panel image to a version that includes `proxy-runner.sh`, then
   run `zenithctl proxy apply` once so the current configuration is validated.
   Record the current image ID (`docker inspect -f '{{.Image}}' zenithpanel`)
   and the original container configuration, including mounts and environment.
2. Stop the old panel container during the maintenance window, then start an
   initially idle runner from the recorded image, using host networking and
   the exact same writable data mount. The example assumes the original mount
   is `/opt/zenithpanel/data` on the host:

   ```sh
   docker run -d --name zenith-proxy --restart unless-stopped --network host \
     -v /opt/zenithpanel/data:/opt/zenithpanel/data \
     --entrypoint /opt/zenithpanel/scripts/proxy-runner.sh \
     <recorded-image-id>
   ```

3. Rename the stopped old container and recreate the panel with its original
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
  The optional host socket egress sampler can have reduced process visibility
  across container PID namespaces; treat it as best-effort in this deployment.
- Do not use this mode with a missing persistent data mount, bridge networking,
  or a manually managed second proxy on the same ports.
- If external apply reports `rollback_unverified`, old engine markers were
  restored but old listener readiness could not be confirmed; inspect the
  runner and verify connectivity before treating the service as recovered.
