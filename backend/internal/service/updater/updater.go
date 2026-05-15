package updater

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const DefaultImage = "ghcr.io/harveyxiacn/zenithpanel:main"

// UpdateInfo contains the result of an update check.
type UpdateInfo struct {
	Available bool   `json:"available"`
	CurrentID string `json:"current_id"`
	LatestID  string `json:"latest_id"`
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

// newUpdateInfo compares the running container's image ID against the registry
// distribution descriptor without pulling any layers.
func newUpdateInfo(currentImageID string, inspect registry.DistributionInspect) *UpdateInfo {
	latestImageID := inspect.Descriptor.Digest.String()
	return &UpdateInfo{
		Available: currentImageID != latestImageID,
		CurrentID: truncID(currentImageID),
		LatestID:  truncID(latestImageID),
	}
}

// buildHelperContainerConfig returns the container and host config for the
// updater helper. Uses the panel image directly to avoid `apk add` at runtime.
func buildHelperContainerConfig(image, swapScript string) (*container.Config, *container.HostConfig) {
	return &container.Config{
		Image:      image,
		Entrypoint: []string{"sh", "-c"},
		Cmd:        []string{swapScript},
	}, &container.HostConfig{
		Binds:      []string{"/var/run/docker.sock:/var/run/docker.sock"},
		AutoRemove: true,
	}
}

// CheckForUpdate inspects the registry digest without pulling layers and
// compares it with the running container's image ID.
func CheckForUpdate(ctx context.Context) (*UpdateInfo, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	containerID, err := getContainerID()
	if err != nil {
		return nil, fmt.Errorf("get container ID: %w", err)
	}

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	distInspect, err := cli.DistributionInspect(ctx, DefaultImage, "")
	if err != nil {
		return nil, fmt.Errorf("inspect registry image: %w", err)
	}

	return newUpdateInfo(info.Image, registry.DistributionInspect(distInspect)), nil
}

// PerformUpdate recreates the current container with the latest image.
// A helper container orchestrates the swap: stop old → start new → cleanup.
// This avoids port conflicts when using --network=host.
//
// IMPORTANT: This function uses its own background context (not the HTTP request
// context) to prevent Docker API calls from being cancelled if the HTTP
// connection drops or times out during the operation.
func PerformUpdate(_ context.Context) error {
	// Use a background context so Docker operations survive HTTP request cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	// 1. Pull latest image (may already be cached from check)
	log.Println("OTA: pulling latest image...")
	reader, err := cli.ImagePull(ctx, DefaultImage, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()
	log.Println("OTA: image pull complete")

	// 2. Get current container ID and full config
	containerID, err := getContainerID()
	if err != nil {
		return fmt.Errorf("get container ID: %w", err)
	}
	log.Printf("OTA: detected self container ID: %s", containerID)

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}

	// 3. Prepare new container config with updated image.
	// Clear runtime-only fields that Docker sets during creation and that
	// would conflict or be meaningless on a brand-new container.
	newConfig := info.Config
	newConfig.Image = DefaultImage
	newConfig.Hostname = ""
	newConfig.Domainname = ""
	hostConfig := info.HostConfig

	// Derive the original container name. If a previous failed update left
	// the container renamed to "<name>-old", strip the suffix so we create
	// the new container with the correct original name.
	containerName := strings.TrimPrefix(info.Name, "/")
	containerName = strings.TrimSuffix(containerName, "-old")
	oldName := containerName + "-old"

	log.Printf("OTA: container name=%s, will rename to=%s", containerName, oldName)

	// Clean up leftover containers from previous update attempts. Each of
	// these can legitimately fail with "no such container" on a first-time
	// upgrade; drop the error and let the rename step below decide whether
	// the host is in a recoverable state.
	_ = cli.ContainerStop(ctx, oldName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, oldName, types.ContainerRemoveOptions{Force: true})
	_ = cli.ContainerStop(ctx, "zenith-updater", container.StopOptions{})
	_ = cli.ContainerRemove(ctx, "zenith-updater", types.ContainerRemoveOptions{Force: true})

	// 4. Rename old container to free the name
	if err := cli.ContainerRename(ctx, containerID, oldName); err != nil {
		return fmt.Errorf("rename container %s -> %s: %w", containerID[:12], oldName, err)
	}
	log.Printf("OTA: renamed %s -> %s", containerID[:12], oldName)

	// 5. Create new container with same config + new image
	resp, err := cli.ContainerCreate(ctx, newConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		log.Printf("OTA: create failed, rolling back rename: %v", err)
		_ = cli.ContainerRename(ctx, containerID, containerName) // rollback
		return fmt.Errorf("create container: %w", err)
	}
	log.Printf("OTA: created new container %s", resp.ID[:12])

	// 6. Spawn a helper container to orchestrate the swap using the panel image
	// (already present locally). Avoids `apk add` at runtime and alpine dependency.
	swapScript := fmt.Sprintf(
		`sleep 2; docker stop -t 10 %s 2>/dev/null; docker start %s; docker rm %s 2>/dev/null; true`,
		containerID, resp.ID, containerID,
	)
	helperCfg, helperHC := buildHelperContainerConfig(DefaultImage, swapScript)

	helperResp, err := cli.ContainerCreate(ctx, helperCfg, helperHC, nil, nil, "zenith-updater")
	if err != nil {
		_ = cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("create updater helper: %w", err)
	}
	if err := cli.ContainerStart(ctx, helperResp.ID, types.ContainerStartOptions{}); err != nil {
		_ = cli.ContainerRemove(ctx, helperResp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("start updater helper: %w", err)
	}

	log.Printf("OTA: helper container started, will swap %s -> %s in ~2s", containerID[:12], resp.ID[:12])
	return nil
}

// RestartSelf recreates the current container with the same image but updated
// port mapping and environment. Used when the user changes the panel port.
func RestartSelf(_ context.Context, newPort string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	containerID, err := getContainerID()
	if err != nil {
		return fmt.Errorf("get container ID: %w", err)
	}
	log.Printf("Restart: detected self container ID: %s", containerID)

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}

	newConfig := info.Config
	newConfig.Hostname = ""
	newConfig.Domainname = ""
	hostConfig := info.HostConfig

	// Update port mapping if a new port is specified
	if newPort != "" {
		tcpPort := nat.Port(newPort + "/tcp")
		newConfig.ExposedPorts = nat.PortSet{tcpPort: struct{}{}}

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
		hostConfig.PortBindings = nat.PortMap{
			tcpPort: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: newPort}},
		}
	}

	containerName := strings.TrimPrefix(info.Name, "/")
	containerName = strings.TrimSuffix(containerName, "-old")
	oldName := containerName + "-old"

	log.Printf("Restart: container name=%s, will rename to=%s", containerName, oldName)

	// Clean up leftovers
	_ = cli.ContainerStop(ctx, oldName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, oldName, types.ContainerRemoveOptions{Force: true})
	_ = cli.ContainerStop(ctx, "zenith-updater", container.StopOptions{})
	_ = cli.ContainerRemove(ctx, "zenith-updater", types.ContainerRemoveOptions{Force: true})

	// Rename old container
	if err := cli.ContainerRename(ctx, containerID, oldName); err != nil {
		return fmt.Errorf("rename container: %w", err)
	}
	log.Printf("Restart: renamed %s -> %s", containerID[:12], oldName)

	// Create new container with same image, updated config
	resp, err := cli.ContainerCreate(ctx, newConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		log.Printf("Restart: create failed, rolling back: %v", err)
		_ = cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("create container: %w", err)
	}
	log.Printf("Restart: created new container %s", resp.ID[:12])

	// Helper container to swap, using the panel image (already local, no apk needed)
	swapScript := fmt.Sprintf(
		`sleep 2; docker stop -t 10 %s 2>/dev/null; docker start %s; docker rm %s 2>/dev/null; true`,
		containerID, resp.ID, containerID,
	)
	helperCfg, helperHC := buildHelperContainerConfig(newConfig.Image, swapScript)
	helperResp, err := cli.ContainerCreate(ctx, helperCfg, helperHC, nil, nil, "zenith-updater")
	if err != nil {
		_ = cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("create helper: %w", err)
	}
	if err := cli.ContainerStart(ctx, helperResp.ID, types.ContainerStartOptions{}); err != nil {
		_ = cli.ContainerRemove(ctx, helperResp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		_ = cli.ContainerRename(ctx, containerID, containerName)
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
