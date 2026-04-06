package internal

import (
	"context"
	"fmt"
	"time"
)

// ErrorWithOutput is an error with output
type ErrorWithOutput struct {
	Err    error
	Output string
}

// Error returns the error message
func (e *ErrorWithOutput) Error() string {
	return e.Err.Error()
}

// WaitForDockerHealthCheckInput is the input for the waitForDockerHealthCheck function
type WaitForHealthcheckInput struct {
	// Client is the Docker client to use. If nil, a new one will be created.
	Client DockerClientInterface
	// ContainerID is the ID of the container to wait for
	ContainerID string
	// Executor is the command executor to use. If nil, ExecCommand will be used.
	Executor CommandExecutor
	// HealthcheckCommand is the command to run for health checks
	HealthcheckCommand string
	// Monitor is the health check monitoring duration
	Monitor time.Duration
	// ProjectName is the name of the project
	ProjectName string
	// ServiceName is the name of the service
	ServiceName string
	// TickerCh is an optional channel to use for ticking. If nil, time.NewTicker will be used.
	TickerCh <-chan time.Time
	// HealthcheckWait is the duration to wait before considering a container without a Docker healthcheck as healthy.
	// The container must be continuously running for this duration. If it restarts, the timer resets.
	HealthcheckWait time.Duration
}

// waitForHealthcheck waits for a container to become healthy using both Docker and script health checks
func waitForHealthcheck(ctx context.Context, input WaitForHealthcheckInput) error {
	if input.Client == nil {
		return fmt.Errorf("client is required")
	}

	if input.Executor == nil {
		return fmt.Errorf("executor is required")
	}

	if err := waitForDockerHealthCheck(ctx, input); err != nil {
		return err
	}

	return runHostScript(ctx, runScriptInput{
		Client:      input.Client,
		ContainerID: input.ContainerID,
		Executor:    input.Executor,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Script:      input.HealthcheckCommand,
		ScriptType:  "healthcheck",
	})
}

// waitForDockerHealthCheck waits for a container to become healthy
func waitForDockerHealthCheck(ctx context.Context, input WaitForHealthcheckInput) error {
	if input.Monitor == 0 {
		input.Monitor = 1 * time.Millisecond
	}

	maxWaitTime := input.Monitor * 2
	if input.HealthcheckWait > 0 {
		maxWaitTime = (input.Monitor + input.HealthcheckWait) * 2
	}
	deadline := time.Now().Add(maxWaitTime)

	tickerCh := input.TickerCh
	var ticker *time.Ticker
	if tickerCh == nil {
		ticker = time.NewTicker(input.Monitor)
		defer ticker.Stop()
		tickerCh = ticker.C
	}

	var healthcheckWaitStart time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tickerCh:
			if time.Now().After(deadline) {
				return fmt.Errorf("health check timeout after %v", maxWaitTime)
			}

			containerJSON, err := input.Client.ContainerInspect(ctx, input.ContainerID)
			if err != nil {
				return fmt.Errorf("error inspecting container: %v", err)
			}

			// If no health check is configured, consider it healthy if running
			if containerJSON.State.Health == nil {
				if !containerJSON.State.Running {
					if input.HealthcheckWait > 0 {
						healthcheckWaitStart = time.Time{} // reset timer on restart
						continue                           // keep polling
					}
					return fmt.Errorf("container is not running")
				}

				// Container is running
				if input.HealthcheckWait <= 0 {
					return nil // no wait configured, immediately healthy
				}

				if healthcheckWaitStart.IsZero() {
					healthcheckWaitStart = time.Now()
				}

				if time.Since(healthcheckWaitStart) >= input.HealthcheckWait {
					return nil // waited long enough, healthy
				}

				continue // keep polling
			}

			healthStatus := containerJSON.State.Health.Status
			switch healthStatus {
			case "healthy":
				return nil
			case "unhealthy":
				return fmt.Errorf("container is unhealthy")
			case "starting":
				// Continue waiting
			default:
				// Continue waiting for other states
			}
		}
	}
}
