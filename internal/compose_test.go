package internal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/rs/zerolog"
)

func TestSortContainersByCreationTime(t *testing.T) {
	containers := []container.Summary{
		{ID: "oldest", Created: 100},
		{ID: "newest", Created: 300},
		{ID: "middle", Created: 200},
	}

	t.Run("sort newest first", func(t *testing.T) {
		sorted := make([]container.Summary, len(containers))
		copy(sorted, containers)
		sortContainersByCreationTime(sorted, true)

		if sorted[0].ID != "newest" {
			t.Errorf("expected newest first, got %s", sorted[0].ID)
		}
		if sorted[1].ID != "middle" {
			t.Errorf("expected middle second, got %s", sorted[1].ID)
		}
		if sorted[2].ID != "oldest" {
			t.Errorf("expected oldest last, got %s", sorted[2].ID)
		}
	})

	t.Run("sort oldest first", func(t *testing.T) {
		sorted := make([]container.Summary, len(containers))
		copy(sorted, containers)
		sortContainersByCreationTime(sorted, false)

		if sorted[0].ID != "oldest" {
			t.Errorf("expected oldest first, got %s", sorted[0].ID)
		}
		if sorted[1].ID != "middle" {
			t.Errorf("expected middle second, got %s", sorted[1].ID)
		}
		if sorted[2].ID != "newest" {
			t.Errorf("expected newest last, got %s", sorted[2].ID)
		}
	})

	t.Run("equal creation time", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "container1", Created: 100},
			{ID: "container2", Created: 100},
		}
		sortContainersByCreationTime(containers, true)
		if containers[0].Created != 100 || containers[1].Created != 100 {
			t.Errorf("expected both containers to have Created=100")
		}

		sortContainersByCreationTime(containers, false)
		if containers[0].Created != 100 || containers[1].Created != 100 {
			t.Errorf("expected both containers to have Created=100")
		}
	})
}

func TestContainerNameTemplate(t *testing.T) {
	tmpl, err := template.New("container-name").Parse("{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := ContainerNameTemplateData{
		ProjectName: "myproj",
		ServiceName: "web",
		InstanceID:  1,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.String() != "myproj-web-1" {
		t.Errorf("expected 'myproj-web-1', got '%s'", buf.String())
	}
}

func TestComposeFile(t *testing.T) {
	// create a temporary directory
	tempDir := t.TempDir()

	// change to the temporary directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	t.Run("no compose file", func(t *testing.T) {
		_, err := ComposeFile()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("docker-compose.yaml", func(t *testing.T) {
		f, err := os.Create("docker-compose.yaml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		f.Close()
		defer os.Remove("docker-compose.yaml")

		path, err := ComposeFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(path, "docker-compose.yaml") {
			t.Errorf("expected path to end with 'docker-compose.yaml', got %s", path)
		}
	})

	t.Run("docker-compose.yml", func(t *testing.T) {
		// docker-compose.yaml already exists from previous test if defer didn't run yet,
		// but t.TempDir() is clean for each test or we can just remove it.
		os.Remove("docker-compose.yaml")

		f, err := os.Create("docker-compose.yml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		f.Close()
		defer os.Remove("docker-compose.yml")

		path, err := ComposeFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(path, "docker-compose.yml") {
			t.Errorf("expected path to end with 'docker-compose.yml', got %s", path)
		}
	})
}

func TestComposeFileArgs(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		expected []string
	}{
		{
			name:     "single file",
			files:    []string{"docker-compose.yaml"},
			expected: []string{"-f", "docker-compose.yaml"},
		},
		{
			name:     "multiple files",
			files:    []string{"docker-compose.yaml", "docker-compose.override.yaml"},
			expected: []string{"-f", "docker-compose.yaml", "-f", "docker-compose.override.yaml"},
		},
		{
			name:     "empty",
			files:    []string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComposeFileArgs(tt.files)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("arg[%d]: expected %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestEnvFileArgs(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		expected []string
	}{
		{
			name:     "single file",
			files:    []string{"app.env"},
			expected: []string{"--env-file", "app.env"},
		},
		{
			name:     "multiple files",
			files:    []string{"app.env", "secrets.env"},
			expected: []string{"--env-file", "app.env", "--env-file", "secrets.env"},
		},
		{
			name:     "empty",
			files:    []string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnvFileArgs(tt.files)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("arg[%d]: expected %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestComposeProjectMultipleFiles(t *testing.T) {
	tempDir := t.TempDir()

	baseContent := `services:
  web:
    image: nginx:latest
`
	overrideContent := `services:
  web:
    environment:
      OVERRIDE_APPLIED: "true"
`

	basePath := tempDir + "/docker-compose.yaml"
	overridePath := tempDir + "/docker-compose.override.yaml"

	if err := os.WriteFile(basePath, []byte(baseContent), 0644); err != nil {
		t.Fatalf("failed to write base file: %v", err)
	}
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0644); err != nil {
		t.Fatalf("failed to write override file: %v", err)
	}

	ctx := context.Background()

	t.Run("single file", func(t *testing.T) {
		project, err := ComposeProject(ctx, "test", []string{basePath}, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		service, err := project.GetService("web")
		if err != nil {
			t.Fatalf("service 'web' not found: %v", err)
		}

		if service.Image != "nginx:latest" {
			t.Errorf("expected image 'nginx:latest', got %q", service.Image)
		}

		if service.Environment != nil {
			if _, ok := service.Environment["OVERRIDE_APPLIED"]; ok {
				t.Error("did not expect OVERRIDE_APPLIED in single-file mode")
			}
		}
	})

	t.Run("multiple files merged", func(t *testing.T) {
		project, err := ComposeProject(ctx, "test", []string{basePath, overridePath}, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		service, err := project.GetService("web")
		if err != nil {
			t.Fatalf("service 'web' not found: %v", err)
		}

		if service.Image != "nginx:latest" {
			t.Errorf("expected image 'nginx:latest', got %q", service.Image)
		}

		val, ok := service.Environment["OVERRIDE_APPLIED"]
		if !ok {
			t.Fatal("expected OVERRIDE_APPLIED in environment")
		}
		if val == nil || *val != "true" {
			t.Errorf("expected OVERRIDE_APPLIED='true', got %v", val)
		}
	})
}

func TestComposeProjectWithEnvFiles(t *testing.T) {
	tempDir := t.TempDir()

	composeContent := `services:
  web:
    image: nginx:latest
    environment:
      MY_VAR: "${MY_VAR}"
`
	envContent := `MY_VAR=hello`

	composePath := tempDir + "/docker-compose.yaml"
	envPath := tempDir + "/custom.env"

	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}

	ctx := context.Background()

	t.Run("with env file", func(t *testing.T) {
		project, err := ComposeProject(ctx, "test", []string{composePath}, nil, []string{envPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		service, err := project.GetService("web")
		if err != nil {
			t.Fatalf("service 'web' not found: %v", err)
		}

		val, ok := service.Environment["MY_VAR"]
		if !ok {
			t.Fatal("expected MY_VAR in environment")
		}
		if val == nil || *val != "hello" {
			t.Errorf("expected MY_VAR='hello', got %v", val)
		}
	})

	t.Run("without env file uses default behavior", func(t *testing.T) {
		project, err := ComposeProject(ctx, "test", []string{composePath}, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		service, err := project.GetService("web")
		if err != nil {
			t.Fatalf("service 'web' not found: %v", err)
		}

		// Without env file, MY_VAR should be empty (no .env in tempdir)
		val, ok := service.Environment["MY_VAR"]
		if !ok {
			t.Fatal("expected MY_VAR key in environment")
		}
		if val != nil && *val != "" {
			t.Errorf("expected MY_VAR to be empty without env file, got %q", *val)
		}
	})
}

func TestRenameContainersToConvention(t *testing.T) {
	ctx := context.Background()
	containers := []container.Summary{
		{ID: "id1_container_id", Names: []string{"/old1"}, Created: 100},
		{ID: "id2_container_id", Names: []string{"/old2"}, Created: 200},
	}

	t.Run("rename both containers", func(t *testing.T) {
		mock := &mockDockerClient{}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   containers,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mock.renamedContainers) != 2 {
			t.Errorf("expected 2 renames, got %d", len(mock.renamedContainers))
		}
		if mock.renamedContainers["id1_container_id"] != "proj-web-1" {
			t.Errorf("expected id1_container_id renamed to proj-web-1, got %s", mock.renamedContainers["id1_container_id"])
		}
		if mock.renamedContainers["id2_container_id"] != "proj-web-2" {
			t.Errorf("expected id2_container_id renamed to proj-web-2, got %s", mock.renamedContainers["id2_container_id"])
		}
	})

	t.Run("skip renaming if already correct", func(t *testing.T) {
		mock := &mockDockerClient{}
		containersWithCorrectNames := []container.Summary{
			{ID: "id1_container_id", Names: []string{"/proj-web-1"}, Created: 100},
			{ID: "id2_container_id", Names: []string{"/proj-web-2"}, Created: 200},
		}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   containersWithCorrectNames,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mock.renamedContainers) != 0 {
			t.Errorf("expected 0 renames, got %d", len(mock.renamedContainers))
		}
	})

	t.Run("partial rename", func(t *testing.T) {
		mock := &mockDockerClient{}
		containers := []container.Summary{
			{ID: "id1_container_id", Names: []string{"/proj-web-1"}, Created: 100},
			{ID: "id2_container_id", Names: []string{"/old2"}, Created: 200},
		}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   containers,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mock.renamedContainers) != 1 {
			t.Errorf("expected 1 rename, got %d", len(mock.renamedContainers))
		}
		if mock.renamedContainers["id2_container_id"] != "proj-web-2" {
			t.Errorf("expected id2_container_id renamed to proj-web-2, got %s", mock.renamedContainers["id2_container_id"])
		}
	})

	t.Run("empty containers", func(t *testing.T) {
		input := RenameContainersToConventionInput{
			Containers: []container.Summary{},
		}
		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid template", func(t *testing.T) {
		input := RenameContainersToConventionInput{
			Containers:   containers,
			NameTemplate: "{{.Unclosed",
		}
		err := renameContainersToConvention(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("container with no names", func(t *testing.T) {
		mock := &mockDockerClient{}
		containersWithNoNames := []container.Summary{
			{ID: "id1_no_name", Names: []string{}, Created: 100},
		}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   containersWithNoNames,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if mock.renamedContainers["id1_no_name"] != "proj-web-1" {
			t.Errorf("expected id1_no_name renamed to proj-web-1, got %s", mock.renamedContainers["id1_no_name"])
		}
	})

	t.Run("rename with name conflicts", func(t *testing.T) {
		// Simulates docker compose assigning names in different order than creation time.
		// Container created first has name proj-web-3, container created last has name proj-web-1.
		// Without two-pass rename, renaming the first container to proj-web-1 would fail
		// because the last container still holds that name.
		var renameCalls []struct{ id, name string }
		mock := &mockDockerClient{
			containerRename: func(ctx context.Context, id, name string) error {
				renameCalls = append(renameCalls, struct{ id, name string }{id, name})
				return nil
			},
		}
		conflictContainers := []container.Summary{
			{ID: "id1_container_id", Names: []string{"/proj-web-3"}, Created: 100},
			{ID: "id2_container_id", Names: []string{"/proj-web-2"}, Created: 200},
			{ID: "id3_container_id", Names: []string{"/proj-web-1"}, Created: 300},
		}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   conflictContainers,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// id2 already has correct name (proj-web-2), so only id1 and id3 are renamed
		// 4 calls: 2 to tmp names (pass 1), 2 to final names (pass 2)
		if len(renameCalls) != 4 {
			t.Errorf("expected 4 rename calls, got %d", len(renameCalls))
			for i, call := range renameCalls {
				t.Logf("  call %d: %s -> %s", i, call.id, call.name)
			}
			t.FailNow()
		}

		// Pass 1: rename to tmp names in ascending creation order
		if renameCalls[0].id != "id1_container_id" || renameCalls[0].name != "proj-web-1-tmp" {
			t.Errorf("pass 1 call 0: expected id1->proj-web-1-tmp, got %s->%s", renameCalls[0].id, renameCalls[0].name)
		}
		if renameCalls[1].id != "id3_container_id" || renameCalls[1].name != "proj-web-3-tmp" {
			t.Errorf("pass 1 call 1: expected id3->proj-web-3-tmp, got %s->%s", renameCalls[1].id, renameCalls[1].name)
		}

		// Pass 2: rename to final names in ascending creation order
		if renameCalls[2].id != "id1_container_id" || renameCalls[2].name != "proj-web-1" {
			t.Errorf("pass 2 call 0: expected id1->proj-web-1, got %s->%s", renameCalls[2].id, renameCalls[2].name)
		}
		if renameCalls[3].id != "id3_container_id" || renameCalls[3].name != "proj-web-3" {
			t.Errorf("pass 2 call 1: expected id3->proj-web-3, got %s->%s", renameCalls[3].id, renameCalls[3].name)
		}
	})

	t.Run("rename error", func(t *testing.T) {
		mock := &mockDockerClient{
			containerRename: func(ctx context.Context, id, name string) error {
				return errors.New("rename failed")
			},
		}
		input := RenameContainersToConventionInput{
			Client:       mock,
			Containers:   containers,
			ProjectName:  "proj",
			ServiceName:  "web",
			NameTemplate: "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}",
		}

		err := renameContainersToConvention(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "rename failed") {
			t.Errorf("expected error to contain 'rename failed', got '%s'", err.Error())
		}
	})
}

func TestRollingUpdateBatchStopFirst(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("successful rolling update stop-first", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				// First call to composeContainers (after stop)
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "existing1", Created: 100},
					}, nil
				}
				// Second call to composeContainers (after up --scale)
				return []container.Summary{
					{ID: "existing1", Created: 100},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called")
		}
		if len(terminatedIds) != 1 || terminatedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be terminated, got %v", terminatedIds)
		}
		if output.TotalUpdates != 1 {
			t.Errorf("expected 1 total update, got %d", output.TotalUpdates)
		}
		if output.Failures != 0 {
			t.Errorf("expected 0 failures, got %d", output.Failures)
		}
	})

	t.Run("failure ratio exceeded", func(t *testing.T) {
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{}, nil
				}
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // will cause healthcheck failure
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0.1, // 10%
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "max failure ratio exceeded") {
			t.Errorf("expected error to contain 'max failure ratio exceeded', got '%s'", err.Error())
		}
		if output.Failures != 1 {
			t.Errorf("expected 1 failure, got %d", output.Failures)
		}
	})

	t.Run("detached pre-stop and post-stop flags passed through", func(t *testing.T) {
		detachedCount := 0
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "existing1_container", Created: 100},
					}, nil
				}
				return []container.Summary{
					{ID: "existing1_container", Created: 100},
					{ID: "new1_container_id00", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State:      &container.State{Running: true},
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.Detached {
				detachedCount++
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id00", Created: 50},
		}

		input := RollingUpdateInput{
			Client:                      mock,
			Executor:                    executor,
			Logger:                      logger,
			ProjectName:                 "proj",
			ServiceName:                 "web",
			Parallelism:                 1,
			MaxFailureRatio:             0,
			ContainersToUpdate:          batch,
			StopTimeout:                 10,
			TickerCh:                    testTickerCh(),
			PreStopHostCommand:          "echo pre-stop",
			PreStopHostCommandDetached:  true,
			PostStopHostCommand:         "echo post-stop",
			PostStopHostCommandDetached: true,
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if detachedCount != 2 {
			t.Errorf("expected 2 detached executor calls (pre-stop + post-stop), got %d", detachedCount)
		}
	})
}

func testTickerCh() <-chan time.Time {
	ch := make(chan time.Time, 10)
	for i := 0; i < 10; i++ {
		ch <- time.Now()
	}
	return ch
}

func TestRollingUpdateContainers(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("batching and start-first strategy", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		executorCallCount := 0
		sleepCalled := false

		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				// First call in each batch: currentContainers
				// Second call in each batch: allContainers after scale up
				if listCallCount%2 == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
						{ID: "old2_container_id", Created: 60},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "old2_container_id", Created: 60},
					{ID: "new_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCallCount++
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		sleeper := func(d time.Duration) {
			sleepCalled = true
		}

		containers := []container.Summary{
			{ID: "old1_container_id", Created: 50},
			{ID: "old2_container_id", Created: 60},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Sleeper:            sleeper,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1, // 2 batches
			Delay:              10 * time.Second,
			Order:              "start-first",
			ContainersToUpdate: containers,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output, err := rollingUpdateContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if executorCallCount != 2 {
			t.Errorf("expected 2 executor calls (1 per batch), got %d", executorCallCount)
		}
		if len(terminatedIds) != 2 {
			t.Errorf("expected 2 terminations, got %d", len(terminatedIds))
		}
		if !sleepCalled {
			t.Error("expected sleeper to be called between batches")
		}
		if output.TotalUpdates != 2 {
			t.Errorf("expected 2 total updates, got %d", output.TotalUpdates)
		}
	})

	t.Run("batching and stop-first strategy", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		executorCallCount := 0

		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				// rollingUpdateBatchStopFirst:
				// First call: composeContainers (after stop)
				// Second call: composeContainers (after up --scale)
				if listCallCount%2 == 1 {
					return []container.Summary{
						{ID: "old2_container_id", Created: 60},
					}, nil
				}
				return []container.Summary{
					{ID: "old2_container_id", Created: 60},
					{ID: "new_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCallCount++
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		containers := []container.Summary{
			{ID: "old1_container_id", Created: 50},
			{ID: "old2_container_id", Created: 60},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1, // 2 batches
			Order:              "stop-first",
			ContainersToUpdate: containers,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output, err := rollingUpdateContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if executorCallCount != 2 {
			t.Errorf("expected 2 executor calls (1 per batch), got %d", executorCallCount)
		}
		if len(terminatedIds) != 2 {
			t.Errorf("expected 2 terminations, got %d", len(terminatedIds))
		}
		if output.TotalUpdates != 2 {
			t.Errorf("expected 2 total updates, got %d", output.TotalUpdates)
		}
	})
}

func TestRollingUpdateBatchStartFirst(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("successful rolling update start-first", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				// First call to composeContainers (before up)
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				// Second call to composeContainers (after up --scale)
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called")
		}
		if len(terminatedIds) != 1 || terminatedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be terminated, got %v", terminatedIds)
		}
		if output.TotalUpdates != 1 {
			t.Errorf("expected 1 total update, got %d", output.TotalUpdates)
		}
		if output.Failures != 0 {
			t.Errorf("expected 0 failures, got %d", output.Failures)
		}
	})

	t.Run("failure ratio exceeded", func(t *testing.T) {
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // will cause healthcheck failure
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0.1, // 10%
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "max failure ratio exceeded") {
			t.Errorf("expected error to contain 'max failure ratio exceeded', got '%s'", err.Error())
		}
		if output.Failures != 1 {
			t.Errorf("expected 1 failure, got %d", output.Failures)
		}
	})

	t.Run("detached pre-stop and post-stop flags passed through", func(t *testing.T) {
		detachedCount := 0
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id00", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id00", Created: 50},
					{ID: "new1_container_id00", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State:      &container.State{Running: true},
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.Detached {
				detachedCount++
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id00", Created: 50},
		}

		input := RollingUpdateInput{
			Client:                      mock,
			Executor:                    executor,
			Logger:                      logger,
			ProjectName:                 "proj",
			ServiceName:                 "web",
			Parallelism:                 1,
			MaxFailureRatio:             0,
			ContainersToUpdate:          batch,
			StopTimeout:                 10,
			TickerCh:                    testTickerCh(),
			PreStopHostCommand:          "echo pre-stop",
			PreStopHostCommandDetached:  true,
			PostStopHostCommand:         "echo post-stop",
			PostStopHostCommandDetached: true,
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if detachedCount != 2 {
			t.Errorf("expected 2 detached executor calls (pre-stop + post-stop), got %d", detachedCount)
		}
	})
}

func TestRollingUpdateBatchStartFirstRollback(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("rollback on healthcheck failure", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // will cause healthcheck failure
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "rolled back") {
			t.Errorf("expected error to contain 'rolled back', got '%s'", err.Error())
		}
		// Only the failed new container should be terminated, NOT the old one
		if len(terminatedIds) != 1 || terminatedIds[0] != "new1_container_id" {
			t.Errorf("expected only new1_container_id to be terminated, got %v", terminatedIds)
		}
		if output.Failures != 1 {
			t.Errorf("expected 1 failure, got %d", output.Failures)
		}
	})

	t.Run("rollback mode healthy containers", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Old container should be terminated after batch (deferred cleanup)
		if len(terminatedIds) != 1 || terminatedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be terminated, got %v", terminatedIds)
		}
		if output.Failures != 0 {
			t.Errorf("expected 0 failures, got %d", output.Failures)
		}
	})

	t.Run("rollback with max_failure_ratio not exceeded", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		inspectCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
						{ID: "old2_container_id", Created: 60},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "old2_container_id", Created: 60},
					{ID: "new1_container_id", Created: 300},
					{ID: "new2_container_id", Created: 310},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				inspectCount++
				// First new container healthy, second fails
				if id == "new1_container_id" {
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{Running: true},
						},
					}, nil
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: false},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
			{ID: "old2_container_id", Created: 60},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        2,
			MaxFailureRatio:    0.6, // 50% failure should NOT exceed 60% threshold
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if output.Failures != 1 {
			t.Errorf("expected 1 failure, got %d", output.Failures)
		}
	})

	t.Run("rollback with max_failure_ratio exceeded", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
						{ID: "old2_container_id", Created: 60},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "old2_container_id", Created: 60},
					{ID: "new1_container_id", Created: 300},
					{ID: "new2_container_id", Created: 310},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				// First new container healthy, second fails
				if id == "new1_container_id" {
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{Running: true},
						},
					}, nil
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: false},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
			{ID: "old2_container_id", Created: 60},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        2,
			MaxFailureRatio:    0.1, // 50% failure EXCEEDS 10% threshold
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "max failure ratio exceeded") {
			t.Errorf("expected error to contain 'max failure ratio exceeded', got '%s'", err.Error())
		}
		if !strings.Contains(err.Error(), "rolling back") {
			t.Errorf("expected error to contain 'rolling back', got '%s'", err.Error())
		}
		// Both the failed new container and the healthy new container should be terminated (rollback)
		// Old containers should NOT be terminated
		for _, id := range terminatedIds {
			if id == "old1_container_id" || id == "old2_container_id" {
				t.Errorf("old container %s should not be terminated during rollback", id)
			}
		}
	})
}

func TestRollingUpdateBatchStopFirstRollback(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("rollback on healthcheck failure", func(t *testing.T) {
		stoppedIds := make([]string, 0)
		startedIds := make([]string, 0)
		terminatedIds := make([]string, 0)
		removedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{}, nil
				}
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // will cause healthcheck failure
						},
					},
				}, nil
			},
			containerStop: func(ctx context.Context, id string, options container.StopOptions) error {
				stoppedIds = append(stoppedIds, id)
				return nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				startedIds = append(startedIds, id)
				return nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
			containerRemove: func(ctx context.Context, id string, options container.RemoveOptions) error {
				removedIds = append(removedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "rolled back") {
			t.Errorf("expected error to contain 'rolled back', got '%s'", err.Error())
		}
		// Old container should have been stopped (not terminated)
		if len(stoppedIds) != 1 || stoppedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be stopped, got %v", stoppedIds)
		}
		// Old container should have been restarted
		if len(startedIds) != 1 || startedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be restarted, got %v", startedIds)
		}
		// New container should have been terminated
		if len(terminatedIds) != 2 {
			t.Errorf("expected 2 terminations (failed new + rollback cleanup), got %d: %v", len(terminatedIds), terminatedIds)
		}
		// Old container should NOT have been removed
		if len(removedIds) != 0 {
			t.Errorf("expected no containers removed during rollback, got %v", removedIds)
		}
	})

	t.Run("rollback mode healthy containers", func(t *testing.T) {
		stoppedIds := make([]string, 0)
		startedIds := make([]string, 0)
		terminatedIds := make([]string, 0)
		removedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{}, nil
				}
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerStop: func(ctx context.Context, id string, options container.StopOptions) error {
				stoppedIds = append(stoppedIds, id)
				return nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				startedIds = append(startedIds, id)
				return nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
			containerRemove: func(ctx context.Context, id string, options container.RemoveOptions) error {
				removedIds = append(removedIds, id)
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Old container should have been stopped (not terminated)
		if len(stoppedIds) != 1 || stoppedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be stopped, got %v", stoppedIds)
		}
		// Old container should NOT have been restarted (success path)
		if len(startedIds) != 0 {
			t.Errorf("expected no containers restarted on success, got %v", startedIds)
		}
		// Old container should have been removed (cleanup after success)
		if len(removedIds) != 1 || removedIds[0] != "old1_container_id" {
			t.Errorf("expected old1_container_id to be removed, got %v", removedIds)
		}
		// No containers should have been terminated (ContainerTerminate not used in rollback success)
		if len(terminatedIds) != 0 {
			t.Errorf("expected no containers terminated, got %v", terminatedIds)
		}
	})

	t.Run("rollback with restart failure", func(t *testing.T) {
		startedIds := make([]string, 0)
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{}, nil
				}
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // healthcheck fails
						},
					},
				}, nil
			},
			containerStop: func(ctx context.Context, id string, options container.StopOptions) error {
				return nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				startedIds = append(startedIds, id)
				return fmt.Errorf("container not found")
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			FailureAction:      "rollback",
			ContainersToUpdate: batch,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		// Rollback error should still be returned even if restart fails
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "rolled back") {
			t.Errorf("expected error to contain 'rolled back', got '%s'", err.Error())
		}
		// ContainerStart should have been attempted
		if len(startedIds) != 1 || startedIds[0] != "old1_container_id" {
			t.Errorf("expected ContainerStart to be called for old1_container_id, got %v", startedIds)
		}
		// Log output should contain the restart warning
		logOutput := buf.String()
		if !strings.Contains(logOutput, "failed to restart old container") {
			t.Errorf("expected log to contain restart failure warning, got: %s", logOutput)
		}
	})
}

func TestScaleDownContainers(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("successful scale down", func(t *testing.T) {
		terminatedIds := make([]string, 0)
		mock := &mockDockerClient{
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminatedIds = append(terminatedIds, id)
				return nil
			},
		}

		containers := []container.Summary{
			{ID: "id1_oldest_container", Created: 100},
			{ID: "id3_newest_container", Created: 300},
			{ID: "id2_middle_container", Created: 200},
		}

		input := ScaleDownContainersInput{
			Client:            mock,
			CurrentContainers: containers,
			CurrentReplicas:   3,
			DesiredReplicas:   1,
			Logger:            logger,
			ProjectName:       "proj",
			ServiceName:       "web",
			StopTimeout:       10,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(terminatedIds) != 2 {
			t.Errorf("expected 2 terminations, got %d", len(terminatedIds))
		}
		// Should terminate oldest first: id1_oldest then id2_middle
		if terminatedIds[0] != "id1_oldest_container" {
			t.Errorf("expected id1_oldest_container to be terminated first, got %s", terminatedIds[0])
		}
		if terminatedIds[1] != "id2_middle_container" {
			t.Errorf("expected id2_middle_container to be terminated second, got %s", terminatedIds[1])
		}
	})

	t.Run("no scale down needed", func(t *testing.T) {
		mock := &mockDockerClient{
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				t.Error("ContainerTerminate should not have been called")
				return nil
			},
		}

		containers := []container.Summary{
			{ID: "id1", Created: 100},
		}

		input := ScaleDownContainersInput{
			Client:            mock,
			CurrentContainers: containers,
			CurrentReplicas:   1,
			DesiredReplicas:   1,
			Logger:            logger,
			StopTimeout:       10,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("custom stop timeout is passed through", func(t *testing.T) {
		var capturedTimeout int
		mock := &mockDockerClient{
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				capturedTimeout = timeoutSeconds
				return nil
			},
		}

		containers := []container.Summary{
			{ID: "id1_oldest_container", Created: 100},
			{ID: "id2_newest_container", Created: 200},
		}

		input := ScaleDownContainersInput{
			Client:            mock,
			CurrentContainers: containers,
			CurrentReplicas:   2,
			DesiredReplicas:   1,
			Logger:            logger,
			ProjectName:       "proj",
			ServiceName:       "web",
			StopTimeout:       30,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedTimeout != 30 {
			t.Errorf("expected timeout 30, got %d", capturedTimeout)
		}
	})

	t.Run("pre-stop host command detached flag is passed through", func(t *testing.T) {
		var capturedDetached []bool
		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			capturedDetached = append(capturedDetached, input.Detached)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		containers := []container.Summary{
			{ID: "id1_oldest_container", Created: 100},
			{ID: "id2_newest_container", Created: 200},
		}

		input := ScaleDownContainersInput{
			Client:                     mock,
			CurrentContainers:          containers,
			CurrentReplicas:            2,
			DesiredReplicas:            1,
			Executor:                   executor,
			Logger:                     logger,
			ProjectName:                "proj",
			ServiceName:                "web",
			StopTimeout:                10,
			PreStopHostCommand:         "echo pre-stop",
			PreStopHostCommandDetached: true,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		hasDetached := false
		for _, d := range capturedDetached {
			if d {
				hasDetached = true
				break
			}
		}
		if !hasDetached {
			t.Error("expected executor to receive Detached=true for pre-stop host command")
		}
	})

	t.Run("post-stop host command detached flag is passed through", func(t *testing.T) {
		var capturedDetached []bool
		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			capturedDetached = append(capturedDetached, input.Detached)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		containers := []container.Summary{
			{ID: "id1_oldest_container", Created: 100},
			{ID: "id2_newest_container", Created: 200},
		}

		input := ScaleDownContainersInput{
			Client:                      mock,
			CurrentContainers:           containers,
			CurrentReplicas:             2,
			DesiredReplicas:             1,
			Executor:                    executor,
			Logger:                      logger,
			ProjectName:                 "proj",
			ServiceName:                 "web",
			StopTimeout:                 10,
			PostStopHostCommand:         "echo post-stop",
			PostStopHostCommandDetached: true,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		hasDetached := false
		for _, d := range capturedDetached {
			if d {
				hasDetached = true
				break
			}
		}
		if !hasDetached {
			t.Error("expected executor to receive Detached=true for post-stop host command")
		}
	})

	t.Run("both pre-stop and post-stop detached flags are passed through", func(t *testing.T) {
		detachedCount := 0
		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						HostConfig: &container.HostConfig{NetworkMode: "host"},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.Detached {
				detachedCount++
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		containers := []container.Summary{
			{ID: "id1_oldest_container", Created: 100},
			{ID: "id2_newest_container", Created: 200},
		}

		input := ScaleDownContainersInput{
			Client:                      mock,
			CurrentContainers:           containers,
			CurrentReplicas:             2,
			DesiredReplicas:             1,
			Executor:                    executor,
			Logger:                      logger,
			ProjectName:                 "proj",
			ServiceName:                 "web",
			StopTimeout:                 10,
			PreStopHostCommand:          "echo pre-stop",
			PreStopHostCommandDetached:  true,
			PostStopHostCommand:         "echo post-stop",
			PostStopHostCommandDetached: true,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if detachedCount != 2 {
			t.Errorf("expected 2 detached executor calls, got %d", detachedCount)
		}
	})
}

func TestScaleUpContainers(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("successful scale up", func(t *testing.T) {
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "new1_container_id", Names: []string{"/new1"}},
					{ID: "new2_container_id", Names: []string{"/new2"}},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
		}

		executorCalled := false
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			executorCalled = true
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			DesiredReplicas:    2,
			Parallelism:        1,
			ExistingContainers: []container.Summary{},
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !executorCalled {
			t.Error("expected executor to be called")
		}
	})

	t.Run("failure ratio exceeded", func(t *testing.T) {
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "new1_container_id", Names: []string{"/new1"}},
					{ID: "new2_container_id", Names: []string{"/new2"}},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: false, // will cause healthcheck failure
						},
					},
				}, nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			DesiredReplicas:    2,
			Parallelism:        1,
			MaxFailureRatio:    0.1, // 10%
			ExistingContainers: []container.Summary{},
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "max failure ratio exceeded") {
			t.Errorf("expected error to contain 'max failure ratio exceeded', got '%s'", err.Error())
		}
	})
}

func TestPostStartHooksInScaleUp(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("post_start hooks execute after container start and before healthcheck", func(t *testing.T) {
		var mu sync.Mutex
		var operations []string

		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "new1_container_id", Names: []string{"/new1"}},
				}, nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				mu.Lock()
				operations = append(operations, "start")
				mu.Unlock()
				return nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				mu.Lock()
				operations = append(operations, "healthcheck")
				mu.Unlock()
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: true},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				mu.Lock()
				operations = append(operations, "hook:"+strings.Join(config.Cmd, " "))
				mu.Unlock()
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (dockerTypes.HijackedResponse, error) {
				reader := strings.NewReader("")
				return dockerTypes.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			DesiredReplicas:    1,
			Parallelism:        1,
			ExistingContainers: []container.Summary{},
			PostStartHooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"sh", "-c", "echo setup1"}},
				{Command: composeTypes.ShellCommand{"sh", "-c", "echo setup2"}},
			},
			StopTimeout: 10,
			TickerCh:    testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expected order: start, hook:sh -c echo setup1, hook:sh -c echo setup2, healthcheck
		expectedOps := []string{
			"start",
			"hook:sh -c echo setup1",
			"hook:sh -c echo setup2",
			"healthcheck",
		}
		if len(operations) != len(expectedOps) {
			t.Fatalf("expected %d operations, got %d: %v", len(expectedOps), len(operations), operations)
		}
		for i := range expectedOps {
			if operations[i] != expectedOps[i] {
				t.Errorf("operation[%d]: expected %q, got %q", i, expectedOps[i], operations[i])
			}
		}
	})

	t.Run("post_start hook failure triggers cleanup and terminate", func(t *testing.T) {
		var mu sync.Mutex
		var operations []string

		// Track which exec IDs correspond to which hook types
		execCounter := 0
		postStartExecID := ""

		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "new1_container_id", Names: []string{"/new1"}},
				}, nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				mu.Lock()
				operations = append(operations, "start")
				mu.Unlock()
				return nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
						State: &container.State{Running: true},
					},
					Config: &container.Config{
						Shell: []string{"/bin/sh"},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				mu.Lock()
				execCounter++
				currentExecID := fmt.Sprintf("exec-%d", execCounter)
				isScript := false
				for _, c := range config.Cmd {
					if strings.Contains(c, "pre-stop.sh") {
						isScript = true
						break
					}
				}
				if isScript {
					operations = append(operations, "pre-stop-command")
				} else {
					hookLabel := "hook:" + strings.Join(config.Cmd, " ")
					operations = append(operations, hookLabel)
					if strings.Contains(strings.Join(config.Cmd, " "), "fail") {
						postStartExecID = currentExecID
					}
				}
				mu.Unlock()
				return container.ExecCreateResponse{ID: currentExecID}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (dockerTypes.HijackedResponse, error) {
				reader := strings.NewReader("")
				return dockerTypes.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				mu.Lock()
				isPostStart := execID == postStartExecID
				mu.Unlock()
				if isPostStart {
					return container.ExecInspect{ExitCode: 1}, nil
				}
				return container.ExecInspect{ExitCode: 0}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				mu.Lock()
				operations = append(operations, "terminate")
				mu.Unlock()
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			mu.Lock()
			// Only track host script commands, not docker compose create
			if input.Command != "docker" {
				operations = append(operations, "host-command")
			}
			mu.Unlock()
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			DesiredReplicas:    1,
			Parallelism:        1,
			ExistingContainers: []container.Summary{},
			PostStartHooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"sh", "-c", "fail"}},
			},
			PreStopHostCommand: "echo stopping",
			PreStopCommand:     "#!/bin/sh\necho bye",
			PreStopHooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"echo", "cleanup"}},
			},
			PostStopHostCommand: "echo stopped",
			StopTimeout:         10,
			TickerCh:            testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "post_start hook failed") {
			t.Errorf("expected error to contain 'post_start hook failed', got '%s'", err.Error())
		}

		// Expected order: start, hook:sh -c fail (fails), host-command (pre-stop host), pre-stop-command, hook:echo cleanup (pre-stop hook), terminate, host-command (post-stop host)
		// No healthcheck operation should appear in the list
		expectedOps := []string{
			"start",
			"hook:sh -c fail",
			"host-command",
			"pre-stop-command",
			"hook:echo cleanup",
			"terminate",
			"host-command",
		}
		if len(operations) != len(expectedOps) {
			t.Fatalf("expected %d operations, got %d: %v", len(expectedOps), len(operations), operations)
		}
		for i := range expectedOps {
			if operations[i] != expectedOps[i] {
				t.Errorf("operation[%d]: expected %q, got %q", i, expectedOps[i], operations[i])
			}
		}
	})

	t.Run("empty post_start hooks are skipped", func(t *testing.T) {
		execCalled := false
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "new1_container_id", Names: []string{"/new1"}},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: true},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				execCalled = true
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			DesiredReplicas:    1,
			Parallelism:        1,
			ExistingContainers: []container.Summary{},
			PostStartHooks:     nil,
			StopTimeout:        10,
			TickerCh:           testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execCalled {
			t.Error("expected no exec calls for empty post_start hooks")
		}
	})
}

func TestPreStopHooksExecutionOrder(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("scale down executes hooks after pre-stop command and before terminate", func(t *testing.T) {
		var mu sync.Mutex
		var operations []string

		mock := &mockDockerClient{
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
				mu.Lock()
				// Distinguish between script exec (has /tmp/pre-stop.sh in Cmd) and hook exec
				isScript := false
				for _, c := range config.Cmd {
					if strings.Contains(c, "pre-stop.sh") {
						isScript = true
						break
					}
				}
				if isScript {
					operations = append(operations, "pre-stop-command")
				} else {
					operations = append(operations, "hook:"+strings.Join(config.Cmd, " "))
				}
				mu.Unlock()
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (dockerTypes.HijackedResponse, error) {
				reader := strings.NewReader("")
				return dockerTypes.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				mu.Lock()
				operations = append(operations, "terminate")
				mu.Unlock()
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			mu.Lock()
			operations = append(operations, "host-command")
			mu.Unlock()
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleDownContainersInput{
			Client: mock,
			CurrentContainers: []container.Summary{
				{ID: "container1_to_remove", Created: 100},
			},
			CurrentReplicas: 1,
			DesiredReplicas: 0,
			Executor:        executor,
			Logger:          logger,
			ProjectName:     "proj",
			ServiceName:     "web",
			PreStopHostCommand: "echo stopping",
			PreStopCommand:     "#!/bin/sh\necho bye",
			PreStopHooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"nginx", "-s", "quit"}},
				{Command: composeTypes.ShellCommand{"echo", "done"}},
			},
			PostStopHostCommand: "echo stopped",
			StopTimeout:         10,
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expected order: host-command, pre-stop-command, hook:nginx -s quit, hook:echo done, terminate, host-command
		expectedOps := []string{
			"host-command",
			"pre-stop-command",
			"hook:nginx -s quit",
			"hook:echo done",
			"terminate",
			"host-command",
		}
		if len(operations) != len(expectedOps) {
			t.Fatalf("expected %d operations, got %d: %v", len(expectedOps), len(operations), operations)
		}
		for i := range expectedOps {
			if operations[i] != expectedOps[i] {
				t.Errorf("operation[%d]: expected %q, got %q", i, expectedOps[i], operations[i])
			}
		}
	})

	t.Run("rolling update stop-first executes hooks before terminate", func(t *testing.T) {
		var mu sync.Mutex
		var hookCmds []string
		terminateCalled := false

		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "existing1", Created: 100},
					}, nil
				}
				return []container.Summary{
					{ID: "existing1", Created: 100},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: true},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				// Only track hook calls (not script calls)
				isScript := false
				for _, c := range config.Cmd {
					if strings.Contains(c, "pre-stop.sh") {
						isScript = true
						break
					}
				}
				if !isScript {
					mu.Lock()
					hookCmds = append(hookCmds, strings.Join(config.Cmd, " "))
					mu.Unlock()
				}
				return container.ExecCreateResponse{ID: "exec-123"}, nil
			},
			containerExecStart: func(ctx context.Context, execID string, config container.ExecStartOptions) error {
				return nil
			},
			containerExecAttach: func(ctx context.Context, execID string, config container.ExecAttachOptions) (dockerTypes.HijackedResponse, error) {
				reader := strings.NewReader("")
				return dockerTypes.HijackedResponse{
					Conn:   &mockConn{},
					Reader: bufio.NewReader(reader),
				}, nil
			},
			containerExecInspect: func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: 0}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				mu.Lock()
				terminateCalled = true
				// Hooks should have been called before terminate
				if len(hookCmds) == 0 {
					t.Error("expected hooks to be called before terminate")
				}
				mu.Unlock()
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			PreStopHooks: []composeTypes.ServiceHook{
				{Command: composeTypes.ShellCommand{"kill", "-TERM", "1"}},
			},
			StopTimeout: 10,
			TickerCh:    testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !terminateCalled {
			t.Error("expected terminate to be called")
		}
		if len(hookCmds) != 1 || hookCmds[0] != "kill -TERM 1" {
			t.Errorf("expected hook command 'kill -TERM 1', got %v", hookCmds)
		}
	})
}

func TestRollingUpdateBatchStartFirstWaitAfterHealthy(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("sleeper called with correct duration after healthy", func(t *testing.T) {
		var sleeperDurations []time.Duration
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			WaitAfterHealthy:   5 * time.Second,
			Sleeper: func(d time.Duration) {
				sleeperDurations = append(sleeperDurations, d)
			},
			StopTimeout: 10,
			TickerCh:    testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		found := false
		for _, d := range sleeperDurations {
			if d == 5*time.Second {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sleeper to be called with 5s, got calls: %v", sleeperDurations)
		}
	})

	t.Run("no sleep when WaitAfterHealthy is zero", func(t *testing.T) {
		buf.Reset()
		var sleeperCalled bool
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{
						{ID: "old1_container_id", Created: 50},
					}, nil
				}
				return []container.Summary{
					{ID: "old1_container_id", Created: 50},
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			WaitAfterHealthy:   0,
			Sleeper: func(d time.Duration) {
				sleeperCalled = true
			},
			StopTimeout: 10,
			TickerCh:    testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStartFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if sleeperCalled {
			t.Error("expected sleeper not to be called when WaitAfterHealthy is 0")
		}
	})
}

func TestRollingUpdateBatchStopFirstWaitAfterHealthy(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("sleeper called with correct duration after healthy", func(t *testing.T) {
		var sleeperDurations []time.Duration
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				if listCallCount == 1 {
					return []container.Summary{}, nil
				}
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		batch := []container.Summary{
			{ID: "old1_container_id", Created: 50},
		}

		input := RollingUpdateInput{
			Client:             mock,
			Executor:           executor,
			Logger:             logger,
			ProjectName:        "proj",
			ServiceName:        "web",
			Parallelism:        1,
			MaxFailureRatio:    0,
			ContainersToUpdate: batch,
			WaitAfterHealthy:   3 * time.Second,
			Sleeper: func(d time.Duration) {
				sleeperDurations = append(sleeperDurations, d)
			},
			StopTimeout: 10,
			TickerCh:    testTickerCh(),
		}

		output := &RollingUpdateOutput{}
		err := rollingUpdateBatchStopFirst(ctx, input, batch, output)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		found := false
		for _, d := range sleeperDurations {
			if d == 3*time.Second {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sleeper to be called with 3s, got calls: %v", sleeperDurations)
		}
	})
}

func TestScaleUpContainersWaitAfterHealthy(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("sleeper called with correct duration after healthy", func(t *testing.T) {
		var sleeperDurations []time.Duration
		listCallCount := 0
		mock := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				listCallCount++
				return []container.Summary{
					{ID: "new1_container_id", Created: 300},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
						},
					},
				}, nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				return nil
			},
		}

		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		input := ScaleUpContainersInput{
			Client:          mock,
			Executor:        executor,
			Logger:          logger,
			ProjectName:     "proj",
			ProjectDir:      "/tmp",
			ServiceName:     "web",
			ComposeFiles:    []string{"/tmp/docker-compose.yaml"},
			Parallelism:     1,
			CurrentReplicas: 0,
			DesiredReplicas: 1,
			WaitAfterHealthy: 4 * time.Second,
			Sleeper: func(d time.Duration) {
				sleeperDurations = append(sleeperDurations, d)
			},
			TickerCh: testTickerCh(),
		}

		err := scaleUpContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		found := false
		for _, d := range sleeperDurations {
			if d == 4*time.Second {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sleeper to be called with 4s, got calls: %v", sleeperDurations)
		}
	})
}

func TestSortContainersByHealthThenCreationTime(t *testing.T) {
	ctx := context.Background()

	t.Run("unhealthy_first_then_oldest", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "healthy_new", Created: 300},
			{ID: "unhealthy_old", Created: 100},
			{ID: "healthy_old", Created: 200},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "unhealthy_old":
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "unhealthy"},
							},
						},
					}, nil
				default:
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "healthy"},
							},
						},
					}, nil
				}
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 1 {
			t.Errorf("expected 1 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy_old" {
			t.Errorf("expected unhealthy_old first, got %s", containers[0].ID)
		}
		if containers[1].ID != "healthy_old" {
			t.Errorf("expected healthy_old second, got %s", containers[1].ID)
		}
		if containers[2].ID != "healthy_new" {
			t.Errorf("expected healthy_new third, got %s", containers[2].ID)
		}
	})

	t.Run("all_healthy_falls_back_to_creation_time", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "newest", Created: 300},
			{ID: "oldest", Created: 100},
			{ID: "middle", Created: 200},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{Status: "healthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 0 {
			t.Errorf("expected 0 unhealthy, got %d", count)
		}
		if containers[0].ID != "oldest" {
			t.Errorf("expected oldest first, got %s", containers[0].ID)
		}
		if containers[1].ID != "middle" {
			t.Errorf("expected middle second, got %s", containers[1].ID)
		}
		if containers[2].ID != "newest" {
			t.Errorf("expected newest third, got %s", containers[2].ID)
		}
	})

	t.Run("all_unhealthy_sorted_by_creation_time", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "newest", Created: 300},
			{ID: "oldest", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{Status: "unhealthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 2 {
			t.Errorf("expected 2 unhealthy, got %d", count)
		}
		if containers[0].ID != "oldest" {
			t.Errorf("expected oldest first, got %s", containers[0].ID)
		}
		if containers[1].ID != "newest" {
			t.Errorf("expected newest second, got %s", containers[1].ID)
		}
	})

	t.Run("no_healthcheck_treated_as_healthy", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "no_healthcheck", Created: 200},
			{ID: "unhealthy", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "unhealthy":
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "unhealthy"},
							},
						},
					}, nil
				default:
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: nil,
							},
						},
					}, nil
				}
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 1 {
			t.Errorf("expected 1 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy" {
			t.Errorf("expected unhealthy first, got %s", containers[0].ID)
		}
		if containers[1].ID != "no_healthcheck" {
			t.Errorf("expected no_healthcheck second, got %s", containers[1].ID)
		}
	})

	t.Run("inspect_error_treated_as_healthy", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "error_container", Created: 200},
			{ID: "unhealthy", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "unhealthy":
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "unhealthy"},
							},
						},
					}, nil
				default:
					return container.InspectResponse{}, errors.New("inspect failed")
				}
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 1 {
			t.Errorf("expected 1 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy" {
			t.Errorf("expected unhealthy first, got %s", containers[0].ID)
		}
	})

	t.Run("starting_treated_as_healthy", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "starting_container", Created: 200},
			{ID: "unhealthy", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "unhealthy":
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "unhealthy"},
							},
						},
					}, nil
				default:
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							State: &container.State{
								Health: &container.Health{Status: "starting"},
							},
						},
					}, nil
				}
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 1 {
			t.Errorf("expected 1 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy" {
			t.Errorf("expected unhealthy first, got %s", containers[0].ID)
		}
	})

	t.Run("empty_containers", func(t *testing.T) {
		containers := []container.Summary{}
		mock := &mockDockerClient{}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 0 {
			t.Errorf("expected 0 unhealthy, got %d", count)
		}
		if len(containers) != 0 {
			t.Errorf("expected empty slice, got %d", len(containers))
		}
	})

	t.Run("single_container", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "only_one", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Health: &container.Health{Status: "healthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 0 {
			t.Errorf("expected 0 unhealthy, got %d", count)
		}
		if containers[0].ID != "only_one" {
			t.Errorf("expected only_one, got %s", containers[0].ID)
		}
	})

	t.Run("healthy_with_different_restart_counts", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "no_restarts", Created: 100},
			{ID: "many_restarts", Created: 200},
			{ID: "few_restarts", Created: 150},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				restartCount := 0
				switch id {
				case "many_restarts":
					restartCount = 10
				case "few_restarts":
					restartCount = 3
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						RestartCount: restartCount,
						State: &container.State{
							Health: &container.Health{Status: "healthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 0 {
			t.Errorf("expected 0 unhealthy, got %d", count)
		}
		if containers[0].ID != "many_restarts" {
			t.Errorf("expected many_restarts first, got %s", containers[0].ID)
		}
		if containers[1].ID != "few_restarts" {
			t.Errorf("expected few_restarts second, got %s", containers[1].ID)
		}
		if containers[2].ID != "no_restarts" {
			t.Errorf("expected no_restarts third, got %s", containers[2].ID)
		}
	})

	t.Run("unhealthy_with_different_restart_counts", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "unhealthy_few", Created: 100},
			{ID: "unhealthy_many", Created: 200},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				restartCount := 1
				if id == "unhealthy_many" {
					restartCount = 5
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						RestartCount: restartCount,
						State: &container.State{
							Health: &container.Health{Status: "unhealthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 2 {
			t.Errorf("expected 2 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy_many" {
			t.Errorf("expected unhealthy_many first, got %s", containers[0].ID)
		}
		if containers[1].ID != "unhealthy_few" {
			t.Errorf("expected unhealthy_few second, got %s", containers[1].ID)
		}
	})

	t.Run("unhealthy_no_restarts_before_healthy_many_restarts", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "healthy_many_restarts", Created: 100},
			{ID: "unhealthy_no_restarts", Created: 200},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "unhealthy_no_restarts":
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							RestartCount: 0,
							State: &container.State{
								Health: &container.Health{Status: "unhealthy"},
							},
						},
					}, nil
				default:
					return container.InspectResponse{
						ContainerJSONBase: &container.ContainerJSONBase{
							RestartCount: 10,
							State: &container.State{
								Health: &container.Health{Status: "healthy"},
							},
						},
					}, nil
				}
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 1 {
			t.Errorf("expected 1 unhealthy, got %d", count)
		}
		if containers[0].ID != "unhealthy_no_restarts" {
			t.Errorf("expected unhealthy_no_restarts first, got %s", containers[0].ID)
		}
		if containers[1].ID != "healthy_many_restarts" {
			t.Errorf("expected healthy_many_restarts second, got %s", containers[1].ID)
		}
	})

	t.Run("same_restart_count_falls_back_to_oldest", func(t *testing.T) {
		containers := []container.Summary{
			{ID: "newer", Created: 200},
			{ID: "older", Created: 100},
		}

		mock := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						RestartCount: 5,
						State: &container.State{
							Health: &container.Health{Status: "healthy"},
						},
					},
				}, nil
			},
		}

		count := sortContainersByHealthThenCreationTime(ctx, mock, containers)
		if count != 0 {
			t.Errorf("expected 0 unhealthy, got %d", count)
		}
		if containers[0].ID != "older" {
			t.Errorf("expected older first, got %s", containers[0].ID)
		}
		if containers[1].ID != "newer" {
			t.Errorf("expected newer second, got %s", containers[1].ID)
		}
	})
}
