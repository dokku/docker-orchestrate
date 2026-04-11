package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
)

func TestRandomSuffix(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{name: "length_4", length: 4},
		{name: "length_8", length: 8},
		{name: "length_1", length: 1},
	}

	validChars := regexp.MustCompile(`^[a-z0-9]+$`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := randomSuffix(tt.length)
			if len(result) != tt.length {
				t.Errorf("expected length %d, got %d (value: %q)", tt.length, len(result), result)
			}
			if !validChars.MatchString(result) {
				t.Errorf("result %q contains invalid characters (expected only a-z0-9)", result)
			}
		})
	}

	// Test uniqueness (probabilistic but extremely unlikely to fail)
	t.Run("uniqueness", func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 100; i++ {
			s := randomSuffix(4)
			seen[s] = true
		}
		// With 36^4 = ~1.7M possible values, 100 samples should almost always be unique
		if len(seen) < 90 {
			t.Errorf("expected mostly unique values from 100 calls, got only %d unique", len(seen))
		}
	})
}

func TestAppendDetachFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "inserts_d_after_run",
			args:     []string{"compose", "-f", "docker-compose.yml", "-p", "myproject", "run", "--no-deps", "--name", "test", "web"},
			expected: []string{"compose", "-f", "docker-compose.yml", "-p", "myproject", "run", "-d", "--no-deps", "--name", "test", "web"},
		},
		{
			name:     "simple_run_command",
			args:     []string{"compose", "run", "web"},
			expected: []string{"compose", "run", "-d", "web"},
		},
		{
			name:     "no_run_in_args_returns_unchanged",
			args:     []string{"compose", "up", "web"},
			expected: []string{"compose", "up", "web"},
		},
		{
			name:     "run_as_first_element",
			args:     []string{"run", "web"},
			expected: []string{"run", "-d", "web"},
		},
		{
			name:     "empty_args",
			args:     []string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendDetachFlag(tt.args)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.expected), len(result), result)
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("arg[%d]: expected %q, got %q", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

// execCall captures one invocation of the mock executor for assertion.
type execCall struct {
	args []string
}

// newRecordingExecutor returns an executor that records each call's args
// and returns the given responses in order. If no responses are provided,
// all calls return a zero-valued success response.
func newRecordingExecutor(responses []ExecCommandResponse, errs []error) (CommandExecutor, *[]execCall) {
	calls := []execCall{}
	mu := &sync.Mutex{}
	idx := 0
	exec := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		callArgs := append([]string{}, input.Args...)
		calls = append(calls, execCall{args: callArgs})
		if idx < len(responses) {
			r := responses[idx]
			var e error
			if idx < len(errs) {
				e = errs[idx]
			}
			idx++
			return r, e
		}
		return ExecCommandResponse{}, nil
	}
	return exec, &calls
}

func buildSpawnInput(config *CronConfig) SpawnCronTaskInput {
	var buf bytes.Buffer
	return SpawnCronTaskInput{
		Logger: newBufferLogger(&buf),
		Project: CronProject{
			Name:             "proj",
			ComposeFiles:     []string{"/tmp/docker-compose.yml"},
			WorkingDirectory: "/tmp",
		},
		Service: CronService{
			Name:   "job",
			Config: config,
			Service: &composeTypes.ServiceConfig{
				Name: "job",
			},
		},
	}
}

func TestSpawnCronTaskBasic(t *testing.T) {
	exec, calls := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 executor call, got %d", len(*calls))
	}
	args := (*calls)[0].args
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron=true") {
		t.Errorf("expected cron label in args, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-project=proj") {
		t.Errorf("expected cron-project label, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-service=job") {
		t.Errorf("expected cron-service label, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-schedule=@every 1h") {
		t.Errorf("expected schedule label, got: %s", joined)
	}
	if !strings.Contains(joined, "--rm") {
		t.Errorf("expected --rm when no notify configured, got: %s", joined)
	}
	// --detach must be inserted after "run"
	runIdx := -1
	for i, a := range args {
		if a == "run" {
			runIdx = i
			break
		}
	}
	if runIdx == -1 || runIdx+1 >= len(args) || args[runIdx+1] != "-d" {
		t.Errorf("expected -d immediately after run, got: %v", args)
	}
}

func TestSpawnCronTaskWithNotify(t *testing.T) {
	exec, calls := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{
		Schedule: "@every 1h",
		Notify: &CronNotifyConfig{
			URL:           "https://example.com/hook",
			On:            "always",
			IncludeOutput: true,
		},
	})
	input.Executor = exec

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := (*calls)[0].args
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--rm") {
		t.Errorf("expected NO --rm when notify configured, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-notify-url=https://example.com/hook") {
		t.Errorf("expected notify-url label, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-notify-on=always") {
		t.Errorf("expected notify-on label, got: %s", joined)
	}
	if !strings.Contains(joined, "--label com.dokku.orchestrate/cron-notify-include-output=true") {
		t.Errorf("expected include-output label, got: %s", joined)
	}
}

func TestSpawnCronTaskBuildTriggersBuild(t *testing.T) {
	exec, calls := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	input.Build = true
	input.Service.Service.Build = &composeTypes.BuildConfig{Context: "."}

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 executor calls (build + run), got %d", len(*calls))
	}
	buildArgs := strings.Join((*calls)[0].args, " ")
	if !strings.Contains(buildArgs, "build job") {
		t.Errorf("expected build call, got: %s", buildArgs)
	}
}

func TestSpawnCronTaskBuildError(t *testing.T) {
	exec, _ := newRecordingExecutor(
		[]ExecCommandResponse{{ExitCode: 1}},
		[]error{errors.New("build boom")},
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	input.Build = true
	input.Service.Service.Build = &composeTypes.BuildConfig{Context: "."}

	err := SpawnCronTask(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "error building image") {
		t.Errorf("expected build error, got: %v", err)
	}
}

func TestSpawnCronTaskPullAlwaysTriggersPull(t *testing.T) {
	exec, calls := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	input.PullPolicy = "always"

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 executor calls (pull + run), got %d", len(*calls))
	}
	pullArgs := strings.Join((*calls)[0].args, " ")
	if !strings.Contains(pullArgs, "pull job") {
		t.Errorf("expected pull call, got: %s", pullArgs)
	}
}

func TestSpawnCronTaskPullError(t *testing.T) {
	exec, _ := newRecordingExecutor(
		[]ExecCommandResponse{{ExitCode: 1}},
		[]error{errors.New("pull boom")},
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	input.PullPolicy = "always"

	err := SpawnCronTask(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "error pulling image") {
		t.Errorf("expected pull error, got: %v", err)
	}
}

func TestSpawnCronTaskRunError(t *testing.T) {
	exec, _ := newRecordingExecutor(
		[]ExecCommandResponse{{ExitCode: 1}},
		[]error{errors.New("run boom")},
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec

	err := SpawnCronTask(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "error spawning cron task") {
		t.Errorf("expected run error, got: %v", err)
	}
}

func TestSpawnCronTaskPullPolicyError(t *testing.T) {
	exec, _ := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	// 'build' pull policy with no Build section triggers ResolvePullPolicy error.
	input.Service.Service.PullPolicy = composeTypes.PullPolicyBuild

	err := SpawnCronTask(context.Background(), input)
	if err == nil {
		t.Error("expected error from invalid pull policy combination")
	}
}

func TestSpawnCronTaskNoOverlapSkipsWhenRunning(t *testing.T) {
	exec, calls := newRecordingExecutor(
		[]ExecCommandResponse{{Stdout: "container-id\n"}},
		nil,
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h", NoOverlap: true})
	input.Executor = exec

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the ps -q check should have been called — no run.
	if len(*calls) != 1 {
		t.Fatalf("expected 1 call (ps -q), got %d", len(*calls))
	}
	if !strings.Contains(strings.Join((*calls)[0].args, " "), "ps -q") {
		t.Errorf("expected ps -q call, got: %v", (*calls)[0].args)
	}
}

func TestSpawnCronTaskNoOverlapProceedsWhenIdle(t *testing.T) {
	exec, calls := newRecordingExecutor(
		[]ExecCommandResponse{{Stdout: ""}},
		nil,
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h", NoOverlap: true})
	input.Executor = exec

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls (ps + run), got %d", len(*calls))
	}
}

func TestSpawnCronTaskNoOverlapCheckError(t *testing.T) {
	// When the check fails, the spawner logs the error and proceeds as if
	// nothing was running.
	exec, calls := newRecordingExecutor(
		[]ExecCommandResponse{{}, {}},
		[]error{errors.New("ps boom"), nil},
	)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h", NoOverlap: true})
	input.Executor = exec

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("expected 2 calls even after check error, got %d", len(*calls))
	}
}

func TestSpawnCronTaskWorkingDirFallback(t *testing.T) {
	// Empty WorkingDirectory should be derived from the compose file path.
	exec, _ := newRecordingExecutor(nil, nil)
	input := buildSpawnInput(&CronConfig{Schedule: "@every 1h"})
	input.Executor = exec
	input.Project.WorkingDirectory = ""
	input.Project.ComposeFiles = []string{"/some/project/docker-compose.yml"}

	if err := SpawnCronTask(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckRunningCronContainer(t *testing.T) {
	t.Run("running_when_stdout_nonempty", func(t *testing.T) {
		exec, _ := newRecordingExecutor(
			[]ExecCommandResponse{{Stdout: "container-id\n"}},
			nil,
		)
		project := CronProject{Name: "proj"}
		running, err := checkRunningCronContainer(context.Background(), exec, project, "job")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !running {
			t.Error("expected running=true")
		}
	})

	t.Run("not_running_when_stdout_empty", func(t *testing.T) {
		exec, _ := newRecordingExecutor(
			[]ExecCommandResponse{{Stdout: ""}},
			nil,
		)
		project := CronProject{Name: "proj"}
		running, err := checkRunningCronContainer(context.Background(), exec, project, "job")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Error("expected running=false")
		}
	})

	t.Run("executor_error_returned", func(t *testing.T) {
		exec, _ := newRecordingExecutor(
			[]ExecCommandResponse{{}},
			[]error{errors.New("boom")},
		)
		project := CronProject{Name: "proj"}
		_, err := checkRunningCronContainer(context.Background(), exec, project, "job")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("expected executor error, got: %v", err)
		}
	})
}

func TestCronContainerNameFormat(t *testing.T) {
	// Verify the container name format matches the expected pattern:
	// {project}-{service}-{date}-{time}-{suffix}
	// e.g., myproject-web-20240115-143022-ab12
	t.Run("container_name_format", func(t *testing.T) {
		projectName := "myproject"
		serviceName := "backup"
		now := time.Date(2024, 1, 15, 14, 30, 22, 0, time.UTC)
		suffix := "ab12"

		containerName := fmt.Sprintf("%s-%s-%s-%s", projectName, serviceName, now.Format("20060102-150405"), suffix)

		expected := "myproject-backup-20240115-143022-ab12"
		if containerName != expected {
			t.Errorf("expected %q, got %q", expected, containerName)
		}

		// Verify it matches a reasonable pattern
		pattern := regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+-\d{8}-\d{6}-[a-z0-9]{4}$`)
		if !pattern.MatchString(containerName) {
			t.Errorf("container name %q does not match expected pattern", containerName)
		}
	})
}
