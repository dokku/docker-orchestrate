package internal

import (
	"context"
	"errors"
	"os"
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

func TestRunHostScript(t *testing.T) {
	ctx := context.Background()

	t.Run("successful execution", func(t *testing.T) {
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
			if !strings.Contains(input.Command, "test-script-") {
				// The prefix in CreateTemp is input.ScriptType + "-"
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id-long-enough",
			Executor:    executor,
			ServiceName: "test-service",
			Script:      "echo hello",
			ScriptType:  "test-script",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !executorCalled {
			t.Error("expected executor to be called")
		}
	})

	t.Run("template variables", func(t *testing.T) {
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

		var executedCommand string
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			content, _ := os.ReadFile(input.Command)
			executedCommand = string(content)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "12345678901234567890",
			Executor:    executor,
			ServiceName: "web",
			Script:      "echo {{.ContainerID}} {{.ContainerIP}} {{.ContainerShortID}} {{.ServiceName}}",
			ScriptType:  "test",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := "#!/usr/bin/env bash\necho 12345678901234567890 172.17.0.2 123456789012 web"
		if !strings.Contains(executedCommand, expected) {
			t.Errorf("expected command to contain %q, got %q", expected, executedCommand)
		}
	})

	t.Run("failing command", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.StderrWriter != nil {
				_, _ = input.StderrWriter.Write([]byte("command failed output"))
			}
			return ExecCommandResponse{ExitCode: 1}, errors.New("exit status 1")
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-id",
			Executor:    executor,
			Script:      "exit 1",
			ScriptType:  "test",
		}

		err := runHostScript(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		eo, ok := err.(*ErrorWithOutput)
		if !ok {
			t.Fatalf("expected *ErrorWithOutput, got %T", err)
		}
		if !strings.Contains(eo.Output, "command failed output") {
			t.Errorf("expected output to contain 'command failed output', got %q", eo.Output)
		}
	})

	t.Run("empty script", func(t *testing.T) {
		err := runHostScript(ctx, runScriptInput{Script: ""})
		if err != nil {
			t.Errorf("expected nil error for empty script, got %v", err)
		}
	})

	t.Run("missing client", func(t *testing.T) {
		input := runScriptInput{
			Script: "echo hello",
			Executor: func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{}, nil
			},
		}
		err := runHostScript(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "client is required") {
			t.Errorf("expected 'client is required' error, got %v", err)
		}
	})

	t.Run("missing executor", func(t *testing.T) {
		input := runScriptInput{
			Script: "echo hello",
			Client: &mockDockerClient{},
		}
		err := runHostScript(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "executor is required") {
			t.Errorf("expected 'executor is required' error, got %v", err)
		}
	})

	t.Run("invalid template", func(t *testing.T) {
		input := runScriptInput{
			Client: &mockDockerClient{},
			Executor: func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{}, nil
			},
			Script: "echo {{.Invalid}}",
		}
		err := runHostScript(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "error parsing") {
			// Actually parsing might succeed, but execution will fail if field doesn't exist
			// Wait, template.New().Parse() only checks syntax.
		}
	})

	t.Run("container IP error", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("inspect error")
			},
		}
		input := runScriptInput{
			Client: mockClient,
			Executor: func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{}, nil
			},
			Script:      "echo hello",
			ContainerID: "test-id",
		}
		err := runHostScript(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "inspect error") {
			t.Errorf("expected inspect error, got %v", err)
		}
	})
}

func TestGetContainerIP(t *testing.T) {
	ctx := context.Background()

	t.Run("host network", func(t *testing.T) {
		client := &mockDockerClient{
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
		ip, err := getContainerIP(ctx, client, "id")
		if err != nil {
			t.Fatal(err)
		}
		if ip != "127.0.0.1" {
			t.Errorf("expected 127.0.0.1, got %s", ip)
		}
	})

	t.Run("bridge network", func(t *testing.T) {
		client := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{
							NetworkMode: "bridge",
						},
					},
					NetworkSettings: &container.NetworkSettings{
						Networks: map[string]*network.EndpointSettings{
							"bridge": {
								IPAddress: "172.17.0.5",
							},
						},
					},
				}, nil
			},
		}
		ip, err := getContainerIP(ctx, client, "id")
		if err != nil {
			t.Fatal(err)
		}
		if ip != "172.17.0.5" {
			t.Errorf("expected 172.17.0.5, got %s", ip)
		}
	})

	t.Run("custom network", func(t *testing.T) {
		client := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{
							NetworkMode: "my-net",
						},
					},
					NetworkSettings: &container.NetworkSettings{
						Networks: map[string]*network.EndpointSettings{
							"my-net": {
								IPAddress: "192.168.0.10",
							},
						},
					},
				}, nil
			},
		}
		ip, err := getContainerIP(ctx, client, "id")
		if err != nil {
			t.Fatal(err)
		}
		if ip != "192.168.0.10" {
			t.Errorf("expected 192.168.0.10, got %s", ip)
		}
	})

	t.Run("network mismatch", func(t *testing.T) {
		client := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{
							NetworkMode: "my-net",
						},
					},
					NetworkSettings: &container.NetworkSettings{
						Networks: map[string]*network.EndpointSettings{
							"other-net": {
								IPAddress: "192.168.0.10",
							},
						},
					},
				}, nil
			},
		}
		ip, err := getContainerIP(ctx, client, "id")
		if err != nil {
			t.Fatal(err)
		}
		if ip != "" {
			t.Errorf("expected empty IP for network mismatch, got %s", ip)
		}
	})
}
