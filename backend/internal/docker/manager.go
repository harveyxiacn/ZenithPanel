package docker

import (
	"context"
	"encoding/json"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Manager wraps the docker client.
type Manager struct {
	cli *client.Client
}

// RunContainerRequest holds the parameters for creating and starting a container.
type RunContainerRequest struct {
	Image         string
	Name          string
	Ports         []string // host:container/proto, e.g. "8080:80/tcp"
	Volumes       []string // host:container, e.g. "/data:/data"
	Env           []string // KEY=VALUE
	Cmd           []string
	RestartPolicy string // "always"|"unless-stopped"|"on-failure"|"no"
	NetworkMode   string // "bridge"|"host"|""
}

// NewManager creates a new Docker client manager.
func NewManager() (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Manager{cli: cli}, nil
}

// ListContainers returns a list of containers (all=true includes stopped).
func (m *Manager) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	return m.cli.ContainerList(ctx, types.ContainerListOptions{All: all})
}

// StartContainer starts a stopped container.
func (m *Manager) StartContainer(ctx context.Context, id string) error {
	return m.cli.ContainerStart(ctx, id, types.ContainerStartOptions{})
}

// StopContainer stops a running container with a 10-second timeout.
func (m *Manager) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	return m.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RestartContainer restarts a container with a 10-second timeout.
func (m *Manager) RestartContainer(ctx context.Context, id string) error {
	timeout := 10
	return m.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RemoveContainer removes a container, optionally with force.
func (m *Manager) RemoveContainer(ctx context.Context, id string, force bool) error {
	return m.cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{Force: force})
}

// ListImages returns all local images.
func (m *Manager) ListImages(ctx context.Context) ([]types.ImageSummary, error) {
	return m.cli.ImageList(ctx, types.ImageListOptions{})
}

// PullImage pulls an image from a registry. Returns a ReadCloser that streams
// JSON progress events; the caller must drain and close it.
func (m *Manager) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return m.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
}

// RemoveImage removes an image by ID or reference.
func (m *Manager) RemoveImage(ctx context.Context, id string, force bool) ([]types.ImageDeleteResponseItem, error) {
	return m.cli.ImageRemove(ctx, id, types.ImageRemoveOptions{Force: force})
}

// GetContainerLogs returns the last `tail` lines from a container's stdout+stderr.
// Pass tail="all" for all lines.
func (m *Manager) GetContainerLogs(ctx context.Context, id string, tail string) (string, error) {
	rc, err := m.cli.ContainerLogs(ctx, id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ContainerStatsSnapshot holds a one-shot CPU and memory sample.
type ContainerStatsSnapshot struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsageMB float64 `json:"memory_usage_mb"`
	MemoryLimitMB float64 `json:"memory_limit_mb"`
}

// GetContainerStats returns a single CPU/memory snapshot for a container.
func (m *Manager) GetContainerStats(ctx context.Context, id string) (*ContainerStatsSnapshot, error) {
	resp, err := m.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw types.StatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage - raw.PreCPUStats.SystemUsage)
	numCPU := float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
	if numCPU == 0 {
		numCPU = float64(raw.CPUStats.OnlineCPUs)
	}
	cpuPct := 0.0
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPct = (cpuDelta / sysDelta) * numCPU * 100.0
	}

	const mb = 1024 * 1024
	return &ContainerStatsSnapshot{
		CPUPercent:    cpuPct,
		MemoryUsageMB: float64(raw.MemoryStats.Usage) / mb,
		MemoryLimitMB: float64(raw.MemoryStats.Limit) / mb,
	}, nil
}

// RunContainer creates and starts a new container. Returns the container ID.
func (m *Manager) RunContainer(ctx context.Context, req RunContainerRequest) (string, error) {
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, p := range req.Ports {
		binding, err := nat.ParsePortSpec(p)
		if err != nil {
			return "", err
		}
		for _, b := range binding {
			exposedPorts[b.Port] = struct{}{}
			portBindings[b.Port] = append(portBindings[b.Port], b.Binding)
		}
	}

	hostCfg := &container.HostConfig{
		PortBindings: portBindings,
		Binds:        req.Volumes,
	}
	if req.NetworkMode != "" {
		hostCfg.NetworkMode = container.NetworkMode(req.NetworkMode)
	}
	if req.RestartPolicy != "" {
		hostCfg.RestartPolicy = container.RestartPolicy{Name: req.RestartPolicy}
	}

	cfg := &container.Config{
		Image:        req.Image,
		Env:          req.Env,
		Cmd:          req.Cmd,
		ExposedPorts: exposedPorts,
	}

	resp, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{}, nil, req.Name)
	if err != nil {
		return "", err
	}
	if err := m.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return resp.ID, err
	}
	return resp.ID, nil
}

// InspectContainer returns detailed information about a container.
func (m *Manager) InspectContainer(ctx context.Context, id string) (types.ContainerJSON, error) {
	return m.cli.ContainerInspect(ctx, id)
}

// ListVolumes returns all Docker volumes.
func (m *Manager) ListVolumes(ctx context.Context) ([]*volume.Volume, error) {
	resp, err := m.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

// ListNetworks returns all Docker networks.
func (m *Manager) ListNetworks(ctx context.Context) ([]types.NetworkResource, error) {
	return m.cli.NetworkList(ctx, types.NetworkListOptions{Filters: filters.NewArgs()})
}
