package internal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
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

	t.Run("no healthcheck running with healthcheck wait", func(t *testing.T) {
		mockClient := &mockDockerClient{
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

		healthcheckWait := 50 * time.Millisecond
		tickerCh := make(chan time.Time, 10)

		input := WaitForHealthcheckInput{
			Client:          mockClient,
			ContainerID:     "test-id",
			Monitor:         10 * time.Millisecond,
			TickerCh:        tickerCh,
			HealthcheckWait: healthcheckWait,
		}

		// Send first tick — should not return yet
		tickerCh <- time.Now()

		startTime := time.Now()
		done := make(chan error, 1)
		go func() {
			done <- waitForDockerHealthCheck(ctx, input)
		}()

		// Send ticks periodically until the wait elapses
		go func() {
			for i := 0; i < 20; i++ {
				time.Sleep(10 * time.Millisecond)
				tickerCh <- time.Now()
			}
		}()

		err := <-done
		elapsed := time.Since(startTime)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if elapsed < healthcheckWait {
			t.Errorf("expected to wait at least %v, but returned after %v", healthcheckWait, elapsed)
		}
	})

	t.Run("no healthcheck running without healthcheck wait returns immediately", func(t *testing.T) {
		callCount := 0
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				callCount++
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: true,
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Errorf("expected 1 call (immediate return), got %d", callCount)
		}
	})

	t.Run("container restarts during healthcheck wait resets timer", func(t *testing.T) {
		callCount := 0
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				callCount++
				running := true
				// Simulate restart: not running on call 2
				if callCount == 2 {
					running = false
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{
							Running: running,
						},
					},
				}, nil
			},
		}

		healthcheckWait := 50 * time.Millisecond
		tickerCh := make(chan time.Time, 30)

		input := WaitForHealthcheckInput{
			Client:          mockClient,
			ContainerID:     "test-id",
			Monitor:         10 * time.Millisecond,
			TickerCh:        tickerCh,
			HealthcheckWait: healthcheckWait,
		}

		startTime := time.Now()
		done := make(chan error, 1)
		go func() {
			done <- waitForDockerHealthCheck(ctx, input)
		}()

		// Send ticks every 10ms
		go func() {
			for i := 0; i < 30; i++ {
				time.Sleep(10 * time.Millisecond)
				tickerCh <- time.Now()
			}
		}()

		err := <-done
		elapsed := time.Since(startTime)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The restart at call 2 (~20ms) should reset the timer,
		// so total time should be longer than a single healthcheckWait
		if elapsed < healthcheckWait {
			t.Errorf("expected to wait at least %v due to restart, but returned after %v", healthcheckWait, elapsed)
		}
		if callCount < 3 {
			t.Errorf("expected at least 3 inspect calls (restart should cause more polling), got %d", callCount)
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

func TestWaitForHealthcheck(t *testing.T) {
	ctx := context.Background()

	t.Run("nil_client_returns_error", func(t *testing.T) {
		err := waitForHealthcheck(ctx, WaitForHealthcheckInput{})
		if err == nil || !strings.Contains(err.Error(), "client is required") {
			t.Errorf("expected client-required error, got: %v", err)
		}
	})

	t.Run("nil_executor_returns_error", func(t *testing.T) {
		err := waitForHealthcheck(ctx, WaitForHealthcheckInput{
			Client: &mockDockerClient{},
		})
		if err == nil || !strings.Contains(err.Error(), "executor is required") {
			t.Errorf("expected executor-required error, got: %v", err)
		}
	})

	t.Run("docker_healthcheck_error_propagates", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("inspect boom")
			},
		}
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{}, nil
		}
		tickerCh := make(chan time.Time, 1)
		tickerCh <- time.Now()
		err := waitForHealthcheck(ctx, WaitForHealthcheckInput{
			Client:      mockClient,
			ContainerID: "abc",
			Executor:    executor,
			Monitor:     1 * time.Millisecond,
			TickerCh:    tickerCh,
		})
		if err == nil || !strings.Contains(err.Error(), "error inspecting container") {
			t.Errorf("expected inspect error, got: %v", err)
		}
	})

	t.Run("success_path_runs_host_script", func(t *testing.T) {
		running := true
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
						State: &container.State{
							Running: running,
							Health:  &container.Health{Status: "healthy"},
						},
					},
					Config: &container.Config{},
				}, nil
			},
		}
		executor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{}, nil
		}
		tickerCh := make(chan time.Time, 1)
		tickerCh <- time.Now()
		err := waitForHealthcheck(ctx, WaitForHealthcheckInput{
			Client:             mockClient,
			ContainerID:        "abc",
			Executor:           executor,
			Monitor:            1 * time.Millisecond,
			TickerCh:           tickerCh,
			HealthcheckCommand: "", // empty script = noop runHostScript
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
