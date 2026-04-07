package internal

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"path/filepath"
	"time"

	"github.com/josegonzalez/cli-skeleton/command"
)

// SpawnCronTaskInput is the input for the SpawnCronTask function
type SpawnCronTaskInput struct {
	// Build is whether to build images before running
	Build bool
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Project is the cron project configuration
	Project CronProject
	// PullPolicy is the pull policy override from the CLI flag
	PullPolicy string
	// Service is the cron service configuration
	Service CronService
}

// SpawnCronTask spawns a one-shot container for a cron task.
// It attaches metadata labels to the container for the notifier to use.
// If no-overlap is enabled, it checks for running containers first.
// The container is spawned in a fire-and-forget manner.
func SpawnCronTask(ctx context.Context, input SpawnCronTaskInput) error {
	config := input.Service.Config
	service := input.Service.Service
	projectName := input.Project.Name
	serviceName := input.Service.Name

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Check for overlap if no-overlap is enabled
	if config.NoOverlap {
		running, err := checkRunningCronContainer(ctx, executor, input.Project, serviceName)
		if err != nil {
			input.Logger.Error(fmt.Sprintf("Error checking for running cron container %s/%s: %v", projectName, serviceName, err))
		}
		if running {
			input.Logger.Info(fmt.Sprintf("Skipping cron job %s/%s — previous run still active", projectName, serviceName))
			return nil
		}
	}

	// Resolve pull policy
	pullPolicy, buildImage, err := ResolvePullPolicy(input.PullPolicy, input.Build, service)
	if err != nil {
		return err
	}

	projectDir := input.Project.WorkingDirectory
	if projectDir == "" && len(input.Project.ComposeFiles) > 0 {
		projectDir = filepath.Dir(input.Project.ComposeFiles[0])
	}

	// Pre-build image when build flag is set
	if buildImage {
		input.Logger.Info(fmt.Sprintf("Building image for cron service %s/%s", projectName, serviceName))
		buildArgs := []string{"compose"}
		buildArgs = append(buildArgs, ComposeFileArgs(input.Project.ComposeFiles)...)
		buildArgs = append(buildArgs, EnvFileArgs(input.Project.EnvFiles)...)
		buildArgs = append(buildArgs, "-p", projectName, "build", serviceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             buildArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error building image for cron service %s/%s: %v", projectName, serviceName, err)
		}
	}

	// Pre-pull image when pull policy is "always" and not building
	if pullPolicy == "always" && !buildImage {
		input.Logger.Info(fmt.Sprintf("Pulling image for cron service %s/%s", projectName, serviceName))
		pullArgs := []string{"compose"}
		pullArgs = append(pullArgs, ComposeFileArgs(input.Project.ComposeFiles)...)
		pullArgs = append(pullArgs, EnvFileArgs(input.Project.EnvFiles)...)
		pullArgs = append(pullArgs, "-p", projectName, "pull", serviceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             pullArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error pulling image for cron service %s/%s: %v", projectName, serviceName, err)
		}
	}

	// Generate container name and timestamp
	now := time.Now()
	suffix := randomSuffix(4)
	containerName := fmt.Sprintf("%s-%s-%s-%s", projectName, serviceName, now.Format("20060102-150405"), suffix)
	triggeredAt := now.UTC().Format(time.RFC3339)

	// Build run command with labels
	input.Logger.Info(fmt.Sprintf("Spawning cron task %s/%s (container: %s)", projectName, serviceName, containerName))
	runArgs := []string{"compose"}
	runArgs = append(runArgs, ComposeFileArgs(input.Project.ComposeFiles)...)
	runArgs = append(runArgs, EnvFileArgs(input.Project.EnvFiles)...)
	runArgs = append(runArgs, "-p", projectName, "run", "--no-deps")
	runArgs = append(runArgs, "--name", containerName)

	// Attach metadata labels
	runArgs = append(runArgs, "--label", "com.dokku.orchestrate/cron=true")
	runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-project=%s", projectName))
	runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-service=%s", serviceName))
	runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-triggered-at=%s", triggeredAt))
	runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-schedule=%s", config.Schedule))

	// Attach notification labels if configured
	hasNotify := false
	if config.Notify != nil {
		hasNotify = true
		runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-notify-url=%s", config.Notify.URL))
		runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-notify-on=%s", config.Notify.On))
		runArgs = append(runArgs, "--label", fmt.Sprintf("com.dokku.orchestrate/cron-notify-include-output=%t", config.Notify.IncludeOutput))
	}

	// Use --rm only when no notifications are configured
	// (notifier handles cleanup after sending webhook)
	if !hasNotify {
		runArgs = append(runArgs, "--rm")
	}

	runArgs = append(runArgs, serviceName)

	// Fire and forget — spawn the container without waiting for it to exit.
	// We use --detach (-d) so docker compose run returns immediately.
	runArgs = appendDetachFlag(runArgs)

	_, err = executor(ctx, ExecCommandInput{
		Command:          "docker",
		Args:             runArgs,
		WorkingDirectory: projectDir,
	})
	if err != nil {
		return fmt.Errorf("error spawning cron task %s/%s: %v", projectName, serviceName, err)
	}

	return nil
}

// appendDetachFlag inserts --detach (-d) after "run" and before the service name
// so that docker compose run returns immediately
func appendDetachFlag(args []string) []string {
	// Find the position of "run" in args and insert -d after it
	for i, arg := range args {
		if arg == "run" {
			result := make([]string, 0, len(args)+1)
			result = append(result, args[:i+1]...)
			result = append(result, "-d")
			result = append(result, args[i+1:]...)
			return result
		}
	}
	return args
}

// checkRunningCronContainer checks if there's a running container for the given
// project/service cron task
func checkRunningCronContainer(ctx context.Context, executor CommandExecutor, project CronProject, serviceName string) (bool, error) {
	args := []string{
		"ps", "-q",
		"--filter", fmt.Sprintf("label=com.dokku.orchestrate/cron-project=%s", project.Name),
		"--filter", fmt.Sprintf("label=com.dokku.orchestrate/cron-service=%s", serviceName),
		"--filter", "status=running",
	}

	resp, err := executor(ctx, ExecCommandInput{
		Command: "docker",
		Args:    args,
	})
	if err != nil {
		return false, err
	}

	return resp.StdoutContents() != "", nil
}

// randomSuffix generates a random alphanumeric string of the given length
func randomSuffix(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// Fallback to a simple timestamp-based suffix
			return fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
		}
		result[i] = charset[n.Int64()]
	}
	return string(result)
}
