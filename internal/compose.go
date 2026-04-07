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
	"sync/atomic"
	"text/template"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/josegonzalez/cli-skeleton/command"
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

// ComposeProject reads the compose files specified by the filenames
// and returns the compose types.Project
func ComposeProject(ctx context.Context, projectName string, filenames []string, profiles []string, envFiles []string) (*types.Project, error) {
	opts := []cli.ProjectOptionsFn{
		cli.WithOsEnv,
		cli.WithEnvFiles(envFiles...),
		cli.WithDotEnv,
		cli.WithDefaultProfiles(profiles...),
		cli.WithName(projectName),
	}

	options, err := cli.NewProjectOptions(filenames, opts...)
	if err != nil {
		return nil, fmt.Errorf("error creating project options: %v", err)
	}

	project, err := options.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("error loading project: %v", err)
	}

	return project, nil
}

// ComposeFileArgs returns the command-line arguments for specifying
// one or more compose files to docker compose (e.g., ["-f", "a.yml", "-f", "b.yml"]).
func ComposeFileArgs(files []string) []string {
	args := make([]string, 0, len(files)*2)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	return args
}

// EnvFileArgs returns the command-line arguments for specifying
// one or more env files to docker compose (e.g., ["--env-file", "a.env", "--env-file", "b.env"]).
func EnvFileArgs(files []string) []string {
	args := make([]string, 0, len(files)*2)
	for _, f := range files {
		args = append(args, "--env-file", f)
	}
	return args
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
func composeContainers(ctx context.Context, input ComposeContainersInput) ([]container.Summary, error) {
	if input.Client == nil {
		return nil, fmt.Errorf("client is required")
	}

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
	// Build is whether to build images before deploying
	Build bool
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// ContainersToUpdate is the list of containers to update
	ContainersToUpdate []container.Summary
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables from env files
	EnvVars map[string]string
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
	// HealthcheckWait is the duration to wait before considering a container without a Docker healthcheck as healthy
	HealthcheckWait time.Duration
	// WaitAfterHealthy is the duration to wait after a container becomes healthy before proceeding
	WaitAfterHealthy time.Duration
	// Logger is the logger to use
	Logger *command.ZerologUi
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
	// PullPolicy is the pull policy to pass to docker compose (always, missing, never)
	PullPolicy string
	// RollbackConfig is the optional rollback configuration. Used when FailureAction is "rollback".
	RollbackConfig *types.UpdateConfig
	// ServiceName is the name of the service
	ServiceName string
	// Sleeper is the function to use for sleeping. If nil, time.Sleep will be used.
	Sleeper func(time.Duration)
	// PreStopHostCommand is the command to run before stopping a container (on host)
	PreStopHostCommand string
	// PreStopHostCommandDetached indicates if the pre-stop host command should run detached
	PreStopHostCommandDetached bool
	// PreStopCommand is the command to run inside the container before stopping
	PreStopCommand string
	// PreStopHooks are the compose spec pre_stop hooks to run inside the container before stopping
	PreStopHooks []types.ServiceHook
	// PostStopHostCommand is the command to run after stopping a container
	PostStopHostCommand string
	// PostStopHostCommandDetached indicates if the post-stop command should run detached
	PostStopHostCommandDetached bool
	// StopTimeout is the timeout in seconds for stopping containers (from stop_grace_period)
	StopTimeout int
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
	input.Logger.Info(fmt.Sprintf("Starting rolling update existing containers: current-replicas=%d, delay=%v, parallelism=%d, order=%s, target-replicas=%d", input.CurrentReplicas, input.Delay, input.Parallelism, input.Order, input.DesiredReplicas))

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
			input.Logger.Info(fmt.Sprintf("Waiting before next batch: %v", input.Delay))
			input.Sleeper(input.Delay)
		}
	}

	return output, nil
}

// rollingUpdateBatchStartFirst starts the new containers first
func rollingUpdateBatchStartFirst(ctx context.Context, input RollingUpdateInput, batch []container.Summary, output *RollingUpdateOutput) error {
	// Get currently running containers to determine current scale
	currentContainers, err := composeContainers(ctx, ComposeContainersInput{
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
	args := []string{"compose"}
	args = append(args, ComposeFileArgs(input.ComposeFiles)...)
	args = append(args, EnvFileArgs(input.EnvFiles)...)
	args = append(args, "-p", input.ProjectName, "up", "--detach")
	if input.Build {
		args = append(args, "--build")
	}
	args = append(args, "--pull", input.PullPolicy,
		"--scale", fmt.Sprintf("%s=%d", input.ServiceName, newScale),
		"--no-deps", "--no-recreate", input.ServiceName)
	_, err = input.Executor(ctx, ExecCommandInput{
		Command:          "docker",
		Args:             args,
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		return fmt.Errorf("error creating new containers: %v", err)
	}

	// Get all containers to find the new ones
	allContainers, err := composeContainers(ctx, ComposeContainersInput{
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

	if input.FailureAction == "rollback" {
		type containerPair struct {
			newContainer container.Summary
			oldContainer *container.Summary
			healthy      bool
		}

		var pairs []containerPair
		var oldIdx int64
		for _, nc := range newContainers {
			wg.Add(1)
			go func(newContainer container.Summary) {
				defer wg.Done()

				mu.Lock()
				output.TotalUpdates++
				mu.Unlock()

				input.Logger.Info(fmt.Sprintf("Waiting for container to become healthy: %s", newContainer.ID[:12]))
				healthcheckInput := WaitForHealthcheckInput{
					Client:             input.Client,
					ContainerID:        newContainer.ID,
					Executor:           input.Executor,
					HealthcheckCommand: input.HealthcheckCommand,
					HealthcheckWait:    input.HealthcheckWait,
					Monitor:            input.Monitor,
					ProjectName:        input.ProjectName,
					ServiceName:        input.ServiceName,
					TickerCh:           input.TickerCh,
				}

				if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
					input.Logger.Info(fmt.Sprintf("Container %s failed health check: %v", newContainer.ID[:12], err))
					if eo, ok := err.(*ErrorWithOutput); ok {
						lines := strings.Split(eo.Output, "\n")
						for _, line := range lines {
							input.Logger.Info(fmt.Sprintf("    %s", line))
						}
					}

					mu.Lock()
					output.Failures++
					pairs = append(pairs, containerPair{newContainer: newContainer, healthy: false})
					mu.Unlock()

					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: newContainer.ID,
						Detached:    input.PreStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    input.Executor,
						ProjectName: input.ProjectName,
						Script:      input.PreStopHostCommand,
						ScriptType:  "pre-stop",
						ServiceName: input.ServiceName,
					})
					if input.PreStopCommand != "" {
						_ = runContainerScript(ctx, RunContainerScriptInput{
							Client:      input.Client,
							ContainerID: newContainer.ID,
							Env:         input.EnvVars,
							Script:      input.PreStopCommand,
							ScriptPath:  "/tmp/pre-stop.sh",
							ServiceName: input.ServiceName,
						})
					}
					_ = runContainerHooks(ctx, RunContainerHooksInput{
						Client:      input.Client,
						ContainerID: newContainer.ID,
						Hooks:       input.PreStopHooks,
						ServiceName: input.ServiceName,
					})
					_ = input.Client.ContainerTerminate(ctx, newContainer.ID, input.StopTimeout)
					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: newContainer.ID,
						Detached:    input.PostStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    input.Executor,
						ProjectName: input.ProjectName,
						Script:      input.PostStopHostCommand,
						ScriptType:  "post-stop",
						ServiceName: input.ServiceName,
					})
					return
				}

				if input.WaitAfterHealthy > 0 {
					input.Logger.Info(fmt.Sprintf("Waiting after healthy: %v for container %s", input.WaitAfterHealthy, newContainer.ID[:12]))
					input.Sleeper(input.WaitAfterHealthy)
				}

				idx := int(atomic.AddInt64(&oldIdx, 1)) - 1
				mu.Lock()
				if idx < len(batch) {
					pairs = append(pairs, containerPair{newContainer: newContainer, oldContainer: &batch[idx], healthy: true})
				} else {
					pairs = append(pairs, containerPair{newContainer: newContainer, healthy: true})
				}
				mu.Unlock()

				input.Logger.Info(fmt.Sprintf("Container %s is healthy, deferring old container cleanup for rollback evaluation", newContainer.ID[:12]))
			}(nc)
		}

		wg.Wait()

		failureRatio := float64(output.Failures) / float64(output.TotalUpdates)
		maxFailureRatioFloat := float64(input.MaxFailureRatio)
		shouldRollback := output.Failures > 0
		if maxFailureRatioFloat > 0 && failureRatio <= maxFailureRatioFloat {
			shouldRollback = false
		}

		if shouldRollback {
			input.Logger.Info("Rolling back: terminating new containers and keeping old containers")
			for _, pair := range pairs {
				if pair.healthy {
					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: pair.newContainer.ID,
						Detached:    input.PreStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    input.Executor,
						ProjectName: input.ProjectName,
						Script:      input.PreStopHostCommand,
						ScriptType:  "pre-stop",
						ServiceName: input.ServiceName,
					})
					if input.PreStopCommand != "" {
						_ = runContainerScript(ctx, RunContainerScriptInput{
							Client:      input.Client,
							ContainerID: pair.newContainer.ID,
							Env:         input.EnvVars,
							Script:      input.PreStopCommand,
							ScriptPath:  "/tmp/pre-stop.sh",
							ServiceName: input.ServiceName,
						})
					}
					_ = runContainerHooks(ctx, RunContainerHooksInput{
						Client:      input.Client,
						ContainerID: pair.newContainer.ID,
						Hooks:       input.PreStopHooks,
						ServiceName: input.ServiceName,
					})
					_ = input.Client.ContainerTerminate(ctx, pair.newContainer.ID, input.StopTimeout)
					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: pair.newContainer.ID,
						Detached:    input.PostStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    input.Executor,
						ProjectName: input.ProjectName,
						Script:      input.PostStopHostCommand,
						ScriptType:  "post-stop",
						ServiceName: input.ServiceName,
					})
				}
			}
			if maxFailureRatioFloat > 0 && failureRatio > maxFailureRatioFloat {
				return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), rolling back deployment", failureRatio, maxFailureRatioFloat)
			}
			return fmt.Errorf("deployment rolled back due to failure (failure_action: rollback)")
		}

		for _, pair := range pairs {
			if pair.healthy && pair.oldContainer != nil {
				oldContainerIdentifier := pair.oldContainer.ID[:12]
				for _, name := range pair.oldContainer.Names {
					if n, found := strings.CutPrefix(name, "/"); found {
						oldContainerIdentifier = n
						break
					}
				}

				input.Logger.Info(fmt.Sprintf("Batch successful, stopping old container %s", oldContainerIdentifier))
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: pair.oldContainer.ID,
					Detached:    input.PreStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PreStopHostCommand,
					ScriptType:  "pre-stop",
					ServiceName: input.ServiceName,
				})
				if input.PreStopCommand != "" {
					_ = runContainerScript(ctx, RunContainerScriptInput{
						Client:      input.Client,
						ContainerID: pair.oldContainer.ID,
						Env:         input.EnvVars,
						Script:      input.PreStopCommand,
						ScriptPath:  "/tmp/pre-stop.sh",
						ServiceName: input.ServiceName,
					})
				}
				_ = runContainerHooks(ctx, RunContainerHooksInput{
					Client:      input.Client,
					ContainerID: pair.oldContainer.ID,
					Hooks:       input.PreStopHooks,
					ServiceName: input.ServiceName,
				})
				if err := input.Client.ContainerTerminate(ctx, pair.oldContainer.ID, input.StopTimeout); err != nil {
					input.Logger.Info(fmt.Sprintf("Error stopping old container %s: %v", oldContainerIdentifier, err))
				}
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: pair.oldContainer.ID,
					Detached:    input.PostStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PostStopHostCommand,
					ScriptType:  "post-stop",
					ServiceName: input.ServiceName,
				})
			}
		}

		return nil
	}

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

			input.Logger.Info(fmt.Sprintf("Waiting for container to become healthy: %s", newContainer.ID[:12]))
			healthcheckInput := WaitForHealthcheckInput{
				Client:             input.Client,
				ContainerID:        newContainer.ID,
				Executor:           input.Executor,
				HealthcheckCommand: input.HealthcheckCommand,
				HealthcheckWait:    input.HealthcheckWait,
				Monitor:            input.Monitor,
				ProjectName:        input.ProjectName,
				ServiceName:        input.ServiceName,
				TickerCh:           input.TickerCh,
			}

			if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
				input.Logger.Info(fmt.Sprintf("Container %s failed health check: %v", newContainer.ID[:12], err))
				if eo, ok := err.(*ErrorWithOutput); ok {
					lines := strings.Split(eo.Output, "\n")
					for _, line := range lines {
						input.Logger.Info(fmt.Sprintf("    %s", line))
					}
				}

				mu.Lock()
				output.Failures++
				mu.Unlock()

				// Clean up failed container
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Detached:    input.PreStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PreStopHostCommand,
					ScriptType:  "pre-stop",
					ServiceName: input.ServiceName,
				})
				// Run pre-stop command inside container if specified
				if input.PreStopCommand != "" {
					_ = runContainerScript(ctx, RunContainerScriptInput{
						Client:      input.Client,
						ContainerID: newContainer.ID,
						Env:         input.EnvVars,
						Script:      input.PreStopCommand,
						ScriptPath:  "/tmp/pre-stop.sh",
						ServiceName: input.ServiceName,
					})
				}
				// Run compose spec pre_stop hooks inside container
				_ = runContainerHooks(ctx, RunContainerHooksInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Hooks:       input.PreStopHooks,
					ServiceName: input.ServiceName,
				})
				_ = input.Client.ContainerTerminate(ctx, newContainer.ID, input.StopTimeout)
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Detached:    input.PostStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PostStopHostCommand,
					ScriptType:  "post-stop",
					ServiceName: input.ServiceName,
				})

				// We don't return error here because we want to continue with others in batch
				// but we check failure ratio later
				return
			}

			if input.WaitAfterHealthy > 0 {
				input.Logger.Info(fmt.Sprintf("Waiting after healthy: %v for container %s", input.WaitAfterHealthy, newContainer.ID[:12]))
				input.Sleeper(input.WaitAfterHealthy)
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

				input.Logger.Info(fmt.Sprintf("Container %s is healthy, stopping %s", newContainer.ID[:12], oldContainerIdentifier))
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: oldContainer.ID,
					Detached:    input.PreStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PreStopHostCommand,
					ScriptType:  "pre-stop",
					ServiceName: input.ServiceName,
				})
				// Run pre-stop command inside container if specified
				if input.PreStopCommand != "" {
					_ = runContainerScript(ctx, RunContainerScriptInput{
						Client:      input.Client,
						ContainerID: oldContainer.ID,
						Env:         input.EnvVars,
						Script:      input.PreStopCommand,
						ScriptPath:  "/tmp/pre-stop.sh",
						ServiceName: input.ServiceName,
					})
				}
				// Run compose spec pre_stop hooks inside container
				_ = runContainerHooks(ctx, RunContainerHooksInput{
					Client:      input.Client,
					ContainerID: oldContainer.ID,
					Hooks:       input.PreStopHooks,
					ServiceName: input.ServiceName,
				})
				if err := input.Client.ContainerTerminate(ctx, oldContainer.ID, input.StopTimeout); err != nil {
					input.Logger.Info(fmt.Sprintf("Error stopping old container %s: %v", oldContainerIdentifier, err))
				}
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: oldContainer.ID,
					Detached:    input.PostStopHostCommandDetached,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PostStopHostCommand,
					ScriptType:  "post-stop",
					ServiceName: input.ServiceName,
				})
			} else {
				input.Logger.Info(fmt.Sprintf("Container %s is healthy", newContainer.ID[:12]))
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

	return nil
}

// rollingUpdateBatchStopFirst stops the old containers first
func rollingUpdateBatchStopFirst(ctx context.Context, input RollingUpdateInput, batch []container.Summary, output *RollingUpdateOutput) error {
	input.Logger.Info(fmt.Sprintf("Stopping %d old containers first", len(batch)))

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
			input.Logger.Info(fmt.Sprintf("Stopping container %s", containerIdentifier))
			_ = runHostScript(ctx, runScriptInput{
				Client:      input.Client,
				ContainerID: containerID,
				Detached:    input.PreStopHostCommandDetached,
				Env:         input.EnvVars,
				Executor:    input.Executor,
				ProjectName: input.ProjectName,
				Script:      input.PreStopHostCommand,
				ScriptType:  "pre-stop",
				ServiceName: input.ServiceName,
			})
			// Run pre-stop command inside container if specified
			if input.PreStopCommand != "" {
				_ = runContainerScript(ctx, RunContainerScriptInput{
					Client:      input.Client,
					ContainerID: containerID,
					Env:         input.EnvVars,
					Script:      input.PreStopCommand,
					ScriptPath:  "/tmp/pre-stop.sh",
					ServiceName: input.ServiceName,
				})
			}
			// Run compose spec pre_stop hooks inside container
			_ = runContainerHooks(ctx, RunContainerHooksInput{
				Client:      input.Client,
				ContainerID: containerID,
				Hooks:       input.PreStopHooks,
				ServiceName: input.ServiceName,
			})

			if input.FailureAction == "rollback" {
				timeoutSeconds := input.StopTimeout
				err := input.Client.ContainerStop(stopCtx, containerID, container.StopOptions{Timeout: &timeoutSeconds})
				if err != nil {
					return fmt.Errorf("error stopping container %s: %v", containerIdentifier, err)
				}
				return nil
			}

			err := input.Client.ContainerTerminate(stopCtx, containerID, input.StopTimeout)
			_ = runHostScript(ctx, runScriptInput{
				Client:      input.Client,
				ContainerID: containerID,
				Detached:    input.PostStopHostCommandDetached,
				Env:         input.EnvVars,
				Executor:    input.Executor,
				ProjectName: input.ProjectName,
				Script:      input.PostStopHostCommand,
				ScriptType:  "post-stop",
				ServiceName: input.ServiceName,
			})
			return err
		})
	}

	if err := g.Wait(); err != nil {
		if input.FailureAction == "rollback" {
			return fmt.Errorf("error stopping containers in batch during rollback: %v", err)
		}
		return fmt.Errorf("error stopping containers in batch: %v", err)
	}

	// Current scale after stopping containers is (total - stopped)
	// We want to scale back up to target replicas (or current replicas if we are in middle of update)
	// Actually, we should scale up to whatever the count was before we stopped these
	currentContainers, err := composeContainers(ctx, ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting current containers: %v", err)
	}

	// Start new containers
	targetScale := len(currentContainers) + len(batch)
	args := []string{"compose"}
	args = append(args, ComposeFileArgs(input.ComposeFiles)...)
	args = append(args, EnvFileArgs(input.EnvFiles)...)
	args = append(args, "-p", input.ProjectName, "up", "--detach")
	if input.Build {
		args = append(args, "--build")
	}
	args = append(args, "--pull", input.PullPolicy,
		"--scale", fmt.Sprintf("%s=%d", input.ServiceName, targetScale),
		"--no-deps", "--no-recreate", input.ServiceName)
	_, err = input.Executor(ctx, ExecCommandInput{
		Command:          "docker",
		Args:             args,
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		if input.FailureAction == "rollback" {
			input.Logger.Info("Rolling back: restarting old containers after failed container creation")
			for _, oldContainer := range batch {
				if startErr := input.Client.ContainerStart(ctx, oldContainer.ID, container.StartOptions{}); startErr != nil {
					input.Logger.Info(fmt.Sprintf("Warning: failed to restart old container %s during rollback: %v", oldContainer.ID[:12], startErr))
				}
			}
			return fmt.Errorf("deployment rolled back due to container creation failure (failure_action: rollback): %v", err)
		}
		return fmt.Errorf("error starting new containers: %v", err)
	}

	// Get all containers to find the new ones
	allContainers, err := composeContainers(ctx, ComposeContainersInput{
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

			input.Logger.Info(fmt.Sprintf("Waiting for container to become healthy: %s", newContainer.ID[:12]))
			healthcheckInput := WaitForHealthcheckInput{
				Client:             input.Client,
				ContainerID:        newContainer.ID,
				Executor:           input.Executor,
				HealthcheckCommand: input.HealthcheckCommand,
				HealthcheckWait:    input.HealthcheckWait,
				Monitor:            input.Monitor,
				ProjectName:        input.ProjectName,
				ServiceName:        input.ServiceName,
				TickerCh:           input.TickerCh,
			}

			if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
				input.Logger.Info(fmt.Sprintf("Container %s failed health check: %v", newContainer.ID[:12], err))
				if eo, ok := err.(*ErrorWithOutput); ok {
					lines := strings.Split(eo.Output, "\n")
					for _, line := range lines {
						input.Logger.Info(fmt.Sprintf("    %s", line))
					}
				}

				mu.Lock()
				output.Failures++
				mu.Unlock()

				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PreStopHostCommand,
					ScriptType:  "pre-stop",
					ServiceName: input.ServiceName,
				})
				_ = input.Client.ContainerTerminate(ctx, newContainer.ID, input.StopTimeout)
				_ = runHostScript(ctx, runScriptInput{
					Client:      input.Client,
					ContainerID: newContainer.ID,
					Env:         input.EnvVars,
					Executor:    input.Executor,
					ProjectName: input.ProjectName,
					Script:      input.PostStopHostCommand,
					ScriptType:  "post-stop",
					ServiceName: input.ServiceName,
				})
				return
			}

			if input.WaitAfterHealthy > 0 {
				input.Logger.Info(fmt.Sprintf("Waiting after healthy: %v for container %s", input.WaitAfterHealthy, newContainer.ID[:12]))
				input.Sleeper(input.WaitAfterHealthy)
			}

			input.Logger.Info(fmt.Sprintf("Container %s is healthy", newContainer.ID[:12]))
		}(nc)
	}

	wg.Wait()

	// Check failure ratio
	failureRatio := float64(output.Failures) / float64(output.TotalUpdates)
	maxFailureRatioFloat := float64(input.MaxFailureRatio)

	if input.FailureAction == "rollback" {
		shouldRollback := output.Failures > 0
		if maxFailureRatioFloat > 0 && failureRatio <= maxFailureRatioFloat {
			shouldRollback = false
		}

		if shouldRollback {
			input.Logger.Info("Rolling back: restarting old containers and terminating new containers")
			for _, oldContainer := range batch {
				if err := input.Client.ContainerStart(ctx, oldContainer.ID, container.StartOptions{}); err != nil {
					input.Logger.Info(fmt.Sprintf("Warning: failed to restart old container %s during rollback: %v", oldContainer.ID[:12], err))
				}
			}
			for _, nc := range newContainers {
				_ = input.Client.ContainerTerminate(ctx, nc.ID, input.StopTimeout)
			}
			if maxFailureRatioFloat > 0 && failureRatio > maxFailureRatioFloat {
				return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), rolling back deployment", failureRatio, maxFailureRatioFloat)
			}
			return fmt.Errorf("deployment rolled back due to failure (failure_action: rollback)")
		}

		for _, oldContainer := range batch {
			if err := input.Client.ContainerRemove(ctx, oldContainer.ID, container.RemoveOptions{RemoveVolumes: true}); err != nil {
				input.Logger.Info(fmt.Sprintf("Warning: failed to remove old container %s: %v", oldContainer.ID[:12], err))
			}
			_ = runHostScript(ctx, runScriptInput{
				Client:      input.Client,
				ContainerID: oldContainer.ID,
				Detached:    input.PostStopHostCommandDetached,
				Env:         input.EnvVars,
				Executor:    input.Executor,
				ProjectName: input.ProjectName,
				Script:      input.PostStopHostCommand,
				ScriptType:  "post-stop",
				ServiceName: input.ServiceName,
			})
		}
		return nil
	}

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
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// CurrentContainers is the current list of containers
	CurrentContainers []container.Summary
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables from env files
	EnvVars map[string]string
	// CurrentReplicas is the current number of containers
	CurrentReplicas int
	// DesiredReplicas is the target number of replicas
	DesiredReplicas int
	// Executor is the command executor to use. If nil, ExecCommand will be used.
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// SkipDatabases is whether to skip interacting with databases
	SkipDatabases bool
	// PreStopHostCommand is the command to run before stopping a container (on host)
	PreStopHostCommand string
	// PreStopHostCommandDetached indicates if the pre-stop host command should run detached
	PreStopHostCommandDetached bool
	// PreStopCommand is the command to run inside the container before stopping
	PreStopCommand string
	// PreStopHooks are the compose spec pre_stop hooks to run inside the container before stopping
	PreStopHooks []types.ServiceHook
	// PostStopHostCommand is the command to run after stopping a container
	PostStopHostCommand string
	// PostStopHostCommandDetached indicates if the post-stop command should run detached
	PostStopHostCommandDetached bool
	// StopTimeout is the timeout in seconds for stopping containers (from stop_grace_period)
	StopTimeout int
}

// scaleDownContainers scales down containers by stopping and removing excess ones
// It always removes the oldest containers first
func scaleDownContainers(ctx context.Context, input ScaleDownContainersInput) error {
	toRemove := input.CurrentReplicas - input.DesiredReplicas

	if toRemove <= 0 {
		return nil
	}

	if input.CurrentReplicas == 0 {
		return nil
	}

	skipService := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
		Container:           input.CurrentContainers[0],
		ServiceName:         input.ServiceName,
		ShouldSkipDatabases: input.SkipDatabases,
		Logger:              input.Logger,
	})
	if skipService {
		return nil
	}

	input.Logger.Info(fmt.Sprintf("Scaling down containers: current-replicas=%d, target-replicas=%d", input.CurrentReplicas, input.DesiredReplicas))

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
		input.Logger.Info(fmt.Sprintf("Stopping container %s", containerIdentifier))

		executor := input.Executor
		if executor == nil {
			executor = ExecCommand
		}

		_ = runHostScript(ctx, runScriptInput{
			Client:      input.Client,
			ContainerID: container.ID,
			Detached:    input.PreStopHostCommandDetached,
			Env:         input.EnvVars,
			Executor:    executor,
			ProjectName: input.ProjectName,
			Script:      input.PreStopHostCommand,
			ScriptType:  "pre-stop",
			ServiceName: input.ServiceName,
		})
		// Run pre-stop command inside container if specified
		if input.PreStopCommand != "" {
			_ = runContainerScript(ctx, RunContainerScriptInput{
				Client:      input.Client,
				ContainerID: container.ID,
				Env:         input.EnvVars,
				Script:      input.PreStopCommand,
				ScriptPath:  "/tmp/pre-stop.sh",
				ServiceName: input.ServiceName,
			})
		}
		// Run compose spec pre_stop hooks inside container
		_ = runContainerHooks(ctx, RunContainerHooksInput{
			Client:      input.Client,
			ContainerID: container.ID,
			Hooks:       input.PreStopHooks,
			ServiceName: input.ServiceName,
		})
		if err := input.Client.ContainerTerminate(ctx, container.ID, input.StopTimeout); err != nil {
			return fmt.Errorf("error scaling down: %v", err)
		}
		_ = runHostScript(ctx, runScriptInput{
			Client:      input.Client,
			ContainerID: container.ID,
			Detached:    input.PostStopHostCommandDetached,
			Env:         input.EnvVars,
			Executor:    executor,
			ProjectName: input.ProjectName,
			Script:      input.PostStopHostCommand,
			ScriptType:  "post-stop",
			ServiceName: input.ServiceName,
		})
	}

	return nil
}

// ScaleUpContainersInput is the input for the scaleUpContainers function
type ScaleUpContainersInput struct {
	// Build is whether to build images before deploying
	Build bool
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// CurrentReplicas is the current number of containers
	CurrentReplicas int
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables from env files
	EnvVars map[string]string
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
	// HealthcheckWait is the duration to wait before considering a container without a Docker healthcheck as healthy
	HealthcheckWait time.Duration
	// WaitAfterHealthy is the duration to wait after a container becomes healthy before proceeding
	WaitAfterHealthy time.Duration
	// Logger is the logger to use
	Logger *command.ZerologUi
	// MaxFailureRatio is the maximum allowed failure ratio
	MaxFailureRatio float32
	// Monitor is the health check monitoring duration
	Monitor time.Duration
	// Parallelism is the number of containers to update simultaneously
	Parallelism int
	// PostStartHooks are the compose spec post_start hooks to run inside the container after starting
	PostStartHooks []types.ServiceHook
	// ProjectDir is the project directory
	ProjectDir string
	// ProjectName is the name of the project
	ProjectName string
	// PullPolicy is the pull policy to pass to docker compose (always, missing, never)
	PullPolicy string
	// RollbackConfig is the optional rollback configuration. Used when FailureAction is "rollback".
	RollbackConfig *types.UpdateConfig
	// ServiceName is the name of the service
	ServiceName string
	// Sleeper is the function to use for sleeping. If nil, time.Sleep will be used.
	Sleeper func(time.Duration)
	// PreStopHostCommand is the command to run before stopping a container (on host)
	PreStopHostCommand string
	// PreStopHostCommandDetached indicates if the pre-stop host command should run detached
	PreStopHostCommandDetached bool
	// PreStopCommand is the command to run inside the container before stopping
	PreStopCommand string
	// PreStopHooks are the compose spec pre_stop hooks to run inside the container before stopping
	PreStopHooks []types.ServiceHook
	// PostStopHostCommand is the command to run after stopping a container
	PostStopHostCommand string
	// PostStopHostCommandDetached indicates if the post-stop command should run detached
	PostStopHostCommandDetached bool
	// StopTimeout is the timeout in seconds for stopping containers (from stop_grace_period)
	StopTimeout int
	// TickerCh is an optional channel to use for ticking. If nil, time.NewTicker will be used.
	TickerCh <-chan time.Time
}

// scaleUpContainers scales up containers by creating and starting new ones
func scaleUpContainers(ctx context.Context, input ScaleUpContainersInput) error {
	input.Logger.Info(fmt.Sprintf("Scaling up containers: service=%s, current-replicas=%d, parallelism=%d, target-replicas=%d", input.ServiceName, input.CurrentReplicas, input.Parallelism, input.DesiredReplicas))

	if input.Sleeper == nil {
		input.Sleeper = time.Sleep
	}

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Create all containers at once
	args := []string{"compose"}
	args = append(args, ComposeFileArgs(input.ComposeFiles)...)
	args = append(args, EnvFileArgs(input.EnvFiles)...)
	args = append(args, "-p", input.ProjectName, "create")
	if input.Build {
		args = append(args, "--build")
	}
	args = append(args, "--pull", input.PullPolicy,
		"--scale", fmt.Sprintf("%s=%d", input.ServiceName, input.DesiredReplicas),
		input.ServiceName)
	_, err := executor(ctx, ExecCommandInput{
		Command:          "docker",
		Args:             args,
		WorkingDirectory: input.ProjectDir,
	})
	if err != nil {
		return fmt.Errorf("error creating containers: %v", err)
	}

	// Get all created containers (including existing running ones)
	allContainers, err := composeContainers(ctx, ComposeContainersInput{
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
		input.Logger.Info("No created containers to start")
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
					input.Logger.Info(fmt.Sprintf("Error starting container %s: %v", c.ID[:12], err))
					mu.Lock()
					failures++
					if batchErr == nil {
						batchErr = fmt.Errorf("error starting container %s: %v", c.ID[:12], err)
					}
					mu.Unlock()
					return
				}

				// Run compose spec post_start hooks inside container
				if err := runContainerHooks(ctx, RunContainerHooksInput{
					Client:      input.Client,
					ContainerID: c.ID,
					Hooks:       input.PostStartHooks,
					ServiceName: input.ServiceName,
				}); err != nil {
					input.Logger.Info(fmt.Sprintf("Container %s post_start hook failed: %v", c.ID[:12], err))
					if eo, ok := err.(*ErrorWithOutput); ok {
						lines := strings.Split(eo.Output, "\n")
						for _, line := range lines {
							input.Logger.Info(fmt.Sprintf("    %s", line))
						}
					}

					mu.Lock()
					failures++
					if batchErr == nil {
						batchErr = fmt.Errorf("container %s post_start hook failed: %v", c.ID[:12], err)
					}
					mu.Unlock()

					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Detached:    input.PreStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    executor,
						ProjectName: input.ProjectName,
						Script:      input.PreStopHostCommand,
						ScriptType:  "pre-stop",
						ServiceName: input.ServiceName,
					})
					if input.PreStopCommand != "" {
						_ = runContainerScript(ctx, RunContainerScriptInput{
							Client:      input.Client,
							ContainerID: c.ID,
							Env:         input.EnvVars,
							Script:      input.PreStopCommand,
							ScriptPath:  "/tmp/pre-stop.sh",
							ServiceName: input.ServiceName,
						})
					}
					_ = runContainerHooks(ctx, RunContainerHooksInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Hooks:       input.PreStopHooks,
						ServiceName: input.ServiceName,
					})
					_ = input.Client.ContainerTerminate(ctx, c.ID, input.StopTimeout)
					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Detached:    input.PostStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    executor,
						ProjectName: input.ProjectName,
						Script:      input.PostStopHostCommand,
						ScriptType:  "post-stop",
						ServiceName: input.ServiceName,
					})
					return
				}

				// Wait for health check
				input.Logger.Info(fmt.Sprintf("Waiting for container to become healthy: %s", c.ID[:12]))
				healthcheckInput := WaitForHealthcheckInput{
					Client:             input.Client,
					ContainerID:        c.ID,
					Executor:           executor,
					HealthcheckCommand: input.HealthcheckCommand,
					HealthcheckWait:    input.HealthcheckWait,
					Monitor:            input.Monitor,
					ProjectName:        input.ProjectName,
					ServiceName:        input.ServiceName,
					TickerCh:           input.TickerCh,
				}

				if err := waitForHealthcheck(ctx, healthcheckInput); err != nil {
					input.Logger.Info(fmt.Sprintf("Container %s failed health check: %v", c.ID[:12], err))
					if eo, ok := err.(*ErrorWithOutput); ok {
						lines := strings.Split(eo.Output, "\n")
						for _, line := range lines {
							input.Logger.Info(fmt.Sprintf("    %s", line))
						}
					}

					mu.Lock()
					failures++
					if batchErr == nil {
						batchErr = fmt.Errorf("container %s failed health check: %v", c.ID[:12], err)
					}
					mu.Unlock()

					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Detached:    input.PreStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    executor,
						ProjectName: input.ProjectName,
						Script:      input.PreStopHostCommand,
						ScriptType:  "pre-stop",
						ServiceName: input.ServiceName,
					})
					// Run pre-stop command inside container if specified
					if input.PreStopCommand != "" {
						_ = runContainerScript(ctx, RunContainerScriptInput{
							Client:      input.Client,
							ContainerID: c.ID,
							Env:         input.EnvVars,
							Script:      input.PreStopCommand,
							ScriptPath:  "/tmp/pre-stop.sh",
							ServiceName: input.ServiceName,
						})
					}
					// Run compose spec pre_stop hooks inside container
					_ = runContainerHooks(ctx, RunContainerHooksInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Hooks:       input.PreStopHooks,
						ServiceName: input.ServiceName,
					})
					_ = input.Client.ContainerTerminate(ctx, c.ID, input.StopTimeout)
					_ = runHostScript(ctx, runScriptInput{
						Client:      input.Client,
						ContainerID: c.ID,
						Detached:    input.PostStopHostCommandDetached,
						Env:         input.EnvVars,
						Executor:    executor,
						ProjectName: input.ProjectName,
						Script:      input.PostStopHostCommand,
						ScriptType:  "post-stop",
						ServiceName: input.ServiceName,
					})
				} else if input.WaitAfterHealthy > 0 {
					input.Logger.Info(fmt.Sprintf("Waiting after healthy: %v for container %s", input.WaitAfterHealthy, c.ID[:12]))
					input.Sleeper(input.WaitAfterHealthy)
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
			if input.FailureAction == "rollback" {
				return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f), rolling back deployment during scale-up", failureRatio, maxFailureRatioFloat)
			}
			return fmt.Errorf("max failure ratio exceeded (%.2f > %.2f)", failureRatio, maxFailureRatioFloat)
		}

		if input.FailureAction == "pause" && failures > 0 {
			return fmt.Errorf("deployment paused due to failure (failure_action: pause)")
		}

		if input.FailureAction == "rollback" && failures > 0 {
			return fmt.Errorf("deployment rolled back due to failure during scale-up (failure_action: rollback)")
		}

		if batchErr != nil && input.MaxFailureRatio == 0 {
			return batchErr
		}

		// Wait for delay between batches (except for the last batch)
		if i+batchSize < len(createdContainers) && input.Delay > 0 {
			input.Logger.Info(fmt.Sprintf("Waiting before next batch: %v", input.Delay))
			input.Sleeper(input.Delay)
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

// containerSortInfo holds the health and restart state for sorting containers
type containerSortInfo struct {
	isUnhealthy  bool
	restartCount int
}

// sortContainersByHealthThenCreationTime sorts containers by health status (unhealthy first),
// then by restart count (most restarts first), then by creation time (oldest first).
// Returns the number of unhealthy containers found.
func sortContainersByHealthThenCreationTime(ctx context.Context, client DockerClientInterface, containers []container.Summary) int {
	if len(containers) == 0 {
		return 0
	}

	// Inspect each container to get health status and restart count
	info := make(map[string]containerSortInfo, len(containers))
	unhealthyCount := 0
	for _, c := range containers {
		si := containerSortInfo{}
		resp, err := client.ContainerInspect(ctx, c.ID)
		if err == nil && resp.ContainerJSONBase != nil {
			si.restartCount = resp.RestartCount
			if resp.ContainerJSONBase.State != nil &&
				resp.ContainerJSONBase.State.Health != nil &&
				resp.ContainerJSONBase.State.Health.Status == "unhealthy" {
				si.isUnhealthy = true
				unhealthyCount++
			}
		}
		info[c.ID] = si
	}

	slices.SortFunc(containers, func(a, b container.Summary) int {
		ai := info[a.ID]
		bi := info[b.ID]

		// Primary: unhealthy before healthy
		if ai.isUnhealthy && !bi.isUnhealthy {
			return -1
		}
		if !ai.isUnhealthy && bi.isUnhealthy {
			return 1
		}

		// Secondary: more restarts first
		if ai.restartCount > bi.restartCount {
			return -1
		}
		if ai.restartCount < bi.restartCount {
			return 1
		}

		// Tertiary: oldest first
		if a.Created < b.Created {
			return -1
		}
		if a.Created > b.Created {
			return 1
		}

		return 0
	})

	return unhealthyCount
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
// using the provided Go template. The template has access to .ProjectName, .ServiceName, and .InstanceID.
//
// Uses a two-pass approach to avoid name conflicts: first renames to temporary names,
// then renames to final names. This handles cases where container A's current name is
// container B's desired name (e.g., after docker compose assigns names in a different
// order than the creation-time sort).
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

	// Build rename plan: compute desired names for each container in ascending creation order
	type renamePlan struct {
		containerID string
		currentName string
		desiredName string
	}

	var toRename []renamePlan
	for i, c := range input.Containers {
		instanceID := i + 1

		var buf bytes.Buffer
		data := ContainerNameTemplateData{
			ProjectName: input.ProjectName,
			ServiceName: input.ServiceName,
			InstanceID:  instanceID,
		}
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("error executing container name template: %v", err)
		}
		desiredName := buf.String()

		currentName := ""
		if len(c.Names) > 0 {
			currentName = strings.TrimPrefix(c.Names[0], "/")
		}

		if currentName != desiredName {
			toRename = append(toRename, renamePlan{
				containerID: c.ID,
				currentName: currentName,
				desiredName: desiredName,
			})
		}
	}

	// Pass 1: rename to temporary names to avoid conflicts
	for _, r := range toRename {
		tmpName := r.desiredName + "-tmp"
		if err := input.Client.ContainerRename(ctx, r.containerID, tmpName); err != nil {
			return fmt.Errorf("error renaming container %s to %s: %v", r.containerID[:12], tmpName, err)
		}
	}

	// Pass 2: rename from temporary to final names in ascending creation order
	for _, r := range toRename {
		if err := input.Client.ContainerRename(ctx, r.containerID, r.desiredName); err != nil {
			return fmt.Errorf("error renaming container %s to %s: %v", r.containerID[:12], r.desiredName, err)
		}
	}

	return nil
}
