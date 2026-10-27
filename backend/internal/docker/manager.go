package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/netip"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
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
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Manager{cli: cli}, nil
}

// ListContainers returns a list of containers (all=true includes stopped).
func (m *Manager) ListContainers(ctx context.Context, all bool) ([]container.Summary, error) {
	result, err := m.cli.ContainerList(ctx, client.ContainerListOptions{All: all})
	return result.Items, err
}

// StartContainer starts a stopped container.
func (m *Manager) StartContainer(ctx context.Context, id string) error {
	_, err := m.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// StopContainer stops a running container with a 10-second timeout.
func (m *Manager) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	_, err := m.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	return err
}

// RestartContainer restarts a container with a 10-second timeout.
func (m *Manager) RestartContainer(ctx context.Context, id string) error {
	timeout := 10
	_, err := m.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &timeout})
	return err
}

// RemoveContainer removes a container, optionally with force.
func (m *Manager) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := m.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: force})
	return err
}

// ListImages returns all local images.
func (m *Manager) ListImages(ctx context.Context) ([]image.Summary, error) {
	result, err := m.cli.ImageList(ctx, client.ImageListOptions{})
	return result.Items, err
}

// PullImage pulls an image from a registry. Returns a ReadCloser that streams
// JSON progress events; the caller must drain and close it.
func (m *Manager) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return m.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
}

// RemoveImage removes an image by ID or reference.
func (m *Manager) RemoveImage(ctx context.Context, id string, force bool) ([]image.DeleteResponse, error) {
	result, err := m.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: force, PruneChildren: true})
	return result.Items, err
}

// GetContainerLogs returns the last `tail` lines from a container's stdout+stderr.
// Pass tail="all" for all lines.
func (m *Manager) GetContainerLogs(ctx context.Context, id string, tail string) (string, error) {
	rc, err := m.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
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
	resp, err := m.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw container.StatsResponse
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
	exposedPorts := network.PortSet{}
	portBindings := network.PortMap{}
	for _, p := range req.Ports {
		binding, err := nat.ParsePortSpec(p)
		if err != nil {
			return "", err
		}
		for _, b := range binding {
			port, err := network.ParsePort(string(b.Port))
			if err != nil {
				return "", err
			}
			hostIP := netip.Addr{}
			if b.Binding.HostIP != "" {
				hostIP, err = netip.ParseAddr(b.Binding.HostIP)
				if err != nil {
					return "", err
				}
			}
			exposedPorts[port] = struct{}{}
			portBindings[port] = append(portBindings[port], network.PortBinding{HostIP: hostIP, HostPort: b.Binding.HostPort})
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
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(req.RestartPolicy)}
	}

	cfg := &container.Config{
		Image:        req.Image,
		Env:          req.Env,
		Cmd:          req.Cmd,
		ExposedPorts: exposedPorts,
	}

	resp, err := m.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       hostCfg,
		NetworkingConfig: &network.NetworkingConfig{},
		Name:             req.Name,
	})
	if err != nil {
		return "", err
	}
	if _, err := m.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return resp.ID, err
	}
	return resp.ID, nil
}

// InspectContainer returns detailed information about a container.
func (m *Manager) InspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	result, err := m.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	return result.Container, err
}

// ListVolumes returns all Docker volumes.
func (m *Manager) ListVolumes(ctx context.Context) ([]volume.Volume, error) {
	resp, err := m.cli.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// ListNetworks returns all Docker networks.
func (m *Manager) ListNetworks(ctx context.Context) ([]network.Summary, error) {
	result, err := m.cli.NetworkList(ctx, client.NetworkListOptions{})
	return result.Items, err
}
