package internal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/template"
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

// HealthcheckCommandTemplateData is the data structure for the healthcheck command template
type HealthcheckCommandTemplateData struct {
	// ContainerID is the ID of the container
	ContainerID string
	// ContainerIP is the IP address of the container
	ContainerIP string
	// ContainerShortID is the short ID of the container
	ContainerShortID string
	// ServiceName is the name of the service
	ServiceName string
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
	// ServiceName is the name of the service
	ServiceName string
	// TickerCh is an optional channel to use for ticking. If nil, time.NewTicker will be used.
	TickerCh <-chan time.Time
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

	return waitForScriptHealthcheck(ctx, input)
}

// waitForDockerHealthCheck waits for a container to become healthy
func waitForDockerHealthCheck(ctx context.Context, input WaitForHealthcheckInput) error {
	if input.Monitor == 0 {
		input.Monitor = 1 * time.Millisecond
	}

	maxWaitTime := input.Monitor * 2
	deadline := time.Now().Add(maxWaitTime)

	tickerCh := input.TickerCh
	var ticker *time.Ticker
	if tickerCh == nil {
		ticker = time.NewTicker(input.Monitor)
		defer ticker.Stop()
		tickerCh = ticker.C
	}

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
				if containerJSON.State.Running {
					return nil
				}
				return fmt.Errorf("container is not running")
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

// waitForScriptHealthcheck waits for a script based health check to become healthy
func waitForScriptHealthcheck(ctx context.Context, input WaitForHealthcheckInput) error {
	if input.HealthcheckCommand == "" {
		return nil
	}

	tmpl, err := template.New("healthcheck-command").Parse(input.HealthcheckCommand)
	if err != nil {
		return fmt.Errorf("error parsing healthcheck command template: %v", err)
	}

	containerJSON, err := input.Client.ContainerInspect(ctx, input.ContainerID)
	if err != nil {
		return fmt.Errorf("error inspecting container: %v", err)
	}

	containerIP := ""
	if containerJSON.HostConfig.NetworkMode.IsHost() {
		containerIP = "127.0.0.1"
	} else {
		for networkName, network := range containerJSON.NetworkSettings.Networks {
			if networkName != containerJSON.HostConfig.NetworkMode.NetworkName() {
				continue
			}
			if network.IPAddress != "" {
				containerIP = network.IPAddress
				break
			}
		}
	}

	var commandBuf bytes.Buffer
	data := HealthcheckCommandTemplateData{
		ContainerID:      input.ContainerID,
		ContainerIP:      containerIP,
		ContainerShortID: input.ContainerID[:12],
		ServiceName:      input.ServiceName,
	}

	if err := tmpl.Execute(&commandBuf, data); err != nil {
		return fmt.Errorf("error executing healthcheck command template: %v", err)
	}

	command := commandBuf.String()
	if !strings.HasPrefix(command, "#!") {
		command = "#!/usr/bin/env bash\n" + command
	}

	tempFile, err := os.CreateTemp("", "healthcheck-*.script")
	if err != nil {
		return fmt.Errorf("error creating temporary healthcheck script: %v", err)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(command); err != nil {
		return fmt.Errorf("error writing healthcheck command to temporary file: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("error closing temporary healthcheck file: %v", err)
	}

	if err := os.Chmod(tempFile.Name(), 0755); err != nil {
		return fmt.Errorf("error making temporary healthcheck script executable: %v", err)
	}

	var output bytes.Buffer
	_, err = input.Executor(ctx, ExecCommandInput{
		Command:          tempFile.Name(),
		StdoutWriter:     &output,
		StderrWriter:     &output,
		WorkingDirectory: os.TempDir(),
	})
	if err != nil {
		return &ErrorWithOutput{
			Err:    fmt.Errorf("healthcheck command failed for container %s: %v", input.ContainerID[:12], err),
			Output: strings.TrimSpace(output.String()),
		}
	}

	return nil
}
