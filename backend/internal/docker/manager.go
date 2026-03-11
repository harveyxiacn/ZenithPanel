package docker

import (
	"context"

	"github.com/docker/docker/api/types"
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
