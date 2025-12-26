package internal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
			containerTerminate: func(ctx context.Context, id string) error {
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
		}

		err := scaleDownContainers(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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
