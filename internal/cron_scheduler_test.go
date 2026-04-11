package internal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/rs/zerolog"
)

func newBufferLogger(buf *bytes.Buffer) *command.ZerologUi {
	return &command.ZerologUi{
		StderrLogger:      zerolog.New(buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}
}

func buildSchedulerProject(name, serviceName, schedule, timezone string) CronProject {
	return CronProject{
		Name: name,
		Services: []CronService{
			{
				Name: serviceName,
				Config: &CronConfig{
					Schedule: schedule,
					Timezone: timezone,
				},
			},
		},
	}
}

func noopSpawner(ctx context.Context, project CronProject, service CronService) error {
	return nil
}

func TestNewCronScheduler(t *testing.T) {
	t.Run("valid_every_schedule", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		project := buildSchedulerProject("proj1", "job", "@every 1m", "")
		scheduler, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if scheduler == nil {
			t.Fatal("expected scheduler, got nil")
		}
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})

	t.Run("empty_projects", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := scheduler.Entries(); got != 0 {
			t.Errorf("expected 0 entries, got %d", got)
		}
	})

	t.Run("invalid_schedule_returns_error", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		project := buildSchedulerProject("bad", "job", "not-a-cron-expression", "")
		_, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
		if err == nil {
			t.Fatal("expected error for invalid schedule")
		}
	})

	t.Run("invalid_timezone_returns_error", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		project := buildSchedulerProject("badtz", "job", "* * * * *", "Not/A_Real_Zone")
		_, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
		if err == nil {
			t.Fatal("expected error for invalid timezone")
		}
	})

	t.Run("every_with_timezone", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		project := buildSchedulerProject("tz", "job", "@every 1h", "UTC")
		scheduler, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})

	t.Run("standard_cron_with_timezone_prepends_CRON_TZ", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		project := buildSchedulerProject("ny", "job", "0 0 * * *", "America/New_York")
		scheduler, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})
}

func TestCronSchedulerStartStop(t *testing.T) {
	var buf bytes.Buffer
	logger := newBufferLogger(&buf)
	project := buildSchedulerProject("proj", "job", "@every 1h", "")
	scheduler, err := NewCronScheduler([]CronProject{project}, noopSpawner, logger)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	scheduler.Start()
	stopCtx := scheduler.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop within 2 seconds")
	}
}

func TestCronSchedulerReload(t *testing.T) {
	t.Run("add_new_job", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		project := buildSchedulerProject("proj", "job", "@every 1m", "")
		if err := scheduler.Reload([]CronProject{project}); err != nil {
			t.Fatalf("reload failed: %v", err)
		}
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})

	t.Run("remove_missing_jobs", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		initial := buildSchedulerProject("proj", "job", "@every 1m", "")
		scheduler, err := NewCronScheduler([]CronProject{initial}, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		if err := scheduler.Reload(nil); err != nil {
			t.Fatalf("reload failed: %v", err)
		}
		if got := scheduler.Entries(); got != 0 {
			t.Errorf("expected 0 entries, got %d", got)
		}
		if !strings.Contains(buf.String(), "Removed cron job") {
			t.Errorf("expected 'Removed cron job' in logs, got: %s", buf.String())
		}
	})

	t.Run("update_existing_job", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		initial := buildSchedulerProject("proj", "job", "@every 1m", "")
		scheduler, err := NewCronScheduler([]CronProject{initial}, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		updated := buildSchedulerProject("proj", "job", "@every 5m", "")
		if err := scheduler.Reload([]CronProject{updated}); err != nil {
			t.Fatalf("reload failed: %v", err)
		}
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})

	t.Run("addJob_error_is_logged_not_returned", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		badProject := buildSchedulerProject("bad", "job", "not-a-valid-schedule", "")
		if err := scheduler.Reload([]CronProject{badProject}); err != nil {
			t.Fatalf("reload should not return error for addJob failure: %v", err)
		}
		if got := scheduler.Entries(); got != 0 {
			t.Errorf("expected 0 entries, got %d", got)
		}
		if !strings.Contains(buf.String(), "Error adding cron job") {
			t.Errorf("expected 'Error adding cron job' in logs, got: %s", buf.String())
		}
	})
}

func TestCronSchedulerJobFunc(t *testing.T) {
	t.Run("spawner_invoked_on_fire", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		var spawnCount atomic.Int32
		spawner := func(ctx context.Context, project CronProject, service CronService) error {
			spawnCount.Add(1)
			return nil
		}
		project := buildSchedulerProject("proj", "job", "@every 1h", "")
		scheduler, err := NewCronScheduler([]CronProject{project}, spawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		entries := scheduler.cron.Entries()
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		entries[0].Job.Run()
		if spawnCount.Load() != 1 {
			t.Errorf("expected spawner called once, got %d", spawnCount.Load())
		}
		if !strings.Contains(buf.String(), "Triggering cron job") {
			t.Errorf("expected trigger log, got: %s", buf.String())
		}
	})

	t.Run("spawner_error_is_logged", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		spawner := func(ctx context.Context, project CronProject, service CronService) error {
			return errors.New("spawner boom")
		}
		project := buildSchedulerProject("proj", "job", "@every 1h", "")
		scheduler, err := NewCronScheduler([]CronProject{project}, spawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		entries := scheduler.cron.Entries()
		entries[0].Job.Run()
		if !strings.Contains(buf.String(), "failed to spawn") {
			t.Errorf("expected spawner error log, got: %s", buf.String())
		}
	})
}

func TestCronSchedulerDoReload(t *testing.T) {
	t.Run("success_applies_new_projects", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		loader := func() ([]CronProject, error) {
			return []CronProject{buildSchedulerProject("loaded", "job", "@every 1m", "")}, nil
		}
		scheduler.doReload(loader)
		if got := scheduler.Entries(); got != 1 {
			t.Errorf("expected 1 entry, got %d", got)
		}
	})

	t.Run("loader_error_is_logged", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newBufferLogger(&buf)
		scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}
		loader := func() ([]CronProject, error) {
			return nil, errors.New("load failed")
		}
		scheduler.doReload(loader)
		if !strings.Contains(buf.String(), "Error reloading configuration") {
			t.Errorf("expected reload-error log, got: %s", buf.String())
		}
	})
}

func TestCronSchedulerRunConfigReloader(t *testing.T) {
	var buf bytes.Buffer
	logger := newBufferLogger(&buf)
	scheduler, err := NewCronScheduler(nil, noopSpawner, logger)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var loadCount atomic.Int32
	loader := func() ([]CronProject, error) {
		loadCount.Add(1)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	scheduler.RunConfigReloader(ctx, 20*time.Millisecond, loader)

	deadline := time.Now().Add(2 * time.Second)
	for loadCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)

	if loadCount.Load() < 1 {
		t.Errorf("expected loader to be called at least once, got %d", loadCount.Load())
	}
}
