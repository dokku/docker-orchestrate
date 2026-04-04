package internal

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

type mockDockerClient struct {
	DockerClientInterface
	containerList        func(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	containerInspect     func(ctx context.Context, id string) (container.InspectResponse, error)
	containerStart       func(ctx context.Context, id string, options container.StartOptions) error
	containerTerminate   func(ctx context.Context, id string) error
	containerRename      func(ctx context.Context, id, name string) error
	containerExecCreate  func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error)
	containerExecStart   func(ctx context.Context, execID string, config container.ExecStartOptions) error
	containerExecAttach  func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
	containerExecInspect func(ctx context.Context, execID string) (container.ExecInspect, error)
	copyToContainer      func(ctx context.Context, containerID, dstPath string, content io.Reader, options container.CopyToContainerOptions) error
	imageInspect         func(ctx context.Context, imageID string) (image.InspectResponse, error)
	renamedContainers    map[string]string
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

func (m *mockDockerClient) ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
	if m.containerExecCreate != nil {
		return m.containerExecCreate(ctx, containerID, config)
	}
	return container.ExecCreateResponse{}, nil
}

func (m *mockDockerClient) ContainerExecStart(ctx context.Context, execID string, config container.ExecStartOptions) error {
	if m.containerExecStart != nil {
		return m.containerExecStart(ctx, execID, config)
	}
	return nil
}

func (m *mockDockerClient) ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
	if m.containerExecAttach != nil {
		return m.containerExecAttach(ctx, execID, config)
	}
	return types.HijackedResponse{}, nil
}

func (m *mockDockerClient) ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error) {
	if m.containerExecInspect != nil {
		return m.containerExecInspect(ctx, execID)
	}
	return container.ExecInspect{}, nil
}

func (m *mockDockerClient) ImageInspect(ctx context.Context, imageID string) (image.InspectResponse, error) {
	if m.imageInspect != nil {
		return m.imageInspect(ctx, imageID)
	}
	return image.InspectResponse{}, nil
}

func (m *mockDockerClient) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader, options container.CopyToContainerOptions) error {
	if m.copyToContainer != nil {
		return m.copyToContainer(ctx, containerID, dstPath, content, options)
	}
	return nil
}
