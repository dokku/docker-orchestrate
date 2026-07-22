package internal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/rs/zerolog"
)

func testLogger() (*command.ZerologUi, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}
	return logger, &buf
}

func TestRunServiceNotFound(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name: "web",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for missing service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' in error, got: %s", err.Error())
	}
}

func TestRunServiceNotOneShot(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:    "web",
				Restart: "always",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "web",
	})
	if err == nil {
		t.Fatal("expected error for non-one-shot service")
	}
	if !strings.Contains(err.Error(), "not a one-shot service") {
		t.Fatalf("expected 'not a one-shot service' in error, got: %s", err.Error())
	}
}

func TestRunServiceSuccess(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands [][]string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedCommands = append(executedCommands, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the run command was called
	found := false
	for _, args := range executedCommands {
		argsStr := strings.Join(args, " ")
		if strings.Contains(argsStr, "run") && strings.Contains(argsStr, "--no-deps") && strings.Contains(argsStr, "migrate") {
			found = true
			// Verify --rm is NOT present (NoRemove=true for run)
			if strings.Contains(argsStr, "--rm") {
				t.Errorf("expected no --rm flag for run command, got: %s", argsStr)
			}
			// Verify labels are present
			if !strings.Contains(argsStr, "com.dokku.orchestrate/run=true") {
				t.Errorf("expected run label, got: %s", argsStr)
			}
			// Verify --name is present
			if !strings.Contains(argsStr, "--name") {
				t.Errorf("expected --name flag, got: %s", argsStr)
			}
			break
		}
	}
	if !found {
		t.Error("expected docker compose run command to be executed")
	}
}

func TestRunServiceStreamsOutput(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var streamStdioUsed bool
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		argsStr := strings.Join(input.Args, " ")
		if strings.Contains(argsStr, "run") && strings.Contains(argsStr, "--no-deps") {
			streamStdioUsed = input.StreamStdio
		}
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !streamStdioUsed {
		t.Error("expected StreamStdio to be true for foreground run")
	}
}

func TestRunServiceFailure(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		argsStr := strings.Join(input.Args, " ")
		if strings.Contains(argsStr, "run") {
			return ExecCommandResponse{ExitCode: 1}, errors.New("exit status 1")
		}
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err == nil {
		t.Fatal("expected error for failed service")
	}
}

func TestRunServiceDetached(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands [][]string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedCommands = append(executedCommands, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"export": types.ServiceConfig{
				Name:    "export",
				Restart: "no",
				Image:   "myapp:latest",
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Detach:       true,
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "export",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify detach flag is present
	found := false
	for _, args := range executedCommands {
		argsStr := strings.Join(args, " ")
		if strings.Contains(argsStr, "run") && strings.Contains(argsStr, "-d") {
			found = true
			// Verify labels are present
			if !strings.Contains(argsStr, "com.dokku.orchestrate/run=true") {
				t.Errorf("expected run label in detached mode, got: %s", argsStr)
			}
			break
		}
	}
	if !found {
		t.Error("expected docker compose run with -d flag")
	}
}

func TestRunServicePreDeployHook(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands []string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedCommands = append(executedCommands, input.Command+" "+strings.Join(input.Args, " "))
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Deploy: &types.DeployConfig{
					Extensions: map[string]interface{}{
						"x-pre-deploy-host-command": "echo pre-deploy",
					},
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The pre-deploy hook should run before the compose run command
	if len(executedCommands) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(executedCommands))
	}
	// First command should be the hook (run via shell)
	if !strings.Contains(executedCommands[0], "echo pre-deploy") {
		t.Errorf("expected first command to be pre-deploy hook, got: %s", executedCommands[0])
	}
}

func TestRunServicePostDeployHookOnSuccess(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands []string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedCommands = append(executedCommands, input.Command+" "+strings.Join(input.Args, " "))
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Deploy: &types.DeployConfig{
					Extensions: map[string]interface{}{
						"x-post-deploy-host-command": "echo post-deploy",
					},
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Post-deploy hook should be last
	lastCmd := executedCommands[len(executedCommands)-1]
	if !strings.Contains(lastCmd, "echo post-deploy") {
		t.Errorf("expected last command to be post-deploy hook, got: %s", lastCmd)
	}
}

func TestRunServicePostDeployHookSkippedOnFailure(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands []string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		cmd := input.Command + " " + strings.Join(input.Args, " ")
		executedCommands = append(executedCommands, cmd)
		if strings.Contains(cmd, "run") && strings.Contains(cmd, "--no-deps") {
			return ExecCommandResponse{ExitCode: 1}, errors.New("exit status 1")
		}
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Deploy: &types.DeployConfig{
					Extensions: map[string]interface{}{
						"x-post-deploy-host-command": "echo post-deploy",
					},
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err == nil {
		t.Fatal("expected error for failed service")
	}

	// Post-deploy hook should NOT have run
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "echo post-deploy") {
			t.Error("post-deploy hook should not run when service fails")
		}
	}
}

func TestRunServicePostDeployHookSkippedOnDetach(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	var executedCommands []string
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		cmd := input.Command + " " + strings.Join(input.Args, " ")
		executedCommands = append(executedCommands, cmd)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Deploy: &types.DeployConfig{
					Extensions: map[string]interface{}{
						"x-post-deploy-host-command": "echo post-deploy",
					},
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Detach:       true,
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Post-deploy hook should NOT run in detached mode
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "echo post-deploy") {
			t.Error("post-deploy hook should not run in detached mode")
		}
	}
}

func TestRunServiceCronServiceRunnable(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"nightly": types.ServiceConfig{
				Name:    "nightly",
				Restart: "no",
				Image:   "myapp:latest",
				Extensions: map[string]interface{}{
					"x-cron": map[string]interface{}{
						"schedule": "0 2 * * *",
					},
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "nightly",
	})
	if err != nil {
		t.Fatalf("cron service should be runnable, got error: %v", err)
	}
}

func TestRunServiceSkipLabelRunnable(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"seed": types.ServiceConfig{
				Name:    "seed",
				Restart: "no",
				Image:   "myapp:latest",
				Labels: types.Labels{
					"com.dokku.orchestrate/skip": "true",
				},
			},
		},
	}

	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "seed",
	})
	if err != nil {
		t.Fatalf("skip-labeled service should be runnable, got error: %v", err)
	}
}

func TestListOneShotServices(t *testing.T) {
	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:    "web",
				Restart: "always",
				Image:   "myapp:latest",
			},
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Command: types.ShellCommand{"./migrate", "up"},
			},
			"seed": types.ServiceConfig{
				Name:    "seed",
				Restart: "no",
				Image:   "myapp:latest",
				Command: types.ShellCommand{"./seed-data"},
			},
			"worker": types.ServiceConfig{
				Name:    "worker",
				Restart: "unless-stopped",
				Image:   "myapp:latest",
			},
		},
	}

	services := ListOneShotServices(project)
	if len(services) != 2 {
		t.Fatalf("expected 2 one-shot services, got %d", len(services))
	}

	names := make(map[string]bool)
	for _, svc := range services {
		names[svc.Name] = true
	}
	if !names["migrate"] {
		t.Error("expected migrate to be listed")
	}
	if !names["seed"] {
		t.Error("expected seed to be listed")
	}
	if names["web"] {
		t.Error("web should not be listed")
	}
	if names["worker"] {
		t.Error("worker should not be listed")
	}
}

func TestListOneShotServicesCommand(t *testing.T) {
	project := &types.Project{
		Services: types.Services{
			"migrate": types.ServiceConfig{
				Name:    "migrate",
				Restart: "no",
				Image:   "myapp:latest",
				Command: types.ShellCommand{"./migrate", "up"},
			},
		},
	}

	services := ListOneShotServices(project)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Command != "./migrate up" {
		t.Errorf("expected command './migrate up', got '%s'", services[0].Command)
	}
}

func TestListRunExecutions(t *testing.T) {
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{
					Names:  []string{"/test-seed-20260407-143022-ab3f"},
					State:  "exited",
					Status: "Exited (0) 5 minutes ago",
					Labels: map[string]string{
						"com.dokku.orchestrate/run":              "true",
						"com.dokku.orchestrate/run-project":      "test",
						"com.dokku.orchestrate/run-service":      "seed",
						"com.dokku.orchestrate/run-triggered-at": "2026-04-07T14:30:22Z",
					},
				},
				{
					Names: []string{"/test-export-20260407-150000-cd9e"},
					State: "running",
					Labels: map[string]string{
						"com.dokku.orchestrate/run":              "true",
						"com.dokku.orchestrate/run-project":      "test",
						"com.dokku.orchestrate/run-service":      "export",
						"com.dokku.orchestrate/run-triggered-at": "2026-04-07T15:00:00Z",
					},
				},
			}, nil
		},
	}

	executions, err := ListRunExecutions(context.Background(), ListRunExecutionsInput{
		Client: mockClient,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(executions) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(executions))
	}

	// First execution: exited
	if executions[0].ContainerName != "test-seed-20260407-143022-ab3f" {
		t.Errorf("expected container name 'test-seed-20260407-143022-ab3f', got '%s'", executions[0].ContainerName)
	}
	if executions[0].Service != "seed" {
		t.Errorf("expected service 'seed', got '%s'", executions[0].Service)
	}
	if executions[0].Status != "exited" {
		t.Errorf("expected status 'exited', got '%s'", executions[0].Status)
	}
	if executions[0].ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", executions[0].ExitCode)
	}

	// Second execution: running
	if executions[1].Status != "running" {
		t.Errorf("expected status 'running', got '%s'", executions[1].Status)
	}
	if executions[1].ExitCode != -1 {
		t.Errorf("expected exit code -1 for running, got %d", executions[1].ExitCode)
	}
}

func TestListRunExecutionsFilterByProject(t *testing.T) {
	var capturedFilters string
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			// Capture the filter args for verification
			for _, f := range options.Filters.Get("label") {
				capturedFilters += f + " "
			}
			return []container.Summary{}, nil
		},
	}

	_, err := ListRunExecutions(context.Background(), ListRunExecutionsInput{
		Client:      mockClient,
		ProjectName: "myproject",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedFilters, "com.dokku.orchestrate/run-project=myproject") {
		t.Errorf("expected project filter in query, got: %s", capturedFilters)
	}
}

func TestRunServiceValidation(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	project := &types.Project{
		Services: types.Services{},
	}

	tests := []struct {
		name        string
		input       RunServiceInput
		expectedErr string
	}{
		{
			name: "empty compose files",
			input: RunServiceInput{
				Client:      mockClient,
				Logger:      logger,
				Project:     project,
				ProjectName: "test",
				ServiceName: "migrate",
			},
			expectedErr: "at least one compose file is required",
		},
		{
			name: "empty project name",
			input: RunServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yaml"},
				Logger:       logger,
				Project:      project,
				ServiceName:  "migrate",
			},
			expectedErr: "project name is required",
		},
		{
			name: "empty service name",
			input: RunServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yaml"},
				Logger:       logger,
				Project:      project,
				ProjectName:  "test",
			},
			expectedErr: "service name is required",
		},
		{
			name: "nil project",
			input: RunServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yaml"},
				Logger:       logger,
				ProjectName:  "test",
				ServiceName:  "migrate",
			},
			expectedErr: "project is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunService(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got: %s", tt.expectedErr, err.Error())
			}
		})
	}
}

func TestRunServiceDetachedBuild(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}

	t.Run("build_flag_triggers_build_then_run", func(t *testing.T) {
		var executedCommands [][]string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executedCommands = append(executedCommands, input.Args)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"export": types.ServiceConfig{
					Name:    "export",
					Restart: "no",
					Image:   "myapp:latest",
					Build:   &types.BuildConfig{Context: "."},
				},
			},
		}

		err := RunService(context.Background(), RunServiceInput{
			Build:        true,
			Client:       mockClient,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Detach:       true,
			Executor:     mockExecutor,
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
			ServiceName:  "export",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(executedCommands) != 2 {
			t.Fatalf("expected 2 executor calls (build + run), got %d", len(executedCommands))
		}
		if !strings.Contains(strings.Join(executedCommands[0], " "), "build export") {
			t.Errorf("first call should be build, got: %v", executedCommands[0])
		}
	})

	t.Run("build_failure_returns_error", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 1}, errors.New("build boom")
		}
		project := &types.Project{
			Services: types.Services{
				"export": types.ServiceConfig{
					Name:    "export",
					Restart: "no",
					Image:   "myapp:latest",
					Build:   &types.BuildConfig{Context: "."},
				},
			},
		}
		err := RunService(context.Background(), RunServiceInput{
			Build:        true,
			Client:       mockClient,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Detach:       true,
			Executor:     mockExecutor,
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
			ServiceName:  "export",
		})
		if err == nil || !strings.Contains(err.Error(), "error building image") {
			t.Errorf("expected build error, got: %v", err)
		}
	})
}

func TestRunServiceDetachedPull(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}

	t.Run("pull_always_triggers_pull_then_run", func(t *testing.T) {
		var executedCommands [][]string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executedCommands = append(executedCommands, input.Args)
			return ExecCommandResponse{ExitCode: 0}, nil
		}
		project := &types.Project{
			Services: types.Services{
				"export": types.ServiceConfig{
					Name:    "export",
					Restart: "no",
					Image:   "myapp:latest",
				},
			},
		}
		err := RunService(context.Background(), RunServiceInput{
			Client:       mockClient,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Detach:       true,
			Executor:     mockExecutor,
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
			PullPolicy:   "always",
			ServiceName:  "export",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(executedCommands) != 2 {
			t.Fatalf("expected 2 calls (pull + run), got %d", len(executedCommands))
		}
		if !strings.Contains(strings.Join(executedCommands[0], " "), "pull export") {
			t.Errorf("first call should be pull, got: %v", executedCommands[0])
		}
	})

	t.Run("pull_failure_returns_error", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 1}, errors.New("pull boom")
		}
		project := &types.Project{
			Services: types.Services{
				"export": types.ServiceConfig{
					Name:    "export",
					Restart: "no",
					Image:   "myapp:latest",
				},
			},
		}
		err := RunService(context.Background(), RunServiceInput{
			Client:       mockClient,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Detach:       true,
			Executor:     mockExecutor,
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
			PullPolicy:   "always",
			ServiceName:  "export",
		})
		if err == nil || !strings.Contains(err.Error(), "error pulling image") {
			t.Errorf("expected pull error, got: %v", err)
		}
	})
}

func TestRunServiceDetachedRunError(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 1}, errors.New("run boom")
	}
	project := &types.Project{
		Services: types.Services{
			"export": types.ServiceConfig{
				Name:    "export",
				Restart: "no",
				Image:   "myapp:latest",
			},
		},
	}
	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Detach:       true,
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "export",
	})
	if err == nil || !strings.Contains(err.Error(), "error spawning one-shot service") {
		t.Errorf("expected run error, got: %v", err)
	}
}

func TestRunServiceInvalidPullPolicy(t *testing.T) {
	logger, _ := testLogger()
	mockClient := &mockDockerClient{}
	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}
	// pull_policy "build" with no Build section triggers ResolvePullPolicy error.
	project := &types.Project{
		Services: types.Services{
			"export": types.ServiceConfig{
				Name:       "export",
				Restart:    "no",
				Image:      "myapp:latest",
				PullPolicy: types.PullPolicyBuild,
			},
		},
	}
	err := RunService(context.Background(), RunServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yaml"},
		Detach:       true,
		Executor:     mockExecutor,
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "export",
	})
	if err == nil {
		t.Error("expected error from invalid pull policy combo")
	}
}
