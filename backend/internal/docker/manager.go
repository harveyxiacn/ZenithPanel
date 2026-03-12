package docker

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Manager wraps the docker client
type Manager struct {
	cli *client.Client
}

// NewManager creates a new Docker client manager
func NewManager() (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Manager{cli: cli}, nil
}

// ListContainers returns a list of running/stopped containers
func (m *Manager) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	containers, err := m.cli.ContainerList(ctx, types.ContainerListOptions{All: all})
	if err != nil {
		return nil, err
	}
	return containers, nil
}

// StartContainer starts a stopped container
func (m *Manager) StartContainer(ctx context.Context, id string) error {
	return m.cli.ContainerStart(ctx, id, types.ContainerStartOptions{})
}

// StopContainer stops a running container with a 10-second timeout
func (m *Manager) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	return m.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RestartContainer restarts a container with a 10-second timeout
func (m *Manager) RestartContainer(ctx context.Context, id string) error {
	timeout := 10
	return m.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RemoveContainer removes a container, optionally with force
func (m *Manager) RemoveContainer(ctx context.Context, id string, force bool) error {
	return m.cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{Force: force})
}
