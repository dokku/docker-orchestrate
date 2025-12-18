package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func TestErrorWithOutput(t *testing.T) {
	err := &ErrorWithOutput{
		Err:    errors.New("some error"),
		Output: "some output",
	}

	if err.Error() != "some error" {
		t.Errorf("expected 'some error', got '%s'", err.Error())
	}
	if err.Output != "some output" {
		t.Errorf("expected 'some output', got '%s'", err.Output)
	}
}

func TestRunStopCommand(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		stopCommand string
		expectError bool
	}{
		{
			name:        "successful_stop_command",
			stopCommand: "echo 'stopping'",
			expectError: false,
		},
		{
			name:        "failing_stop_command",
			stopCommand: "exit 1",
			expectError: true,
		},
		{
			name:        "no_stop_command",
			stopCommand: "",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockDockerClient{
				containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							ID: id,
							State: &container.State{
								Running: true,
							},
							HostConfig: &container.HostConfig{
								NetworkMode: "bridge",
							},
						},
						NetworkSettings: &container.NetworkSettings{
							Networks: map[string]*network.EndpointSettings{
								"bridge": {
									IPAddress: "172.17.0.2",
								},
							},
						},
					}, nil
				},
			}

			executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				if tt.stopCommand == "exit 1" {
					return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("command failed")
				}
				return ExecCommandResponse{ExitCode: 0}, nil
			}

			input := RunStopCommandInput{
				Client:      mockClient,
				ContainerID: "test-container-id-123456",
				Executor:    executor,
				ServiceName: "test-service",
				StopCommand: tt.stopCommand,
			}

			err := runStopCommand(ctx, input)
			if (err != nil) != tt.expectError {
				t.Errorf("runStopCommand() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestWaitForScriptHealthcheck(t *testing.T) {
	ctx := context.Background()

	t.Run("successful script healthcheck", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
						HostConfig: &container.HostConfig{
							NetworkMode: "bridge",
						},
					},
					NetworkSettings: &container.NetworkSettings{
						Networks: map[string]*network.EndpointSettings{
							"bridge": {
								IPAddress: "172.17.0.2",
							},
						},
					},
				}, nil
			},
		}

		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			if !strings.Contains(input.Command, "healthcheck-") {
				t.Errorf("expected command to be a temp file, got %s", input.Command)
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := WaitForHealthcheckInput{
			Client:             mockClient,
			ContainerID:        "test-container-id",
			Executor:           executor,
			HealthcheckCommand: "curl -f http://{{.ContainerIP}}:8080/health",
			ServiceName:        "web",
		}

		err := waitForScriptHealthcheck(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called")
		}
	})

	t.Run("failing script healthcheck", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{
							NetworkMode: "host",
						},
					},
				}, nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.StderrWriter != nil {
				_, _ = input.StderrWriter.Write([]byte("connection refused"))
			}
			return ExecCommandResponse{
				ExitCode: 1,
				Stderr:   "connection refused",
			}, errors.New("exit status 1")
		}

		input := WaitForHealthcheckInput{
			Client:             mockClient,
			ContainerID:        "test-container-id",
			Executor:           executor,
			HealthcheckCommand: "exit 1",
		}

		err := waitForScriptHealthcheck(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		eo, ok := err.(*ErrorWithOutput)
		if !ok {
			t.Fatalf("expected ErrorWithOutput, got %T", err)
		}
		if !strings.Contains(eo.Output, "connection refused") {
			t.Errorf("expected output to contain 'connection refused', got '%s'", eo.Output)
		}
	})
}

func TestWaitForDockerHealthCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("container becomes healthy", func(t *testing.T) {
		callCount := 0
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				callCount++
				status := "starting"
				if callCount > 1 {
					status = "healthy"
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{
								Status: status,
							},
						},
					},
				}, nil
			},
		}

		tickerCh := make(chan time.Time, 2)
		tickerCh <- time.Now()
		tickerCh <- time.Now()

		input := WaitForHealthcheckInput{
			Client:      mockClient,
			ContainerID: "test-id",
			Monitor:     1 * time.Second,
			TickerCh:    tickerCh,
		}

		err := waitForDockerHealthCheck(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls, got %d", callCount)
		}
	})

	t.Run("container is unhealthy", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{
								Status: "unhealthy",
							},
						},
					},
				}, nil
			},
		}

		tickerCh := make(chan time.Time, 1)
		tickerCh <- time.Now()

		input := WaitForHealthcheckInput{
			Client:      mockClient,
			ContainerID: "test-id",
			Monitor:     1 * time.Second,
			TickerCh:    tickerCh,
		}

		err := waitForDockerHealthCheck(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "container is unhealthy") {
			t.Errorf("expected 'container is unhealthy', got '%v'", err)
		}
	})

	t.Run("container not running no health check", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false,
						},
					},
				}, nil
			},
		}

		tickerCh := make(chan time.Time, 1)
		tickerCh <- time.Now()

		input := WaitForHealthcheckInput{
			Client:      mockClient,
			ContainerID: "test-id",
			Monitor:     1 * time.Second,
			TickerCh:    tickerCh,
		}

		err := waitForDockerHealthCheck(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "container is not running") {
			t.Errorf("expected 'container is not running', got '%v'", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{
								Status: "starting",
							},
						},
					},
				}, nil
			},
		}

		tickerCh := make(chan time.Time, 1)
		tickerCh <- time.Now()

		input := WaitForHealthcheckInput{
			Client:      mockClient,
			ContainerID: "test-id",
			Monitor:     1 * time.Nanosecond, // Tiny monitor to trigger timeout
			TickerCh:    tickerCh,
		}

		// Ensure deadline has passed
		time.Sleep(10 * time.Millisecond)

		err := waitForDockerHealthCheck(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "health check timeout") {
			t.Errorf("expected timeout error, got '%v'", err)
		}
	})
}
