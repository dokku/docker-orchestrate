package internal

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	dockerClient "github.com/docker/docker/client"
)

// DockerClientInterface is an interface for the Docker client
type DockerClientInterface interface {
	Close() error
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerRename(ctx context.Context, containerID, newName string) error
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerTerminate(ctx context.Context, containerID string) error
}

// DockerClient is a wrapper around the Docker client
type DockerClient struct {
	// cli is the Docker client
	cli *dockerClient.Client
}

// NewDockerClient returns a new Docker client instance
func NewDockerClient() (DockerClientInterface, error) {
	cli, err := dockerClient.NewClientWithOpts(
		dockerClient.FromEnv,
		dockerClient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating Docker client: %v", err)
	}
	return &DockerClient{cli: cli}, nil
}

// Close closes the Docker client
func (d *DockerClient) Close() error {
	return d.cli.Close()
}

// ContainerList lists containers
func (d *DockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return d.cli.ContainerList(ctx, options)
}

// ContainerInspect inspects a container
func (d *DockerClient) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	return d.cli.ContainerInspect(ctx, containerID)
}

// ContainerStop stops a container
func (d *DockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	return d.cli.ContainerStop(ctx, containerID, options)
}

// ContainerRemove removes a container
func (d *DockerClient) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	return d.cli.ContainerRemove(ctx, containerID, options)
}

// ContainerRename renames a container
func (d *DockerClient) ContainerRename(ctx context.Context, containerID, newName string) error {
	return d.cli.ContainerRename(ctx, containerID, newName)
}

// ContainerStart starts a container
func (d *DockerClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	return d.cli.ContainerStart(ctx, containerID, options)
}

// ContainerTerminate terminates a container
func (d *DockerClient) ContainerTerminate(ctx context.Context, containerID string) error {
	timeoutSeconds := 10
	if err := d.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeoutSeconds}); err != nil {
		return fmt.Errorf("error stopping container: %v", err)
	}

	if err := d.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("error removing container: %v", err)
	}

	return nil
}
