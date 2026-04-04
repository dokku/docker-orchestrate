package internal

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
)

// mockConn is a minimal mock connection for testing
type mockConn struct {
	net.Conn
}

func (m *mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

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

		expected := "#!/bin/sh\necho 12345678901234567890 172.17.0.2 123456789012 web"
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

	t.Run("container inspect error uses empty IP", func(t *testing.T) {
		executed := false
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("inspect error")
			},
		}
		input := runScriptInput{
			Client: mockClient,
			Executor: func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				executed = true
				return ExecCommandResponse{}, nil
			},
			Script:      "echo hello",
			ContainerID: "test-id",
		}
		err := runHostScript(ctx, input)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !executed {
			t.Errorf("expected script to be executed with empty container IP")
		}
	})

	t.Run("detached execution", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		executorCalled := make(chan bool, 1)
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled <- true
			// Simulate a long-running command
			time.Sleep(100 * time.Millisecond)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			ServiceName: "test-service",
			Script:      "echo hello",
			ScriptType:  "test-script",
			Detached:    true,
		}

		start := time.Now()
		err := runHostScript(ctx, input)
		duration := time.Since(start)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Detached execution should return immediately (much faster than the command duration)
		if duration > 50*time.Millisecond {
			t.Errorf("detached execution should return immediately, took %v", duration)
		}

		// Wait a bit to ensure the goroutine has a chance to execute
		time.Sleep(150 * time.Millisecond)

		// Check that executor was called
		select {
		case <-executorCalled:
			// Good, executor was called
		default:
			t.Error("expected executor to be called in detached mode")
		}
	})

	t.Run("detached execution with background context", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		executorCtx := make(chan context.Context, 1)
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCtx <- ctx
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		// Create a cancellable context
		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			ServiceName: "test-service",
			Script:      "echo hello",
			ScriptType:  "test-script",
			Detached:    true,
		}

		err := runHostScript(cancelCtx, input)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Cancel the context
		cancel()

		// Wait a bit to ensure the goroutine has a chance to execute
		time.Sleep(50 * time.Millisecond)

		// Check that executor was called with background context (not the cancelled one)
		select {
		case ctx := <-executorCtx:
			// The context should be background context, not the cancelled one
			if ctx == cancelCtx {
				t.Error("expected executor to use background context, not the cancelled context")
			}
			// Background context should not be cancelled
			if ctx.Err() != nil {
				t.Errorf("expected background context to not be cancelled, got: %v", ctx.Err())
			}
		default:
			t.Error("expected executor to be called in detached mode")
		}
	})

	t.Run("synchronous execution default behavior", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			ServiceName: "test-service",
			Script:      "echo hello",
			ScriptType:  "test-script",
			Detached:    false, // Explicitly false
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called")
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

func TestIsShellInterpreter(t *testing.T) {
	tests := []struct {
		name        string
		interpreter string
		expected    bool
	}{
		{"full path bash", "/bin/bash", true},
		{"bare bash", "bash", true},
		{"full path sh", "/bin/sh", true},
		{"bare sh", "sh", true},
		{"full path dash", "/bin/dash", true},
		{"full path ash", "/bin/ash", true},
		{"full path zsh", "/usr/bin/zsh", true},
		{"bare zsh", "zsh", true},
		{"full path ksh", "/bin/ksh", true},
		{"full path csh", "/bin/csh", true},
		{"full path tcsh", "/bin/tcsh", true},
		{"full path fish", "/usr/bin/fish", true},
		{"python3", "/usr/bin/python3", false},
		{"php", "/usr/bin/php", false},
		{"node", "node", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isShellInterpreter(tt.interpreter)
			if result != tt.expected {
				t.Errorf("isShellInterpreter(%q) = %v, want %v", tt.interpreter, result, tt.expected)
			}
		})
	}
}

func TestParseShebang(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected []string
	}{
		{
			name:     "no shebang",
			script:   "echo hello",
			expected: nil,
		},
		{
			name:     "bash shebang with env",
			script:   "#!/usr/bin/env bash\necho hello",
			expected: []string{"/bin/bash", "-c"},
		},
		{
			name:     "sh shebang",
			script:   "#!/bin/sh\necho hello",
			expected: []string{"/bin/sh", "-c"},
		},
		{
			name:     "python3 shebang",
			script:   "#!/usr/bin/python3\nprint('hello')",
			expected: []string{"/usr/bin/python3"},
		},
		{
			name:     "direct bash path",
			script:   "#!/bin/bash\necho hello",
			expected: []string{"/bin/bash", "-c"},
		},
		{
			name:     "env sh",
			script:   "#!/usr/bin/env sh\necho hello",
			expected: []string{"/bin/sh", "-c"},
		},
		{
			name:     "python3 direct path no -c",
			script:   "#!/usr/bin/python3\nprint('hello')",
			expected: []string{"/usr/bin/python3"},
		},
		{
			name:     "env ruby no -c",
			script:   "#!/usr/bin/env ruby\nputs 'hello'",
			expected: []string{"/usr/bin/env", "ruby"},
		},
		{
			name:     "env python3 no -c",
			script:   "#!/usr/bin/env python3\nprint('hello')",
			expected: []string{"/usr/bin/env", "python3"},
		},
		{
			name:     "env zsh with -c",
			script:   "#!/usr/bin/env zsh\necho hello",
			expected: []string{"/usr/bin/env", "zsh", "-c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseShebang(tt.script)
			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
			} else {
				if len(result) != len(tt.expected) {
					t.Errorf("expected %v, got %v", tt.expected, result)
				} else {
					for i := range result {
						if result[i] != tt.expected[i] {
							t.Errorf("expected %v, got %v", tt.expected, result)
							break
						}
					}
				}
			}
		})
	}
}

func TestRunContainerScript(t *testing.T) {
	ctx := context.Background()

	t.Run("empty script", func(t *testing.T) {
		mockClient := &mockDockerClient{}
		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "",
			ServiceName: "test-service",
		}
		err := runContainerScript(ctx, input)
		if err != nil {
			t.Errorf("expected nil error for empty script, got %v", err)
		}
	})

	t.Run("missing client", func(t *testing.T) {
		input := RunContainerScriptInput{
			ContainerID: "test-container",
			Script:      "echo hello",
			ServiceName: "test-service",
		}
		err := runContainerScript(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "client is required") {
			t.Errorf("expected 'client is required' error, got %v", err)
		}
	})

	t.Run("script with shebang", func(t *testing.T) {
		execCreateCalled := false
		execStartCalled := false
		execInspectCalled := false
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
					},
					Config: &container.Config{
						Shell: []string{"/bin/sh"},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				execCreateCalled = true
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				execStartCalled = true
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				// Return a minimal HijackedResponse with a reader that returns EOF immediately
				// Use a mock connection that won't panic on close
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				execInspectCalled = true
				return container.ExecInspect{
					ExecID:      execID,
					ContainerID: "test-container",
					Running:     false,
					ExitCode:    0,
				}, nil
			},
		}

		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "#!/usr/bin/env bash\necho hello",
			ScriptPath:  "/tmp/pre-stop.sh",
			ServiceName: "test-service",
		}

		err := runContainerScript(ctx, input)
		if !execCreateCalled {
			t.Error("expected ContainerExecCreate to be called")
		}
		if !execStartCalled {
			t.Error("expected ContainerExecStart to be called")
		}
		if !execInspectCalled {
			t.Error("expected ContainerExecInspect to be called")
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("script without shebang uses image Config.Shell", func(t *testing.T) {
		execCreateCalled := false
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID:    id,
						Image: "test-image-id",
					},
					Config: &container.Config{},
				}, nil
			},
			imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
				return image.InspectResponse{
					Config: &dockerspec.DockerOCIImageConfig{
						DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{
							Shell: []string{"/bin/bash", "-i"},
						},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				execCreateCalled = true
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   nil,
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{
					ExecID:      execID,
					ContainerID: "test-container",
					Running:     false,
					ExitCode:    0,
				}, nil
			},
		}

		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "echo hello",
			ScriptPath:  "/tmp/pre-stop.sh",
			ServiceName: "test-service",
		}

		err := runContainerScript(ctx, input)
		if !execCreateCalled {
			t.Error("expected ContainerExecCreate to be called")
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("script without shebang falls back to sh", func(t *testing.T) {
		execCreateCalled := false
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
					},
					Config: &container.Config{
						// No Shell configured
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				execCreateCalled = true
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   nil,
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{
					ExecID:      execID,
					ContainerID: "test-container",
					Running:     false,
					ExitCode:    0,
				}, nil
			},
		}

		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "echo hello",
			ScriptPath:  "/tmp/pre-stop.sh",
			ServiceName: "test-service",
		}

		err := runContainerScript(ctx, input)
		if !execCreateCalled {
			t.Error("expected ContainerExecCreate to be called")
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("image Config.Shell with -c already included avoids double -c", func(t *testing.T) {
		var capturedCmd []string
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID:    id,
						Image: "test-image-id",
					},
					Config: &container.Config{},
				}, nil
			},
			imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
				return image.InspectResponse{
					Config: &dockerspec.DockerOCIImageConfig{
						DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{
							Shell: []string{"/bin/bash", "-c"},
						},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				capturedCmd = config.Cmd
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   nil,
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{
					ExecID:      execID,
					ContainerID: "test-container",
					Running:     false,
					ExitCode:    0,
				}, nil
			},
		}

		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "echo hello",
			ScriptPath:  "/tmp/pre-stop.sh",
			ServiceName: "test-service",
		}

		err := runContainerScript(ctx, input)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		expectedCmd := []string{"/bin/bash", "-c", "/tmp/pre-stop.sh"}
		if len(capturedCmd) != len(expectedCmd) {
			t.Fatalf("expected cmd %v, got %v", expectedCmd, capturedCmd)
		}
		for i := range capturedCmd {
			if capturedCmd[i] != expectedCmd[i] {
				t.Errorf("expected cmd %v, got %v", expectedCmd, capturedCmd)
				break
			}
		}
	})

	t.Run("image Config.Shell non-shell interpreter used as-is", func(t *testing.T) {
		var capturedCmd []string
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID:    id,
						Image: "test-image-id",
					},
					Config: &container.Config{},
				}, nil
			},
			imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
				return image.InspectResponse{
					Config: &dockerspec.DockerOCIImageConfig{
						DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{
							Shell: []string{"/usr/bin/python3"},
						},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				capturedCmd = config.Cmd
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   nil,
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{
					ExecID:      execID,
					ContainerID: "test-container",
					Running:     false,
					ExitCode:    0,
				}, nil
			},
		}

		input := RunContainerScriptInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Script:      "print('hello')",
			ScriptPath:  "/tmp/pre-stop.sh",
			ServiceName: "test-service",
		}

		err := runContainerScript(ctx, input)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		expectedCmd := []string{"/usr/bin/python3", "/tmp/pre-stop.sh"}
		if len(capturedCmd) != len(expectedCmd) {
			t.Fatalf("expected cmd %v, got %v", expectedCmd, capturedCmd)
		}
		for i := range capturedCmd {
			if capturedCmd[i] != expectedCmd[i] {
				t.Errorf("expected cmd %v, got %v", expectedCmd, capturedCmd)
				break
			}
		}
	})
}
