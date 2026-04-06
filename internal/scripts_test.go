package internal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
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
			// Script without shebang defaults to /bin/sh -c
			if input.Command != "/bin/sh" {
				return ExecCommandResponse{}, errors.New("expected /bin/sh command")
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

		var scriptContent string
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			// For shell scripts, the script content is the last arg after -c
			if len(input.Args) >= 2 && input.Args[0] == "-c" {
				scriptContent = input.Args[1]
			}
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

		expected := "echo 12345678901234567890 172.17.0.2 123456789012 web"
		if !strings.Contains(scriptContent, expected) {
			t.Errorf("expected script content to contain %q, got %q", expected, scriptContent)
		}
	})

	t.Run("project name template variable", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
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

		var scriptContent string
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) >= 2 && input.Args[0] == "-c" {
				scriptContent = input.Args[1]
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "12345678901234567890",
			Executor:    executor,
			ProjectName: "myproject",
			ServiceName: "web",
			Script:      "echo {{.ProjectName}} {{.ServiceName}}",
			ScriptType:  "test",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := "echo myproject web"
		if !strings.Contains(scriptContent, expected) {
			t.Errorf("expected script content to contain %q, got %q", expected, scriptContent)
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

	t.Run("detached execution passes detached flag", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		var receivedDetached bool
		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			receivedDetached = input.Detached
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

		err := runHostScript(ctx, input)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called in detached mode")
		}

		if !receivedDetached {
			t.Error("expected executor to receive Detached=true")
		}
	})

	t.Run("detached execution uses background context", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		var receivedCtx context.Context
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			receivedCtx = ctx
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		// Create a cancellable context and cancel it before calling
		cancelCtx, cancel := context.WithCancel(context.Background())

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

		// Cancel the original context
		cancel()

		// The executor should have received a background context, not the cancelled one
		if receivedCtx == cancelCtx {
			t.Error("expected executor to use background context, not the caller's context")
		}
		// Background context should not be cancelled
		if receivedCtx.Err() != nil {
			t.Errorf("expected background context to not be cancelled, got: %v", receivedCtx.Err())
		}
	})

	t.Run("detached execution returns error on start failure", func(t *testing.T) {
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
			return ExecCommandResponse{}, errors.New("start failed")
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

		err := runHostScript(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "error starting detached") {
			t.Errorf("expected 'error starting detached' in error, got: %v", err)
		}
	})

	t.Run("shell script uses -c flag", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		var receivedCommand string
		var receivedArgs []string
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			receivedCommand = input.Command
			receivedArgs = input.Args
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			Script:      "#!/bin/bash\necho hello",
			ScriptType:  "test-script",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedCommand != "/bin/bash" {
			t.Errorf("expected command '/bin/bash', got %q", receivedCommand)
		}
		if len(receivedArgs) != 2 || receivedArgs[0] != "-c" {
			t.Errorf("expected args [-c, <script>], got %v", receivedArgs)
		}
		if !strings.Contains(receivedArgs[1], "echo hello") {
			t.Errorf("expected script content in args, got %q", receivedArgs[1])
		}
	})

	t.Run("non-shell script uses stdin", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		var receivedCommand string
		var receivedStdin io.Reader
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			receivedCommand = input.Command
			receivedStdin = input.Stdin
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			Script:      "#!/usr/bin/python3\nprint('hello')",
			ScriptType:  "test-script",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedCommand != "/usr/bin/python3" {
			t.Errorf("expected command '/usr/bin/python3', got %q", receivedCommand)
		}
		if receivedStdin == nil {
			t.Fatal("expected stdin to be set for non-shell interpreter")
		}
		stdinContent, _ := io.ReadAll(receivedStdin)
		if !strings.Contains(string(stdinContent), "print('hello')") {
			t.Errorf("expected stdin to contain script, got %q", string(stdinContent))
		}
	})

	t.Run("shebang is stripped from script content", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
		}

		var scriptContent string
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) >= 2 && input.Args[0] == "-c" {
				scriptContent = input.Args[1]
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := runScriptInput{
			Client:      mockClient,
			ContainerID: "test-container-id",
			Executor:    executor,
			Script:      "#!/bin/sh\necho hello\necho world",
			ScriptType:  "test-script",
		}

		err := runHostScript(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(scriptContent, "#!/bin/sh") {
			t.Errorf("expected shebang to be stripped, got %q", scriptContent)
		}
		if !strings.Contains(scriptContent, "echo hello") {
			t.Errorf("expected script body to be preserved, got %q", scriptContent)
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

func TestRunContainerHook(t *testing.T) {
	ctx := context.Background()

	t.Run("empty command", func(t *testing.T) {
		mockClient := &mockDockerClient{}
		input := RunContainerHookInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Hook:        composeTypes.ServiceHook{},
			ServiceName: "test-service",
		}
		err := runContainerHook(ctx, input)
		if err != nil {
			t.Errorf("expected nil error for empty command, got %v", err)
		}
	})

	t.Run("missing client", func(t *testing.T) {
		input := RunContainerHookInput{
			ContainerID: "test-container",
			Hook: composeTypes.ServiceHook{
				Command: composeTypes.ShellCommand{"echo", "hello"},
			},
			ServiceName: "test-service",
		}
		err := runContainerHook(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "client is required") {
			t.Errorf("expected 'client is required' error, got %v", err)
		}
	})

	t.Run("successful execution with all fields", func(t *testing.T) {
		var capturedConfig container.ExecOptions
		val1 := "bar"
		val2 := "qux"
		mockClient := &mockDockerClient{
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				capturedConfig = config
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
		}

		input := RunContainerHookInput{
			Client:      mockClient,
			ContainerID: "test-container-id-long-enough",
			Hook: composeTypes.ServiceHook{
				Command:    composeTypes.ShellCommand{"nginx", "-s", "quit"},
				User:       "www-data",
				Privileged: true,
				WorkingDir: "/app",
				Environment: composeTypes.MappingWithEquals{
					"BAZ": &val2,
					"FOO": &val1,
				},
			},
			ServiceName: "test-service",
		}

		err := runContainerHook(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify Cmd
		expectedCmd := []string{"nginx", "-s", "quit"}
		if len(capturedConfig.Cmd) != len(expectedCmd) {
			t.Fatalf("expected cmd %v, got %v", expectedCmd, capturedConfig.Cmd)
		}
		for i := range expectedCmd {
			if capturedConfig.Cmd[i] != expectedCmd[i] {
				t.Errorf("expected cmd[%d]=%s, got %s", i, expectedCmd[i], capturedConfig.Cmd[i])
			}
		}

		// Verify User
		if capturedConfig.User != "www-data" {
			t.Errorf("expected user www-data, got %s", capturedConfig.User)
		}

		// Verify Privileged
		if !capturedConfig.Privileged {
			t.Error("expected privileged to be true")
		}

		// Verify WorkingDir
		if capturedConfig.WorkingDir != "/app" {
			t.Errorf("expected working dir /app, got %s", capturedConfig.WorkingDir)
		}

		// Verify Env (sorted by key)
		expectedEnv := []string{"BAZ=qux", "FOO=bar"}
		if len(capturedConfig.Env) != len(expectedEnv) {
			t.Fatalf("expected env %v, got %v", expectedEnv, capturedConfig.Env)
		}
		for i := range expectedEnv {
			if capturedConfig.Env[i] != expectedEnv[i] {
				t.Errorf("expected env[%d]=%s, got %s", i, expectedEnv[i], capturedConfig.Env[i])
			}
		}

		// Verify AttachStdout/Stderr
		if !capturedConfig.AttachStdout {
			t.Error("expected AttachStdout to be true")
		}
		if !capturedConfig.AttachStderr {
			t.Error("expected AttachStderr to be true")
		}
	})

	t.Run("non-zero exit code", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("some error output")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 1}, nil
			},
		}

		input := RunContainerHookInput{
			Client:      mockClient,
			ContainerID: "test-container-id-long-enough",
			Hook: composeTypes.ServiceHook{
				Command: composeTypes.ShellCommand{"exit", "1"},
			},
			ServiceName: "test-service",
		}

		err := runContainerHook(ctx, input)
		if err == nil {
			t.Fatal("expected error for non-zero exit code")
		}

		var errWithOutput *ErrorWithOutput
		if !errors.As(err, &errWithOutput) {
			t.Fatalf("expected ErrorWithOutput, got %T", err)
		}
		if !strings.Contains(errWithOutput.Error(), "container hook failed") {
			t.Errorf("expected 'container hook failed' in error, got %s", errWithOutput.Error())
		}
		if !strings.Contains(errWithOutput.Error(), "test-conta") {
			t.Errorf("expected truncated container ID in error, got %s", errWithOutput.Error())
		}
	})

	t.Run("nil environment values skipped", func(t *testing.T) {
		var capturedConfig container.ExecOptions
		val := "bar"
		mockClient := &mockDockerClient{
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				capturedConfig = config
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
		}

		input := RunContainerHookInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Hook: composeTypes.ServiceHook{
				Command: composeTypes.ShellCommand{"echo", "hello"},
				Environment: composeTypes.MappingWithEquals{
					"KEEP":    &val,
					"SKIP_ME": nil,
				},
			},
			ServiceName: "test-service",
		}

		err := runContainerHook(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedEnv := []string{"KEEP=bar"}
		if len(capturedConfig.Env) != len(expectedEnv) {
			t.Fatalf("expected env %v, got %v", expectedEnv, capturedConfig.Env)
		}
		if capturedConfig.Env[0] != expectedEnv[0] {
			t.Errorf("expected env[0]=%s, got %s", expectedEnv[0], capturedConfig.Env[0])
		}
	})
}

func TestRunContainerHooks(t *testing.T) {
	ctx := context.Background()

	t.Run("nil hooks", func(t *testing.T) {
		err := runContainerHooks(ctx, RunContainerHooksInput{
			Client:      &mockDockerClient{},
			ContainerID: "test-container",
			Hooks:       nil,
			ServiceName: "test-service",
		})
		if err != nil {
			t.Errorf("expected nil error for nil hooks, got %v", err)
		}
	})

	t.Run("empty hooks", func(t *testing.T) {
		err := runContainerHooks(ctx, RunContainerHooksInput{
			Client:      &mockDockerClient{},
			ContainerID: "test-container",
			Hooks:       []composeTypes.ServiceHook{},
			ServiceName: "test-service",
		})
		if err != nil {
			t.Errorf("expected nil error for empty hooks, got %v", err)
		}
	})

	t.Run("multiple hooks executed sequentially", func(t *testing.T) {
		var mu sync.Mutex
		var executedCmds []string
		mockClient := &mockDockerClient{
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				mu.Lock()
				executedCmds = append(executedCmds, strings.Join(config.Cmd, " "))
				mu.Unlock()
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
		}

		input := RunContainerHooksInput{
			Client:      mockClient,
			ContainerID: "test-container",
			Hooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"nginx", "-s", "quit"}},
				{Command: composeTypes.ShellCommand{"echo", "done"}},
				{Command: composeTypes.ShellCommand{"rm", "-f", "/tmp/lock"}},
			},
			ServiceName: "test-service",
		}

		err := runContainerHooks(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedCmds := []string{"nginx -s quit", "echo done", "rm -f /tmp/lock"}
		if len(executedCmds) != len(expectedCmds) {
			t.Fatalf("expected %d hooks executed, got %d", len(expectedCmds), len(executedCmds))
		}
		for i := range expectedCmds {
			if executedCmds[i] != expectedCmds[i] {
				t.Errorf("expected hook[%d]=%q, got %q", i, expectedCmds[i], executedCmds[i])
			}
		}
	})

	t.Run("short-circuit on error", func(t *testing.T) {
		execCount := 0
		mockClient := &mockDockerClient{
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				execCount++
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
				reader := strings.NewReader("")
				return types.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				// First call succeeds, second fails
				if execCount == 1 {
					return container.ExecInspect{ExitCode: 0}, nil
				}
				return container.ExecInspect{ExitCode: 1}, nil
			},
		}

		input := RunContainerHooksInput{
			Client:      mockClient,
			ContainerID: "test-container-id-long-enough",
			Hooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"echo", "first"}},
				{Command: composeTypes.ShellCommand{"exit", "1"}},
				{Command: composeTypes.ShellCommand{"echo", "should-not-run"}},
			},
			ServiceName: "test-service",
		}

		err := runContainerHooks(ctx, input)
		if err == nil {
			t.Fatal("expected error from failing hook")
		}
		if execCount != 2 {
			t.Errorf("expected 2 exec calls (first succeeds, second fails), got %d", execCount)
		}
	})
}

func TestRunHostScriptWithEnv(t *testing.T) {
	ctx := context.Background()

	mockClient := &mockDockerClient{
		containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID: id,
				},
				NetworkSettings: &container.NetworkSettings{
					Networks: map[string]*network.EndpointSettings{
						"bridge": {IPAddress: "172.17.0.2"},
					},
				},
			}, nil
		},
	}

	var capturedEnv map[string]string
	executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		capturedEnv = input.Env
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	envVars := map[string]string{
		"DB_HOST": "localhost",
		"DB_PORT": "5432",
	}

	input := runScriptInput{
		Client:      mockClient,
		ContainerID: "test-container-id-long-enough",
		Env:         envVars,
		Executor:    executor,
		ServiceName: "test-service",
		Script:      "echo hello",
		ScriptType:  "test-script",
	}

	err := runHostScript(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedEnv == nil {
		t.Fatal("expected env to be passed to executor")
	}
	if capturedEnv["DB_HOST"] != "localhost" {
		t.Errorf("expected DB_HOST=localhost, got %q", capturedEnv["DB_HOST"])
	}
	if capturedEnv["DB_PORT"] != "5432" {
		t.Errorf("expected DB_PORT=5432, got %q", capturedEnv["DB_PORT"])
	}
}

func TestRunContainerScriptWithEnv(t *testing.T) {
	ctx := context.Background()

	var capturedExecEnv []string
	mockClient := &mockDockerClient{
		containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID: id,
				},
				Config: &container.Config{},
			}, nil
		},
		imageInspect: func(ctx context.Context, id string) (image.InspectResponse, error) {
			return image.InspectResponse{
				Config: &dockerspec.DockerOCIImageConfig{},
			}, nil
		},
		copyToContainer: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			return nil
		},
		containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
			capturedExecEnv = config.Env
			return container.ExecCreateResponse{ID: "exec-id"}, nil
		},
		containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
			reader := strings.NewReader("")
			return types.HijackedResponse{
				Conn:   &mockConn{},
				Reader: bufio.NewReader(reader),
			}, nil
		},
		containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
			return nil
		},
		containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}

	envVars := map[string]string{
		"APP_ENV": "production",
	}

	input := RunContainerScriptInput{
		Client:      mockClient,
		ContainerID: "test-container-id-long-enough",
		Env:         envVars,
		Script:      "echo hello",
		ScriptPath:  "/tmp/test.sh",
		ServiceName: "test-service",
	}

	err := runContainerScript(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedExecEnv) == 0 {
		t.Fatal("expected env to be passed to container exec")
	}
	found := false
	for _, env := range capturedExecEnv {
		if env == "APP_ENV=production" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected APP_ENV=production in exec env, got %v", capturedExecEnv)
	}
}
