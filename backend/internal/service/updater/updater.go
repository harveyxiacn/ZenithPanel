package updater

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/version"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

const DefaultImage = "ghcr.io/harveyxiacn/zenithpanel:main"

const ociImageLabelPrefix = "org.opencontainers.image."

// mergeContainerLabels keeps labels explicitly assigned to the old
// container, while taking OCI image metadata from the candidate image. Docker
// does not reliably replace labels when a container Config is cloned, so stale
// org.opencontainers.image.* values must be removed before the candidate ones
// are applied.
func mergeContainerLabels(containerLabels, candidateImageLabels map[string]string) map[string]string {
	merged := make(map[string]string, len(containerLabels)+len(candidateImageLabels))
	for key, value := range containerLabels {
		if !strings.HasPrefix(key, ociImageLabelPrefix) {
			merged[key] = value
		}
	}
	for key, value := range candidateImageLabels {
		if strings.HasPrefix(key, ociImageLabelPrefix) {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func updateImage() string {
	if configured := strings.TrimSpace(os.Getenv("ZENITH_UPDATE_IMAGE")); configured != "" {
		return configured
	}
	return DefaultImage
}

// UpdateInfo contains the result of an update check.
type UpdateInfo struct {
	Available bool `json:"available"`
	// CurrentVersion is the human-readable build version of the running panel
	// (e.g. "v1.0.0"), baked in at build time — friendlier than the digest.
	CurrentVersion string `json:"current_version"`
	CurrentID      string `json:"current_id"`
	LatestID       string `json:"latest_id"`
}

// getContainerID detects the current Docker container ID.
// It tries multiple sources in order of reliability:
//  1. /proc/self/mountinfo — Docker bind-mounts /etc/hostname from
//     /var/lib/docker/containers/<id>/hostname, visible in all cgroup versions.
//  2. /proc/self/cgroup — works on cgroup v1 and some v2 setups.
//  3. os.Hostname() — fallback, works when hostname equals the container ID.
func getContainerID() (string, error) {
	// Method 1: Parse mountinfo for docker container paths
	if id, err := getContainerIDFromMountinfo(); err == nil {
		return id, nil
	}

	// Method 2: Parse cgroup
	if id, err := getContainerIDFromCgroup(); err == nil {
		return id, nil
	}

	// Method 3: Fallback to hostname
	return os.Hostname()
}

// getContainerIDFromMountinfo reads /proc/self/mountinfo looking for Docker's
// bind-mount of /etc/hostname or /etc/resolv.conf from /var/lib/docker/containers/<id>/.
// This works on both cgroup v1 and v2, and regardless of --pid=host.
func getContainerIDFromMountinfo() (string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, "/docker/containers/"); idx != -1 {
			rest := line[idx+len("/docker/containers/"):]
			if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
				id := rest[:slashIdx]
				if len(id) >= 12 {
					return id, nil
				}
			}
		}
	}
	return "", fmt.Errorf("container ID not found in mountinfo")
}

// getContainerIDFromCgroup parses /proc/self/cgroup for docker container IDs.
// Works on cgroup v1 ("N:xyz:/docker/<id>") and some cgroup v2 setups.
func getContainerIDFromCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		// "0::/docker/<id>" or "N:xyz:/docker/<id>"
		if idx := strings.LastIndex(line, "/docker/"); idx != -1 {
			id := strings.TrimSpace(line[idx+len("/docker/"):])
			if len(id) >= 12 {
				return id, nil
			}
		}
		// systemd: "0::/system.slice/docker-<id>.scope"
		if idx := strings.Index(line, "docker-"); idx != -1 {
			id := line[idx+len("docker-"):]
			if dotIdx := strings.Index(id, "."); dotIdx != -1 {
				id = id[:dotIdx]
			}
			id = strings.TrimSpace(id)
			if len(id) >= 12 {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("container ID not found in cgroup")
}

// newUpdateInfo compares like-for-like registry manifest digests. Docker's
// container inspect Image field is a config-object digest, while distribution
// inspect returns a manifest digest; directly comparing those two always
// reports an update. RepoDigests are the registry digests attached to the
// locally pulled image and therefore the correct comparison source.
func newUpdateInfo(currentImageID string, repoDigests []string, imageRef string, inspect registry.DistributionInspect) *UpdateInfo {
	latestImageID := inspect.Descriptor.Digest.String()
	currentID := currentImageID
	available := true
	repository := imageRepository(imageRef)
	for _, repoDigest := range repoDigests {
		name, digest, ok := strings.Cut(repoDigest, "@")
		if !ok {
			continue
		}
		if repository == "" || strings.EqualFold(name, repository) {
			currentID = digest
			if digest == latestImageID {
				available = false
			}
			break
		}
	}
	return &UpdateInfo{
		Available: available,
		CurrentID: truncID(currentID),
		LatestID:  truncID(latestImageID),
	}
}

func imageRepository(ref string) string {
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		return ref[:at]
	}
	lastSlash := strings.LastIndexByte(ref, '/')
	if colon := strings.LastIndexByte(ref, ':'); colon > lastSlash {
		return ref[:colon]
	}
	return ref
}

var runCosignVerify = func(ctx context.Context, candidate, identity, issuer string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "cosign", "verify",
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", issuer,
		candidate,
	)
	return cmd.CombinedOutput()
}

func verifyCandidateSignature(ctx context.Context, candidate string) error {
	verifySetting := strings.ToLower(strings.TrimSpace(os.Getenv("ZENITH_COSIGN_VERIFY")))
	if verifySetting == "false" || verifySetting == "0" || verifySetting == "no" {
		return nil
	}
	identity := strings.TrimSpace(os.Getenv("ZENITH_COSIGN_IDENTITY"))
	if identity == "" {
		identity = "https://github.com/harveyxiacn/ZenithPanel/.github/workflows/main.yml@refs/heads/main"
	}
	issuer := strings.TrimSpace(os.Getenv("ZENITH_COSIGN_ISSUER"))
	if issuer == "" {
		issuer = "https://token.actions.githubusercontent.com"
	}
	output, err := runCosignVerify(ctx, candidate, identity, issuer)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("cosign verify %s: %s", candidate, message)
	}
	return nil
}

// swapScript builds the helper-container script that performs the actual
// swap: stop old → start new → remove old container → prune dangling images.
// The prune reclaims the superseded panel image (untagged once the new pull
// took over the :main tag) so repeated OTAs don't slowly fill small disks.
// `image prune` without -a removes ONLY dangling (untagged, unreferenced)
// images, so other containers' tagged images are never touched. It must run
// after `docker rm` — until the old container is gone its image is still
// referenced and the prune would skip it.
func swapScript() string {
	return `set -u
old_id="$ZENITH_OLD_ID"
new_id="$ZENITH_NEW_ID"
original_name="$ZENITH_ORIGINAL_NAME"
status_file="$ZENITH_UPDATE_STATUS_FILE"
previous_image="$ZENITH_PREVIOUS_IMAGE"
candidate_image="$ZENITH_CANDIDATE_IMAGE"
candidate_image_id="${ZENITH_CANDIDATE_IMAGE_ID:-}"

write_status() {
  target="$1"
  state="$2"
  phase="$3"
  message="$4"
  rolled_back="$5"
  # The helper shares the original data volume, so failures remain visible
  # even when neither panel container can execute a command.
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  tmp="${status_file}.tmp"
  printf '{"state":"%s","phase":"%s","message":"%s","previous_image":"%s","candidate_image":"%s","rolled_back":%s,"updated_at":"%s"}\n' \
   "$state" "$phase" "$message" "$previous_image" "$candidate_image" "$rolled_back" "$now" > "$tmp" && mv "$tmp" "$status_file"

}

rollback() {
 needs_rollback=0
 restored=1
 docker rm -f "$new_id" >/dev/null 2>&1 || restored=0
 docker rename "$old_id" "$original_name" >/dev/null 2>&1 || restored=0
 docker start "$old_id" >/dev/null 2>&1 || restored=0
 if [ "$restored" = "1" ]; then
  attempt=0
  restored=0
  while [ "$attempt" -lt 30 ]; do
   if docker exec "$old_id" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q status >/dev/null 2>&1; then restored=1;break;fi
   attempt=$((attempt+1));sleep 2
  done
 fi
 if [ "$restored" = "1" ]; then
  write_status "$old_id" "rolled_back" "rollback" "candidate failed; previous container restarted and database health verified" "true"
 else
  write_status "$old_id" "rollback_failed" "rollback" "previous container recovery failed; operator action required" "false"
  echo "rollback_failed: previous container recovery failed" >&2
 fi
}

needs_rollback=0
trap 'if [ "$needs_rollback" = "1" ]; then rollback; fi' EXIT
trap 'exit 1' HUP INT TERM

write_status "$old_id" "running" "switch" "stopping previous container" "false"
needs_rollback=1
if ! docker stop -t 10 "$old_id" >/dev/null; then
  exit 1
fi
if ! docker start "$new_id" >/dev/null; then
  exit 1
fi
if [ -n "$candidate_image_id" ]; then
  actual_image=$(docker inspect --format '{{.Image}}' "$new_id" 2>/dev/null || true)
  if [ "$actual_image" != "$candidate_image_id" ]; then
    echo "candidate container image ID mismatch" >&2
    exit 1
  fi
fi
write_status "$new_id" "running" "health_check" "candidate container started" "false"

attempt=0
healthy=0
while [ "$attempt" -lt 30 ]; do
	if docker exec "$new_id" /opt/zenithpanel/zenithpanel ctl --host unix:///run/zenithpanel.sock -q ready >/dev/null 2>&1; then
 healthy=$((healthy+1))
 if [ "$healthy" -ge 3 ]; then
    if ! docker rm "$old_id" >/dev/null; then
      exit 1
    fi
    needs_rollback=0
		write_status "$new_id" "succeeded" "commit" "candidate passed health check and previous container was removed" "false"
    docker image prune -f >/dev/null 2>&1 || true
    exit 0
 fi
 else
 healthy=0
  fi
  attempt=$((attempt + 1))
  sleep 2
done

exit 1`
}

// buildHelperContainerConfig returns the container and host config for the
// updater helper. Uses the panel image directly to avoid `apk add` at runtime.
func buildHelperContainerConfig(image, oldID, newID, originalName, previousImage, candidateImage string) (*container.Config, *container.HostConfig) {
	return &container.Config{
			Image:      image,
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{swapScript()},
			Env: []string{
				"ZENITH_OLD_ID=" + oldID,
				"ZENITH_NEW_ID=" + newID,
				"ZENITH_ORIGINAL_NAME=" + originalName,
				"ZENITH_UPDATE_STATUS_FILE=" + statusPath(),
				"ZENITH_PREVIOUS_IMAGE=" + previousImage,
				"ZENITH_CANDIDATE_IMAGE=" + candidateImage,
			},
		}, &container.HostConfig{
			Binds:       []string{"/var/run/docker.sock:/var/run/docker.sock"},
			VolumesFrom: []string{oldID + ":rw"},
			AutoRemove:  true,
		}
}

const panelDataPath = "/opt/zenithpanel/data"

func dataMountIdentity(info container.InspectResponse) (string, bool) {
	for _, m := range info.Mounts {
		if m.Destination != panelDataPath || !m.RW {
			continue
		}
		if m.Name != "" {
			return string(m.Type) + ":name:" + m.Name, true
		}
		if m.Source != "" {
			return string(m.Type) + ":source:" + m.Source, true
		}
	}
	return "", false
}

// validateExternalRunnerTopology prevents a panel update from claiming
// connection preservation when its runner cannot share the listeners and
// generated configs. Both containers must use host networking and the exact
// same writable persistent data mount.
func validateExternalRunnerTopology(panel, runner container.InspectResponse) error {
	if panel.HostConfig == nil || panel.HostConfig.NetworkMode != "host" {
		return fmt.Errorf("external proxy runtime requires the panel container to use host networking")
	}
	if runner.HostConfig == nil || runner.HostConfig.NetworkMode != "host" {
		return fmt.Errorf("external proxy runner must use host networking")
	}
	panelData, ok := dataMountIdentity(panel)
	if !ok {
		return fmt.Errorf("panel container has no writable %s mount", panelDataPath)
	}
	runnerData, ok := dataMountIdentity(runner)
	if !ok || runnerData != panelData {
		return fmt.Errorf("external proxy runner must share the panel's writable %s mount", panelDataPath)
	}
	return nil
}

func validateExternalRunnerIdentity(panelID, runnerID, runnerName, panelName string) error {
	if runnerID == panelID {
		return fmt.Errorf("external proxy runner resolves to the panel container itself")
	}
	oldName := panelName + "-old"
	if runnerName == panelName || runnerName == oldName || runnerName == "zenith-updater" {
		return fmt.Errorf("external proxy runner name %q conflicts with panel update container lifecycle", runnerName)
	}
	return nil
}

// CheckForUpdate inspects the registry digest without pulling layers and
// compares it with the running container's image ID.
func CheckForUpdate(ctx context.Context) (*UpdateInfo, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	containerID, err := getContainerID()
	if err != nil {
		return nil, fmt.Errorf("get container ID: %w", err)
	}

	inspectResult, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	info := inspectResult.Container

	imageRef := updateImage()
	distInspect, err := cli.DistributionInspect(ctx, imageRef, client.DistributionInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect registry image: %w", err)
	}
	imageInspect, err := cli.ImageInspect(ctx, info.Image)
	if err != nil {
		return nil, fmt.Errorf("inspect current image: %w", err)
	}

	ui := newUpdateInfo(info.Image, imageInspect.RepoDigests, imageRef, distInspect.DistributionInspect)
	ui.CurrentVersion = version.Version
	return ui, nil
}

// PerformUpdate recreates the current container with the latest image.
// A helper container orchestrates the swap: stop old → start new → cleanup.
// This avoids port conflicts when using --network=host.
//
// IMPORTANT: This function uses its own background context (not the HTTP request
// context) to prevent Docker API calls from being cancelled if the HTTP
// connection drops or times out during the operation.
func PerformUpdate(_ context.Context) (retErr error) {
	status := Status{State: "running", Phase: "prepare", Message: "preparing signed candidate image"}
	if err := writeStatus(status); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			status.State = "failed"
			status.Message = retErr.Error()
			_ = writeStatus(status)
		}
	}()
	// Use a background context so Docker operations survive HTTP request cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	// An external runner is the only supported mode in which replacing this
	// panel container preserves proxy processes. Refuse before pulling or
	// renaming anything if the operator enabled it but its runner is absent.
	if err := proxy.RequireExternalRuntimeReady(ctx); err != nil {
		return err
	}

	imageRef := updateImage()
	distInspect, err := cli.DistributionInspect(ctx, imageRef, client.DistributionInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect candidate image: %w", err)
	}
	digest := distInspect.Descriptor.Digest.String()
	if digest == "" {
		return fmt.Errorf("inspect candidate image: registry returned an empty digest")
	}
	candidateRef := imageRepository(imageRef) + "@" + digest
	status.Candidate = candidateRef
	status.Phase = "signature_verify"
	status.Message = "verifying Sigstore identity"
	if err := writeStatus(status); err != nil {
		return err
	}
	if err := verifyCandidateSignature(ctx, candidateRef); err != nil {
		return err
	}

	// 1. Pull latest image (may already be cached from check)
	status.Phase = "pull"
	status.Message = "pulling digest-pinned candidate"
	if err := writeStatus(status); err != nil {
		return err
	}
	log.Printf("OTA: pulling digest-pinned image %s...", candidateRef)
	reader, err := cli.ImagePull(ctx, candidateRef, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()
	log.Println("OTA: image pull complete")
	candidateInspect, err := cli.ImageInspect(ctx, candidateRef)
	if err != nil {
		return fmt.Errorf("inspect pulled candidate image: %w", err)
	}
	if strings.TrimSpace(candidateInspect.ID) == "" {
		return fmt.Errorf("inspect pulled candidate image: empty image ID")
	}

	// 2. Get current container ID and full config
	containerID, err := getContainerID()
	if err != nil {
		return fmt.Errorf("get container ID: %w", err)
	}
	log.Printf("OTA: detected self container ID: %s", containerID)

	inspectResult, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}
	info := inspectResult.Container
	externalRunnerID := ""
	externalRunnerContainerName := ""
	if proxy.ExternalRuntimeEnabled() {
		runnerName, err := proxy.ExternalRunnerName()
		if err != nil {
			return err
		}
		runnerInspect, err := cli.ContainerInspect(ctx, runnerName, client.ContainerInspectOptions{})
		if err != nil {
			return fmt.Errorf("inspect external proxy runner %q: %w", runnerName, err)
		}
		if err := validateExternalRunnerTopology(info, runnerInspect.Container); err != nil {
			return err
		}
		externalRunnerID = runnerInspect.Container.ID
		externalRunnerContainerName = strings.TrimPrefix(runnerInspect.Container.Name, "/")
	}
	status.Previous = info.Image
	if err := writeStatus(status); err != nil {
		return err
	}

	// 3. Prepare new container config with updated image.
	// Clear runtime-only fields that Docker sets during creation and that
	// would conflict or be meaningless on a brand-new container.
	//
	// We deliberately keep info.HostConfig (and thus its Binds) verbatim so
	// the panel's data volume (/opt/zenithpanel/data) carries over: SQLite
	// rows, generated configs, ACME certs, and audit logs all live there.
	// This is the contract that lets operators change inbound ports through
	// the UI and trust those ports to survive the next OTA — see
	// docs/qr_setup_guide.md.
	newConfig := info.Config
	newConfig.Image = candidateInspect.ID
	if candidateInspect.Config != nil {
		newConfig.Labels = mergeContainerLabels(newConfig.Labels, candidateInspect.Config.Labels)
	} else {
		newConfig.Labels = mergeContainerLabels(newConfig.Labels, nil)
	}
	newConfig.Hostname = ""
	newConfig.Domainname = ""
	hostConfig := info.HostConfig

	// Derive the original container name. If a previous failed update left
	// the container renamed to "<name>-old", strip the suffix so we create
	// the new container with the correct original name.
	containerName := strings.TrimPrefix(info.Name, "/")
	containerName = strings.TrimSuffix(containerName, "-old")
	oldName := containerName + "-old"
	if externalRunnerID != "" {
		if err := validateExternalRunnerIdentity(info.ID, externalRunnerID, externalRunnerContainerName, containerName); err != nil {
			return err
		}
	}

	log.Printf("OTA: container name=%s, will rename to=%s", containerName, oldName)

	// Clean up leftover containers from previous update attempts. Each of
	// these can legitimately fail with "no such container" on a first-time
	// upgrade; drop the error and let the rename step below decide whether
	// the host is in a recoverable state.
	_, _ = cli.ContainerStop(ctx, oldName, client.ContainerStopOptions{})
	_, _ = cli.ContainerRemove(ctx, oldName, client.ContainerRemoveOptions{Force: true})
	_, _ = cli.ContainerStop(ctx, "zenith-updater", client.ContainerStopOptions{})
	_, _ = cli.ContainerRemove(ctx, "zenith-updater", client.ContainerRemoveOptions{Force: true})

	// 4. Rename old container to free the name
	if _, err := cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: oldName}); err != nil {
		return fmt.Errorf("rename container %s -> %s: %w", containerID[:12], oldName, err)
	}
	log.Printf("OTA: renamed %s -> %s", containerID[:12], oldName)

	// 5. Create new container with same config + new image
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: newConfig, HostConfig: hostConfig, Name: containerName})
	if err != nil {
		log.Printf("OTA: create failed, rolling back rename: %v", err)
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName}) // rollback
		return fmt.Errorf("create container: %w", err)
	}
	log.Printf("OTA: created new container %s", resp.ID[:12])

	// 6. Spawn a helper container to orchestrate the swap using the panel image
	// (already present locally). Avoids `apk add` at runtime and alpine dependency.
	// The helper runs from the known-good current image ID, not the candidate
	// tag. This keeps rollback available even if the pulled image is broken.
	helperCfg, helperHC := buildHelperContainerConfig(info.Image, containerID, resp.ID, containerName, info.Image, candidateRef)
	helperCfg.Env = append(helperCfg.Env, "ZENITH_CANDIDATE_IMAGE_ID="+candidateInspect.ID)

	helperResp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: helperCfg, HostConfig: helperHC, Name: "zenith-updater"})
	if err != nil {
		_, _ = cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName})
		return fmt.Errorf("create updater helper: %w", err)
	}
	if _, err := cli.ContainerStart(ctx, helperResp.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = cli.ContainerRemove(ctx, helperResp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName})
		return fmt.Errorf("start updater helper: %w", err)
	}

	status.Phase = "helper_started"
	status.Message = "candidate switch delegated to updater helper"
	if err := writeStatus(status); err != nil {
		log.Printf("OTA: persist helper_started status: %v", err)
	}
	log.Printf("OTA: helper container started, will swap %s -> %s in ~2s", containerID[:12], resp.ID[:12])
	return nil
}

// RestartSelf recreates the current container with the same image but updated
// port mapping and environment. Used when the user changes the panel port.
func RestartSelf(_ context.Context, newPort string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	containerID, err := getContainerID()
	if err != nil {
		return fmt.Errorf("get container ID: %w", err)
	}
	log.Printf("Restart: detected self container ID: %s", containerID)

	inspectResult, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}
	info := inspectResult.Container

	newConfig := info.Config
	newConfig.Hostname = ""
	newConfig.Domainname = ""
	hostConfig := info.HostConfig

	// Update port mapping if a new port is specified
	if newPort != "" {
		tcpPort, err := network.ParsePort(newPort + "/tcp")
		if err != nil {
			return fmt.Errorf("invalid panel port %q: %w", newPort, err)
		}
		newConfig.ExposedPorts = network.PortSet{tcpPort: struct{}{}}

		// Update ZENITH_PORT env var
		envUpdated := false
		for i, e := range newConfig.Env {
			if strings.HasPrefix(e, "ZENITH_PORT=") {
				newConfig.Env[i] = "ZENITH_PORT=" + newPort
				envUpdated = true
				break
			}
		}
		if !envUpdated {
			newConfig.Env = append(newConfig.Env, "ZENITH_PORT="+newPort)
		}

		// Update host port bindings: map container port to same host port
		hostConfig.PortBindings = network.PortMap{
			tcpPort: []network.PortBinding{{HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: newPort}},
		}
	}

	containerName := strings.TrimPrefix(info.Name, "/")
	containerName = strings.TrimSuffix(containerName, "-old")
	oldName := containerName + "-old"

	log.Printf("Restart: container name=%s, will rename to=%s", containerName, oldName)

	// Clean up leftovers
	_, _ = cli.ContainerStop(ctx, oldName, client.ContainerStopOptions{})
	_, _ = cli.ContainerRemove(ctx, oldName, client.ContainerRemoveOptions{Force: true})
	_, _ = cli.ContainerStop(ctx, "zenith-updater", client.ContainerStopOptions{})
	_, _ = cli.ContainerRemove(ctx, "zenith-updater", client.ContainerRemoveOptions{Force: true})

	// Rename old container
	if _, err := cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: oldName}); err != nil {
		return fmt.Errorf("rename container: %w", err)
	}
	log.Printf("Restart: renamed %s -> %s", containerID[:12], oldName)

	// Create new container with same image, updated config
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: newConfig, HostConfig: hostConfig, Name: containerName})
	if err != nil {
		log.Printf("Restart: create failed, rolling back: %v", err)
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName})
		return fmt.Errorf("create container: %w", err)
	}
	log.Printf("Restart: created new container %s", resp.ID[:12])

	// Helper container to swap, using the panel image (already local, no apk needed)
	helperCfg, helperHC := buildHelperContainerConfig(info.Image, containerID, resp.ID, containerName, info.Image, info.Image)
	helperResp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Config: helperCfg, HostConfig: helperHC, Name: "zenith-updater"})
	if err != nil {
		_, _ = cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName})
		return fmt.Errorf("create helper: %w", err)
	}
	if _, err := cli.ContainerStart(ctx, helperResp.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = cli.ContainerRemove(ctx, helperResp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{})
		_, _ = cli.ContainerRename(ctx, containerID, client.ContainerRenameOptions{NewName: containerName})
		return fmt.Errorf("start helper: %w", err)
	}

	log.Printf("Restart: helper started, will swap to new port %s in ~2s", newPort)
	return nil
}

func truncID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
