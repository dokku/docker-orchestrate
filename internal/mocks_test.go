package internal

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

type mockDockerClient struct {
	DockerClientInterface
	containerList      func(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	containerInspect   func(ctx context.Context, id string) (container.InspectResponse, error)
	containerStart     func(ctx context.Context, id string, options container.StartOptions) error
	containerTerminate func(ctx context.Context, id string) error
	containerRename    func(ctx context.Context, id, name string) error
	renamedContainers  map[string]string
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	if m.containerList != nil {
		return m.containerList(ctx, options)
	}
	return nil, nil
}

func (m *mockDockerClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.containerInspect != nil {
		return m.containerInspect(ctx, id)
	}
	return container.InspectResponse{}, nil
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, id string, options container.StartOptions) error {
	if m.containerStart != nil {
		return m.containerStart(ctx, id, options)
	}
	return nil
}

func (m *mockDockerClient) ContainerTerminate(ctx context.Context, id string) error {
	if m.containerTerminate != nil {
		return m.containerTerminate(ctx, id)
	}
	return nil
}

func (m *mockDockerClient) ContainerRename(ctx context.Context, id, name string) error {
	if m.containerRename != nil {
		return m.containerRename(ctx, id, name)
	}
	if m.renamedContainers == nil {
		m.renamedContainers = make(map[string]string)
	}
	m.renamedContainers[id] = name
	return nil
}

func (m *mockDockerClient) Close() error {
	return nil
}
