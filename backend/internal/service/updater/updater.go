package updater

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
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

// CheckForUpdate pulls the latest image and compares its digest with the current container's image.
func CheckForUpdate(ctx context.Context) (*UpdateInfo, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	containerID, err := getContainerID()
	if err != nil {
		return nil, fmt.Errorf("get container ID: %w", err)
	}

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	currentImageID := info.Image

	// Pull latest image tag (downloads new layers if any)
	reader, err := cli.ImagePull(ctx, DefaultImage, types.ImagePullOptions{})
	if err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	// Inspect pulled image to get its ID
	imgInspect, _, err := cli.ImageInspectWithRaw(ctx, DefaultImage)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}

	return &UpdateInfo{
		Available: currentImageID != imgInspect.ID,
		CurrentID: truncID(currentImageID),
		LatestID:  truncID(imgInspect.ID),
	}, nil
}

// PerformUpdate recreates the current container with the latest image.
// A helper container orchestrates the swap: stop old → start new → cleanup.
// This avoids port conflicts when using --network=host.
func PerformUpdate(ctx context.Context) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}

	// 1. Pull latest image
	reader, err := cli.ImagePull(ctx, DefaultImage, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

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

	// 3. Prepare new container config with updated image
	newConfig := info.Config
	newConfig.Image = DefaultImage
	hostConfig := info.HostConfig
	containerName := strings.TrimPrefix(info.Name, "/")
	oldName := containerName + "-old"

	// Clean up leftover containers from previous update attempts
	cli.ContainerStop(ctx, oldName, container.StopOptions{})
	cli.ContainerRemove(ctx, oldName, types.ContainerRemoveOptions{Force: true})
	cli.ContainerStop(ctx, "zenith-updater", container.StopOptions{})
	cli.ContainerRemove(ctx, "zenith-updater", types.ContainerRemoveOptions{Force: true})

	// 4. Rename old container to free the name
	if err := cli.ContainerRename(ctx, containerID, oldName); err != nil {
		return fmt.Errorf("rename container: %w", err)
	}

	// 5. Create new container with same config + new image
	resp, err := cli.ContainerCreate(ctx, newConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		cli.ContainerRename(ctx, containerID, containerName) // rollback
		return fmt.Errorf("create container: %w", err)
	}

	// 6. Spawn a helper container to orchestrate the swap.
	// The helper stops the old container first (freeing ports), then starts the new one.
	// This avoids port conflicts when using --network=host or mapped ports.
	swapScript := fmt.Sprintf(
		`sleep 2; docker stop -t 10 %s 2>/dev/null; docker start %s; docker rm %s 2>/dev/null; true`,
		containerID, resp.ID, containerID,
	)
	helperCfg := &container.Config{
		Image:      DefaultImage,
		Entrypoint: []string{"sh", "-c"},
		Cmd:        []string{swapScript},
	}
	helperHC := &container.HostConfig{
		Binds:      []string{"/var/run/docker.sock:/var/run/docker.sock"},
		AutoRemove: true,
	}
	helperResp, err := cli.ContainerCreate(ctx, helperCfg, helperHC, nil, nil, "zenith-updater")
	if err != nil {
		cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("create updater helper: %w", err)
	}
	if err := cli.ContainerStart(ctx, helperResp.ID, types.ContainerStartOptions{}); err != nil {
		cli.ContainerRemove(ctx, helperResp.ID, types.ContainerRemoveOptions{})
		cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})
		cli.ContainerRename(ctx, containerID, containerName)
		return fmt.Errorf("start updater helper: %w", err)
	}

	log.Printf("OTA: helper container started, will swap %s -> %s in ~2s", containerID[:12], resp.ID[:12])
	return nil
}

func truncID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
