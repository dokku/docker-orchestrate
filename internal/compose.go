package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	mcli "github.com/mitchellh/cli"
	"golang.org/x/sync/errgroup"
)

// ComposeFile gets the compose file from the current directory or the
// specified directory
func ComposeFile() (string, error) {
	composeFile := ""
	if _, err := os.Stat("docker-compose.yaml"); err == nil {
		composeFile = "docker-compose.yaml"
		composeFile, err = filepath.Abs(composeFile)
		if err != nil {
			return "", fmt.Errorf("error expanding path: %v", err)
		}

		return composeFile, nil
	} else if _, err := os.Stat("docker-compose.yml"); err == nil {
		composeFile = "docker-compose.yml"
		composeFile, err = filepath.Abs(composeFile)
		if err != nil {
			return "", fmt.Errorf("error expanding path: %v", err)
		}

		return composeFile, nil
	}

	return "", errors.New("no compose file found")
}

// ComposeProject reads the compose file specified by the filename
// and returns the compose types.Project
func ComposeProject(filename string) (*types.Project, error) {
	ctx := context.Background()

	options, err := cli.NewProjectOptions(
		[]string{filename},
		cli.WithOsEnv,
		cli.WithDotEnv,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating project options: %v", err)
	}

	project, err := options.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("error loading project: %v", err)
	}

	return project, nil
}

// ComposeContainersInput is the input for the ComposeContainers function
type ComposeContainersInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// Status is the status of the containers to return
	Status string
}

// composeContainers returns detailed information about containers
func composeContainers(input ComposeContainersInput) ([]container.Summary, error) {
	if input.Client == nil {
		return nil, fmt.Errorf("client is required")
	}

	ctx := context.Background()

	// Build filters for container labels
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("com.docker.compose.project=%s", input.ProjectName))
	if input.ServiceName != "" {
		filterArgs.Add("label", fmt.Sprintf("com.docker.compose.service=%s", input.ServiceName))
	}

	if input.Status != "" {
		filterArgs.Add("status", input.Status)
	}

	// List containers with filters
	return input.Client.ContainerList(ctx, container.ListOptions{
		Filters: filterArgs,
		All:     true,
	})
}

// RollingUpdateInput contains the parameters for rolling update
type RollingUpdateInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ComposeFile is the path to the compose file
	ComposeFile string
	// ContainersToUpdate is the list of containers to update
	ContainersToUpdate []container.Summary
	// CurrentReplicas is the current number of replicas
	CurrentReplicas int
	// Delay is the delay between batches
	Delay time.Duration
	// DesiredReplicas is the target number of replicas
	DesiredReplicas int
	// Executor is the command executor to use. If nil, ExecCommand will be used.
	Executor CommandExecutor
	// FailureAction is the action to take on failure (pause or empty)
	FailureAction string
	// HealthcheckCommand is the command to run for health checks
	HealthcheckCommand string
	// Logger is the logger to use
	Logger mcli.Ui
	// MaxFailureRatio is the maximum allowed failure ratio
	MaxFailureRatio float32
	// Monitor is the health check monitoring duration
	Monitor time.Duration
	// Order is the update order strategy (start-first or stop-first)
	Order string
	// Parallelism is the number of containers to update simultaneously
	Parallelism int
	// ProjectDir is the project directory
	ProjectDir string
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// Sleeper is the function to use for sleeping. If nil, time.Sleep will be used.
	Sleeper func(time.Duration)
	// PreStopCommand is the command to run before stopping a container
	PreStopCommand string
	// PostStopCommand is the command to run after stopping a container
	PostStopCommand string
	// TickerCh is an optional channel to use for ticking. If nil, time.NewTicker will be used.
	TickerCh <-chan time.Time
}

// RollingUpdateOutput is the output of the rollingUpdateContainers function
type RollingUpdateOutput struct {
	// Failures is the number of failures
	Failures int
	// TotalUpdates is the total number of updates
	TotalUpdates int
}

// rollingUpdateContainers performs a rolling update on a list of containers
func rollingUpdateContainers(ctx context.Context, input RollingUpdateInput) (RollingUpdateOutput, error) {
	input.Logger.Output(fmt.Sprintf("Starting rolling update existing containers: current-replicas=%d, delay=%v, parallelism=%d, order=%s, target-replicas=%d", input.CurrentReplicas, input.Delay, input.Parallelism, input.Order, input.DesiredReplicas))

	if input.Executor == nil {
		input.Executor = ExecCommand
	}

	if input.Sleeper == nil {
		input.Sleeper = time.Sleep
	}

	output := RollingUpdateOutput{
		Failures:     0,
		TotalUpdates: 0,
	}

	// Process containers in batches based on parallelism
	for i := 0; i < len(input.ContainersToUpdate); i += input.Parallelism {
		batchSize := input.Parallelism
		if i+batchSize > len(input.ContainersToUpdate) {
			batchSize = len(input.ContainersToUpdate) - i
		}

		batch := input.ContainersToUpdate[i : i+batchSize]

		if input.Order == "start-first" {
			if err := rollingUpdateBatchStartFirst(ctx, input, batch, &output); err != nil {
				return output, err
			}
		} else {
			if err := rollingUpdateBatchStopFirst(ctx, input, batch, &output); err != nil {
				return output, err
			}
		}

		// Wait for delay between batches (except for the last batch)
		if i+batchSize < len(input.ContainersToUpdate) && input.Delay > 0 {
			input.Logger.Output(fmt.Sprintf("Waiting before next batch: %v", input.Delay))
			input.Sleeper(input.Delay)
		}
	}

	return output, nil
}

// rollingUpdateBatchStartFirst starts the new containers first
func rollingUpdateBatchStartFirst(ctx context.Context, input RollingUpdateInput, batch []container.Summary, output *RollingUpdateOutput) error {
	// Get currently running containers to determine current scale
	currentContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting current containers: %v", err)
	}

	// Start new containers
	newScale := len(currentContainers) + len(batch)
	_, err = input.Executor(ctx, ExecCommandInput{
		Command: "docker",
		Args: []string{
			"compose",
			"-f", input.ComposeFile,
			"-p", input.ProjectName,
			"up",
			"--detach",
			"--scale", fmt.Sprintf("%s=%d", input.ServiceName, newScale),
			"--no-deps",
			"--no-recreate",
			input.ServiceName,
		},
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		return fmt.Errorf("error creating new containers: %v", err)
	}

	// Get all containers to find the new ones
	allContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting containers after scale up: %v", err)
	}

	// Find the newest containers (those not in the current list)
	newContainers := make([]container.Summary, 0)
	for _, c := range allContainers {
		isExisting := false
		for _, existing := range currentContainers {
			if c.ID == existing.ID {
				isExisting = true
				break
			}
		}
		if !isExisting {
			newContainers = append(newContainers, c)
		}
	}

	// If we have more new containers than expected, take the newest ones
	if len(newContainers) > len(batch) {
		// Sort by creation time (newest first)
		sortContainersByCreationTime(newContainers, true)
		newContainers = newContainers[:len(batch)]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var batchErr error

	// Use a channel to distribute old containers to stop
	oldContainersToStop := make(chan container.Summary, len(batch))
	for _, c := range batch {
		oldContainersToStop <- c
	}
	close(oldContainersToStop)

	for _, nc := range newContainers {
		wg.Add(1)
		go func(newContainer container.Summary) {
			defer wg.Done()

			mu.Lock()
			output.TotalUpdates++
			mu.Unlock()

			input.Logger.Output(fmt.Sprintf("Waiting for container to become healthy: %s", newContainer.ID[:12]))
			healthcheckInput := WaitForHealthcheckInput{
				Client:             input.Client,
				ContainerID:        newContainer.ID,
				Executor:           input.Executor,
				HealthcheckCommand: input.HealthcheckCommand,
				Monitor:            input.Monitor,
				ServiceName:        input.ServiceName,
				TickerCh:           input.TickerCh,
			}

			if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
				input.Logger.Output(fmt.Sprintf("Container %s failed health check, stopping", newContainer.ID[:12]))
				if eo, ok := err.(*ErrorWithOutput); ok {
					lines := strings.Split(eo.Output, "\n")
					for _, line := range lines {
						input.Logger.Output(fmt.Sprintf("    %s", line))
					}
				}

				mu.Lock()
				output.Failures++
				mu.Unlock()

				// Clean up failed container
				_ = runPreStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PreStopCommand,
				})
				_ = input.Client.ContainerTerminate(ctx, newContainer.ID)
				_ = runPostStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PostStopCommand,
				})

				// We don't return error here because we want to continue with others in batch
				// but we check failure ratio later
				return
			}

			// Pop an old container to stop
			oldContainer, ok := <-oldContainersToStop
			if ok {
				// get old container name
				oldContainerIdentifier := oldContainer.ID[:12]
				for _, name := range oldContainer.Names {
					if n, found := strings.CutPrefix(name, "/"); found {
						oldContainerIdentifier = n
						break
					}
				}

				input.Logger.Output(fmt.Sprintf("Container %s is healthy, stopping %s", newContainer.ID[:12], oldContainerIdentifier))
				_ = runPreStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: oldContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PreStopCommand,
				})
				if err := input.Client.ContainerTerminate(ctx, oldContainer.ID); err != nil {
					input.Logger.Output(fmt.Sprintf("Error stopping old container %s: %v", oldContainerIdentifier, err))
				}
				_ = runPostStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: oldContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PostStopCommand,
				})
			} else {
				input.Logger.Output(fmt.Sprintf("Container %s is healthy", newContainer.ID[:12]))
			}
		}(nc)
	}

	wg.Wait()

	// Check failure ratio after batch completes
	failureRatio := float64(output.Failures) / float64(output.TotalUpdates)
	maxFailureRatioFloat := float64(input.MaxFailureRatio)
	if maxFailureRatioFloat > 0 && failureRatio > maxFailureRatioFloat {
		if input.FailureAction == "pause" {
			return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), pausing deployment", failureRatio, maxFailureRatioFloat)
		}
		return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f)", failureRatio, maxFailureRatioFloat)
	}

	if input.FailureAction == "pause" && output.Failures > 0 {
		return fmt.Errorf("deployment paused due to failure (failure_action: pause)")
	}

	return batchErr
}

// rollingUpdateBatchStopFirst stops the old containers first
func rollingUpdateBatchStopFirst(ctx context.Context, input RollingUpdateInput, batch []container.Summary, output *RollingUpdateOutput) error {
	input.Logger.Output(fmt.Sprintf("Stopping %d old containers first", len(batch)))

	g, stopCtx := errgroup.WithContext(ctx)
	for _, c := range batch {
		containerID := c.ID
		containerIdentifier := containerID[:12]
		for _, name := range c.Names {
			if n, found := strings.CutPrefix(name, "/"); found {
				containerIdentifier = n
				break
			}
		}
		g.Go(func() error {
			input.Logger.Output(fmt.Sprintf("Stopping container %s", containerIdentifier))
			_ = runPreStopCommand(stopCtx, RunStopCommandInput{
				Client:      input.Client,
				ContainerID: containerID,
				Executor:    input.Executor,
				ServiceName: input.ServiceName,
				Script:      input.PreStopCommand,
			})
			err := input.Client.ContainerTerminate(stopCtx, containerID)
			_ = runPostStopCommand(stopCtx, RunStopCommandInput{
				Client:      input.Client,
				ContainerID: containerID,
				Executor:    input.Executor,
				ServiceName: input.ServiceName,
				Script:      input.PostStopCommand,
			})
			return err
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("error stopping containers in batch: %v", err)
	}

	// Current scale after stopping containers is (total - stopped)
	// We want to scale back up to target replicas (or current replicas if we are in middle of update)
	// Actually, we should scale up to whatever the count was before we stopped these
	currentContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting current containers: %v", err)
	}

	targetScale := len(currentContainers) + len(batch)
	_, err = input.Executor(ctx, ExecCommandInput{
		Command: "docker",
		Args: []string{
			"compose",
			"-f", input.ComposeFile,
			"-p", input.ProjectName,
			"up",
			"--detach",
			"--scale", fmt.Sprintf("%s=%d", input.ServiceName, targetScale),
			"--no-deps",
			"--no-recreate",
			input.ServiceName,
		},
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		return fmt.Errorf("error starting new containers: %v", err)
	}

	// Get all containers to find the new ones
	allContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting containers after scale up: %v", err)
	}

	// Find the new containers (those in 'created' or 'running' status but not the ones we already had)
	newContainers := make([]container.Summary, 0)
	for _, c := range allContainers {
		isExisting := false
		for _, existing := range currentContainers {
			if c.ID == existing.ID {
				isExisting = true
				break
			}
		}
		if !isExisting {
			newContainers = append(newContainers, c)
		}
	}

	if len(newContainers) > len(batch) {
		sortContainersByCreationTime(newContainers, true)
		newContainers = newContainers[:len(batch)]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, nc := range newContainers {
		wg.Add(1)
		go func(newContainer container.Summary) {
			defer wg.Done()

			mu.Lock()
			output.TotalUpdates++
			mu.Unlock()

			input.Logger.Output(fmt.Sprintf("Waiting for container to become healthy: %s", newContainer.ID[:12]))
			healthcheckInput := WaitForHealthcheckInput{
				Client:             input.Client,
				ContainerID:        newContainer.ID,
				Executor:           input.Executor,
				HealthcheckCommand: input.HealthcheckCommand,
				Monitor:            input.Monitor,
				ServiceName:        input.ServiceName,
				TickerCh:           input.TickerCh,
			}

			if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
				input.Logger.Output(fmt.Sprintf("Container %s failed health check, stopping: %v", newContainer.ID[:12], err))
				if eo, ok := err.(*ErrorWithOutput); ok {
					lines := strings.Split(eo.Output, "\n")
					for _, line := range lines {
						input.Logger.Output(fmt.Sprintf("    %s", line))
					}
				}

				mu.Lock()
				output.Failures++
				mu.Unlock()

				_ = runPreStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PreStopCommand,
				})
				_ = input.Client.ContainerTerminate(ctx, newContainer.ID)
				_ = runPostStopCommand(ctx, RunStopCommandInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Executor:    input.Executor,
					ServiceName: input.ServiceName,
					Script:      input.PostStopCommand,
				})
				return
			}
			input.Logger.Output(fmt.Sprintf("Container %s is healthy", newContainer.ID[:12]))
		}(nc)
	}

	wg.Wait()

	// Check failure ratio
	failureRatio := float64(output.Failures) / float64(output.TotalUpdates)
	maxFailureRatioFloat := float64(input.MaxFailureRatio)
	if maxFailureRatioFloat > 0 && failureRatio > maxFailureRatioFloat {
		if input.FailureAction == "pause" {
			return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), pausing deployment", failureRatio, maxFailureRatioFloat)
		}
		return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f)", failureRatio, maxFailureRatioFloat)
	}

	if input.FailureAction == "pause" && output.Failures > 0 {
		return fmt.Errorf("deployment paused due to failure (failure_action: pause)")
	}

	return nil
}

type ScaleDownContainersInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ComposeFile is the path to the compose file
	ComposeFile string
	// CurrentContainers is the current list of containers
	CurrentContainers []container.Summary
	// CurrentReplicas is the current number of containers
	CurrentReplicas int
	// DesiredReplicas is the target number of replicas
	DesiredReplicas int
	// Executor is the command executor to use. If nil, ExecCommand will be used.
	Executor CommandExecutor
	// Logger is the logger to use
	Logger mcli.Ui
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// PreStopCommand is the command to run before stopping a container
	PreStopCommand string
	// PostStopCommand is the command to run after stopping a container
	PostStopCommand string
}

// scaleDownContainers scales down containers by stopping and removing excess ones
// It always removes the oldest containers first
func scaleDownContainers(ctx context.Context, input ScaleDownContainersInput) error {
	input.Logger.Output(fmt.Sprintf("Scaling down containers: current-replicas=%d, target-replicas=%d", input.CurrentReplicas, input.DesiredReplicas))
	toRemove := input.CurrentReplicas - input.DesiredReplicas

	if toRemove <= 0 {
		return nil
	}

	// Sort containers by creation time to ensure we remove the oldest ones
	sortContainersByCreationTime(input.CurrentContainers, false)

	// Remove the oldest containers (first toRemove containers)
	containersToRemove := input.CurrentContainers[:toRemove]
	for _, container := range containersToRemove {
		containerIdentifier := container.ID[:12]
		for _, name := range container.Names {
			if n, found := strings.CutPrefix(name, "/"); found {
				containerIdentifier = n
				break
			}
		}
		input.Logger.Output(fmt.Sprintf("Stopping container %s", containerIdentifier))

		executor := input.Executor
		if executor == nil {
			executor = ExecCommand
		}

		_ = runPreStopCommand(ctx, RunStopCommandInput{
			Client:      input.Client,
			ContainerID: container.ID,
			Executor:    executor,
			ServiceName: input.ServiceName,
			Script:      input.PreStopCommand,
		})
		if err := input.Client.ContainerTerminate(ctx, container.ID); err != nil {
			return fmt.Errorf("error scaling down: %v", err)
		}
		_ = runPostStopCommand(ctx, RunStopCommandInput{
			Client:      input.Client,
			ContainerID: container.ID,
			Executor:    executor,
			ServiceName: input.ServiceName,
			Script:      input.PostStopCommand,
		})
	}

	return nil
}

// ScaleUpContainersInput is the input for the scaleUpContainers function
type ScaleUpContainersInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ComposeFile is the path to the compose file
	ComposeFile string
	// CurrentReplicas is the current number of containers
	CurrentReplicas int
	// Delay is the delay between batches
	Delay time.Duration
	// DesiredReplicas is the target number of replicas
	DesiredReplicas int
	// Executor is the command executor to use. If nil, ExecCommand will be used.
	Executor CommandExecutor
	// ExistingContainers is the list of existing containers to skip
	ExistingContainers []container.Summary
	// FailureAction is the action to take on failure (pause or empty)
	FailureAction string
	// HealthcheckCommand is the command to run for health checks
	HealthcheckCommand string
	// Logger is the logger to use
	Logger mcli.Ui
	// MaxFailureRatio is the maximum allowed failure ratio
	MaxFailureRatio float32
	// Monitor is the health check monitoring duration
	Monitor time.Duration
	// Parallelism is the number of containers to update simultaneously
	Parallelism int
	// ProjectDir is the project directory
	ProjectDir string
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// PreStopCommand is the command to run before stopping a container
	PreStopCommand string
	// PostStopCommand is the command to run after stopping a container
	PostStopCommand string
	// TickerCh is an optional channel to use for ticking. If nil, time.NewTicker will be used.
	TickerCh <-chan time.Time
}

// scaleUpContainers scales up containers by creating and starting new ones
func scaleUpContainers(ctx context.Context, input ScaleUpContainersInput) error {
	input.Logger.Output(fmt.Sprintf("Scaling up containers: current-replicas=%d, parallelism=%d, target-replicas=%d", input.CurrentReplicas, input.Parallelism, input.DesiredReplicas))

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Create all containers at once
	_, err := executor(ctx, ExecCommandInput{
		Command: "docker",
		Args: []string{
			"compose",
			"-f", input.ComposeFile,
			"-p", input.ProjectName,
			"create",
			"--scale", fmt.Sprintf("%s=%d", input.ServiceName, input.DesiredReplicas),
			input.ServiceName,
		},
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		return fmt.Errorf("error creating containers: %v", err)
	}

	// Get all created containers (including existing running ones)
	allContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting created containers: %v", err)
	}

	// Filter to only get created (not running) containers
	createdContainers := make([]container.Summary, 0)
	for _, c := range allContainers {
		// only include the containers from allContainers if they are not in the existingContainers slice
		if slices.ContainsFunc(input.ExistingContainers, func(container container.Summary) bool {
			return container.ID == c.ID
		}) {
			continue
		}
		createdContainers = append(createdContainers, c)
	}

	if len(createdContainers) == 0 {
		input.Logger.Output("No created containers to start")
		return nil
	}

	totalUpdates := 0
	failures := 0

	// Start containers in batches according to parallelism
	for i := 0; i < len(createdContainers); i += input.Parallelism {
		batchSize := input.Parallelism
		if i+batchSize > len(createdContainers) {
			batchSize = len(createdContainers) - i
		}

		batch := createdContainers[i : i+batchSize]

		var wg sync.WaitGroup
		var mu sync.Mutex
		var batchErr error

		// Start containers in this batch
		for _, c := range batch {
			wg.Add(1)
			go func(c container.Summary) {
				defer wg.Done()

				mu.Lock()
				totalUpdates++
				mu.Unlock()

				if err := input.Client.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
					input.Logger.Output(fmt.Sprintf("Error starting container %s: %v", c.ID[:12], err))
					mu.Lock()
					failures++
					if batchErr == nil {
						batchErr = fmt.Errorf("error starting container %s: %v", c.ID[:12], err)
					}
					mu.Unlock()
					return
				}

				// Wait for health check
				input.Logger.Output(fmt.Sprintf("Waiting for container to become healthy: %s", c.ID[:12]))
				healthcheckInput := WaitForHealthcheckInput{
					Client:             input.Client,
					ContainerID:        c.ID,
					Executor:           executor,
					HealthcheckCommand: input.HealthcheckCommand,
					Monitor:            input.Monitor,
					ServiceName:        input.ServiceName,
					TickerCh:           input.TickerCh,
				}

				if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
					input.Logger.Output(fmt.Sprintf("Container %s failed health check: %v", c.ID[:12], err))
					if eo, ok := err.(*ErrorWithOutput); ok {
						lines := strings.Split(eo.Output, "\n")
						for _, line := range lines {
							input.Logger.Output(fmt.Sprintf("    %s", line))
						}
					}

					mu.Lock()
					failures++
					if batchErr == nil {
						batchErr = fmt.Errorf("container %s failed health check: %v", c.ID[:12], err)
					}
					mu.Unlock()

					_ = runPreStopCommand(ctx, RunStopCommandInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Executor:    executor,
						ServiceName: input.ServiceName,
						Script:      input.PreStopCommand,
					})
					_ = input.Client.ContainerTerminate(ctx, c.ID)
					_ = runPostStopCommand(ctx, RunStopCommandInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Executor:    executor,
						ServiceName: input.ServiceName,
						Script:      input.PostStopCommand,
					})
				}
			}(c)
		}
		wg.Wait()

		// Check failure ratio after batch completes
		failureRatio := float64(failures) / float64(totalUpdates)
		maxFailureRatioFloat := float64(input.MaxFailureRatio)
		if maxFailureRatioFloat > 0 && failureRatio > maxFailureRatioFloat {
			if input.FailureAction == "pause" {
				return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), pausing deployment", failureRatio, maxFailureRatioFloat)
			}
			return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f)", failureRatio, maxFailureRatioFloat)
		}

		if input.FailureAction == "pause" && failures > 0 {
			return fmt.Errorf("deployment paused due to failure (failure_action: pause)")
		}

		if batchErr != nil && input.MaxFailureRatio == 0 {
			return batchErr
		}

		// Wait for delay between batches (except for the last batch)
		if i+batchSize < len(createdContainers) && input.Delay > 0 {
			input.Logger.Output(fmt.Sprintf("Waiting before next batch: %v", input.Delay))
			time.Sleep(input.Delay)
		}
	}

	return nil
}

// sortContainersByCreationTime sorts containers by creation time
func sortContainersByCreationTime(containers []container.Summary, newestFirst bool) {
	slices.SortFunc(containers, func(a, b container.Summary) int {
		if newestFirst {
			if a.Created > b.Created {
				return -1
			}
			if a.Created < b.Created {
				return 1
			}
		} else {
			if a.Created < b.Created {
				return -1
			}
			if a.Created > b.Created {
				return 1
			}
		}
		return 0
	})
}

// ContainerNameTemplateData is the data structure for container name templates
type ContainerNameTemplateData struct {
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// InstanceID is the instance ID
	InstanceID int
}

type RenameContainersToConventionInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// Containers is the list of containers to rename
	Containers []container.Summary
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// NameTemplate is the Go template for container names
	NameTemplate string
}

// renameContainersToConvention renames all containers to follow the naming convention
// using the provided Go template. The template has access to .ProjectName, .ServiceName, and .InstanceID
func renameContainersToConvention(ctx context.Context, input RenameContainersToConventionInput) error {
	if len(input.Containers) == 0 {
		return nil
	}

	// Parse the template
	tmpl, err := template.New("container-name").Parse(input.NameTemplate)
	if err != nil {
		return fmt.Errorf("error parsing container name template: %v", err)
	}

	sortContainersByCreationTime(input.Containers, false)

	// Rename each container with instance ID starting from 1
	for i, c := range input.Containers {
		instanceID := i + 1

		// Execute the template
		var buf bytes.Buffer
		data := ContainerNameTemplateData{
			ProjectName: input.ProjectName,
			ServiceName: input.ServiceName,
			InstanceID:  instanceID,
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("error executing container name template: %v", err)
		}
		newName := buf.String()

		// Get current container name to check if rename is needed
		currentName := ""
		if len(c.Names) > 0 {
			currentName = strings.TrimPrefix(c.Names[0], "/")
		}

		if currentName != newName {
			if err := input.Client.ContainerRename(ctx, c.ID, newName); err != nil {
				return fmt.Errorf("error renaming container %s to %s: %v", c.ID[:12], newName, err)
			}
		}
	}

	return nil
}
