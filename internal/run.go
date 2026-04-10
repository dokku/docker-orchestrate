package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/josegonzalez/cli-skeleton/command"
)

// RunServiceInput is the input for the RunService function
type RunServiceInput struct {
	// Build is whether to build images before running
	Build bool
	// Client is the Docker client
	Client DockerClientInterface
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// Detach runs the service in the background
	Detach bool
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables
	EnvVars map[string]string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Project is the parsed compose project
	Project *composetypes.Project
	// ProjectName is the name of the project
	ProjectName string
	// PullPolicy is the pull policy override from the CLI flag
	PullPolicy string
	// ServiceName is the name of the service
	ServiceName string
}

// RunService runs a one-shot service on demand.
// It validates the service exists and is a one-shot service, runs deploy hooks,
// and executes the service with labels for execution tracking.
func RunService(ctx context.Context, input RunServiceInput) error {
	if len(input.ComposeFiles) == 0 {
		return fmt.Errorf("at least one compose file is required")
	}

	if input.ProjectName == "" {
		return fmt.Errorf("project name is required")
	}

	if input.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}

	if input.Project == nil {
		return fmt.Errorf("project is required")
	}

	var service *composetypes.ServiceConfig
	for _, s := range input.Project.Services {
		if s.Name == input.ServiceName {
			service = &s
			break
		}
	}
	if service == nil {
		return fmt.Errorf("service %s not found in compose file", input.ServiceName)
	}

	if !IsOneShotService(service) {
		return fmt.Errorf("service %s is not a one-shot service (restart must be \"no\")", input.ServiceName)
	}

	// Parse per-service deploy hook extensions from deploy config
	servicePreDeployHostCommand := ""
	servicePostDeployHostCommand := ""
	servicePreDeployHostCommandDetached := false
	servicePostDeployHostCommandDetached := false
	if service.Deploy != nil && service.Deploy.Extensions != nil {
		if cmd, ok := service.Deploy.Extensions["x-pre-deploy-host-command"].(string); ok {
			servicePreDeployHostCommand = cmd
		}
		if cmd, ok := service.Deploy.Extensions["x-post-deploy-host-command"].(string); ok {
			servicePostDeployHostCommand = cmd
		}
		if detached, err := parseDetachedFlag(service.Deploy.Extensions, "x-pre-deploy-host-command-detached"); err != nil {
			return err
		} else {
			servicePreDeployHostCommandDetached = detached
		}
		if detached, err := parseDetachedFlag(service.Deploy.Extensions, "x-post-deploy-host-command-detached"); err != nil {
			return err
		} else {
			servicePostDeployHostCommandDetached = detached
		}
	}

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Run per-service pre-deploy host command
	if err := runHostScript(ctx, runScriptInput{
		Client:      input.Client,
		Detached:    servicePreDeployHostCommandDetached,
		Env:         input.EnvVars,
		Executor:    executor,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Script:      servicePreDeployHostCommand,
		ScriptType:  "pre-deploy",
	}); err != nil {
		return fmt.Errorf("pre-deploy host command failed for service %s: %v", input.ServiceName, err)
	}

	// Generate container name and labels for execution tracking
	now := time.Now()
	suffix := randomSuffix(4)
	containerName := fmt.Sprintf("%s-%s-%s-%s", input.ProjectName, input.ServiceName, now.Format("20060102-150405"), suffix)
	triggeredAt := now.UTC().Format(time.RFC3339)

	labels := []string{
		"com.dokku.orchestrate/run=true",
		fmt.Sprintf("com.dokku.orchestrate/run-project=%s", input.ProjectName),
		fmt.Sprintf("com.dokku.orchestrate/run-service=%s", input.ServiceName),
		fmt.Sprintf("com.dokku.orchestrate/run-triggered-at=%s", triggeredAt),
	}

	if input.Detach {
		// Detached mode: build a custom docker compose run command with --detach
		input.Logger.Info(fmt.Sprintf("Spawning one-shot service %s (container: %s)", input.ServiceName, containerName))

		pullPolicy, buildImage, err := ResolvePullPolicy(input.PullPolicy, input.Build, service)
		if err != nil {
			return err
		}

		projectDir := filepath.Dir(input.ComposeFiles[0])

		// Pre-build image when build flag is set
		if buildImage {
			input.Logger.Info(fmt.Sprintf("Building image for one-shot service %s", input.ServiceName))
			buildArgs := []string{"compose"}
			buildArgs = append(buildArgs, ComposeFileArgs(input.ComposeFiles)...)
			buildArgs = append(buildArgs, EnvFileArgs(input.EnvFiles)...)
			buildArgs = append(buildArgs, "-p", input.ProjectName, "build", input.ServiceName)
			_, err = executor(ctx, ExecCommandInput{
				Command:          "docker",
				Args:             buildArgs,
				WorkingDirectory: projectDir,
			})
			if err != nil {
				return fmt.Errorf("error building image for one-shot service %s: %v", input.ServiceName, err)
			}
		}

		// Pre-pull image when pull policy is "always" and not building
		if pullPolicy == "always" && !buildImage {
			input.Logger.Info(fmt.Sprintf("Pulling image for one-shot service %s", input.ServiceName))
			pullArgs := []string{"compose"}
			pullArgs = append(pullArgs, ComposeFileArgs(input.ComposeFiles)...)
			pullArgs = append(pullArgs, EnvFileArgs(input.EnvFiles)...)
			pullArgs = append(pullArgs, "-p", input.ProjectName, "pull", input.ServiceName)
			_, err = executor(ctx, ExecCommandInput{
				Command:          "docker",
				Args:             pullArgs,
				WorkingDirectory: projectDir,
			})
			if err != nil {
				return fmt.Errorf("error pulling image for one-shot service %s: %v", input.ServiceName, err)
			}
		}

		// Build run command with --detach
		runArgs := []string{"compose"}
		runArgs = append(runArgs, ComposeFileArgs(input.ComposeFiles)...)
		runArgs = append(runArgs, EnvFileArgs(input.EnvFiles)...)
		runArgs = append(runArgs, "-p", input.ProjectName, "run", "--no-deps")
		runArgs = append(runArgs, "--name", containerName)
		for _, label := range labels {
			runArgs = append(runArgs, "--label", label)
		}
		runArgs = append(runArgs, input.ServiceName)
		runArgs = appendDetachFlag(runArgs)

		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             runArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error spawning one-shot service %s: %v", input.ServiceName, err)
		}

		return nil
	}

	// Foreground mode: use DeployOneShotService with streaming and labels
	err := DeployOneShotService(ctx, DeployOneShotServiceInput{
		Build:         input.Build,
		ComposeFiles:  input.ComposeFiles,
		ContainerName: containerName,
		EnvFiles:      input.EnvFiles,
		Executor:      input.Executor,
		Labels:        labels,
		Logger:        input.Logger,
		NoRemove:      true,
		ProjectName:   input.ProjectName,
		PullPolicy:    input.PullPolicy,
		Service:       service,
		ServiceName:   input.ServiceName,
		StreamOutput:  true,
	})
	if err != nil {
		return err
	}

	// Run per-service post-deploy host command (only on success, foreground only)
	if err := runHostScript(ctx, runScriptInput{
		Client:      input.Client,
		Detached:    servicePostDeployHostCommandDetached,
		Env:         input.EnvVars,
		Executor:    executor,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Script:      servicePostDeployHostCommand,
		ScriptType:  "post-deploy",
	}); err != nil {
		return fmt.Errorf("post-deploy host command failed for service %s: %v", input.ServiceName, err)
	}

	return nil
}

// OneShotServiceInfo contains information about a one-shot service
type OneShotServiceInfo struct {
	// Name is the service name
	Name string
	// Command is the service command as a string
	Command string
}

// ListOneShotServices returns a list of one-shot services in the project
func ListOneShotServices(project *composetypes.Project) []OneShotServiceInfo {
	var services []OneShotServiceInfo
	for _, s := range project.Services {
		if !IsOneShotService(&s) {
			continue
		}
		cmd := ""
		if s.Command != nil {
			cmd = strings.Join(s.Command, " ")
		}
		services = append(services, OneShotServiceInfo{
			Name:    s.Name,
			Command: cmd,
		})
	}
	return services
}

// RunExecution contains information about an executed one-shot service
type RunExecution struct {
	// ContainerName is the name of the container
	ContainerName string
	// Service is the service name
	Service string
	// Status is the container status (e.g. "running", "exited")
	Status string
	// ExitCode is the exit code (-1 if still running)
	ExitCode int
	// TriggeredAt is when the execution was triggered
	TriggeredAt string
}

// ListRunExecutionsInput is the input for the ListRunExecutions function
type ListRunExecutionsInput struct {
	// Client is the Docker client
	Client DockerClientInterface
	// ProjectName filters by project name (empty = all projects)
	ProjectName string
}

// ListRunExecutions returns a list of past and running one-shot service executions
func ListRunExecutions(ctx context.Context, input ListRunExecutionsInput) ([]RunExecution, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "com.dokku.orchestrate/run=true")
	if input.ProjectName != "" {
		filterArgs.Add("label", fmt.Sprintf("com.dokku.orchestrate/run-project=%s", input.ProjectName))
	}

	containers, err := input.Client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("error listing run executions: %v", err)
	}

	var executions []RunExecution
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		service := c.Labels["com.dokku.orchestrate/run-service"]
		triggeredAt := c.Labels["com.dokku.orchestrate/run-triggered-at"]

		status := c.State
		exitCode := -1
		if c.State == "exited" {
			// Extract exit code from container status string (e.g. "Exited (0) 5 minutes ago")
			fmt.Sscanf(c.Status, "Exited (%d)", &exitCode)
		}

		executions = append(executions, RunExecution{
			ContainerName: name,
			Service:       service,
			Status:        status,
			ExitCode:      exitCode,
			TriggeredAt:   triggeredAt,
		})
	}

	return executions, nil
}
