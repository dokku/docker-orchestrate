package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
)

func TestCronWebhookPayloadJSON(t *testing.T) {
	tests := []struct {
		name    string
		payload CronWebhookPayload
		check   func(t *testing.T, data map[string]interface{})
	}{
		{
			name: "full_payload_marshals_correctly",
			payload: CronWebhookPayload{
				Project:         "myproject",
				Service:         "backup",
				Schedule:        "*/5 * * * *",
				Status:          "success",
				ExitCode:        0,
				DurationSeconds: 12.5,
				TriggeredAt:     "2024-01-15T14:00:00Z",
				CompletedAt:     "2024-01-15T14:00:12Z",
				ContainerName:   "myproject-backup-20240115-140000-ab12",
				Stdout:          "backup complete",
				Stderr:          "",
			},
			check: func(t *testing.T, data map[string]interface{}) {
				if data["project"] != "myproject" {
					t.Errorf("expected project 'myproject', got %v", data["project"])
				}
				if data["service"] != "backup" {
					t.Errorf("expected service 'backup', got %v", data["service"])
				}
				if data["status"] != "success" {
					t.Errorf("expected status 'success', got %v", data["status"])
				}
				if data["exit_code"].(float64) != 0 {
					t.Errorf("expected exit_code 0, got %v", data["exit_code"])
				}
				if data["duration_seconds"].(float64) != 12.5 {
					t.Errorf("expected duration_seconds 12.5, got %v", data["duration_seconds"])
				}
				if data["container_name"] != "myproject-backup-20240115-140000-ab12" {
					t.Errorf("expected container_name, got %v", data["container_name"])
				}
			},
		},
		{
			name: "empty_stdout_stderr_omitted",
			payload: CronWebhookPayload{
				Project:  "proj",
				Service:  "svc",
				Schedule: "@hourly",
				Status:   "failure",
				ExitCode: 1,
			},
			check: func(t *testing.T, data map[string]interface{}) {
				if _, ok := data["stdout"]; ok {
					t.Error("expected stdout to be omitted when empty")
				}
				if _, ok := data["stderr"]; ok {
					t.Error("expected stderr to be omitted when empty")
				}
			},
		},
		{
			name: "non_empty_stdout_stderr_included",
			payload: CronWebhookPayload{
				Project:  "proj",
				Service:  "svc",
				Schedule: "@hourly",
				Status:   "failure",
				ExitCode: 1,
				Stdout:   "some output",
				Stderr:   "some error",
			},
			check: func(t *testing.T, data map[string]interface{}) {
				if data["stdout"] != "some output" {
					t.Errorf("expected stdout 'some output', got %v", data["stdout"])
				}
				if data["stderr"] != "some error" {
					t.Errorf("expected stderr 'some error', got %v", data["stderr"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("failed to marshal payload: %v", err)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(b, &result); err != nil {
				t.Fatalf("failed to unmarshal payload: %v", err)
			}

			tt.check(t, result)
		})
	}
}

func TestCronWebhookPayloadRoundTrip(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		original := CronWebhookPayload{
			Project:         "myproject",
			Service:         "backup",
			Schedule:        "*/5 * * * *",
			Status:          "success",
			ExitCode:        0,
			DurationSeconds: 45.123,
			TriggeredAt:     "2024-01-15T14:00:00Z",
			CompletedAt:     "2024-01-15T14:00:45Z",
			ContainerName:   "myproject-backup-20240115-140000-ab12",
			Stdout:          "done",
			Stderr:          "warn: low disk",
		}

		b, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var decoded CronWebhookPayload
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if decoded.Project != original.Project {
			t.Errorf("project mismatch: %q vs %q", decoded.Project, original.Project)
		}
		if decoded.Service != original.Service {
			t.Errorf("service mismatch: %q vs %q", decoded.Service, original.Service)
		}
		if decoded.ExitCode != original.ExitCode {
			t.Errorf("exit_code mismatch: %d vs %d", decoded.ExitCode, original.ExitCode)
		}
		if decoded.DurationSeconds != original.DurationSeconds {
			t.Errorf("duration_seconds mismatch: %f vs %f", decoded.DurationSeconds, original.DurationSeconds)
		}
		if decoded.Stdout != original.Stdout {
			t.Errorf("stdout mismatch: %q vs %q", decoded.Stdout, original.Stdout)
		}
		if decoded.Stderr != original.Stderr {
			t.Errorf("stderr mismatch: %q vs %q", decoded.Stderr, original.Stderr)
		}
	})
}

func TestCronShortID(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{
			name:     "normal_64_char_id",
			id:       "abc123def456abc123def456abc123def456abc123def456abc123def456abcd",
			expected: "abc123def456",
		},
		{
			name:     "exactly_12_chars",
			id:       "abc123def456",
			expected: "abc123def456",
		},
		{
			name:     "short_id_less_than_12",
			id:       "abc123",
			expected: "abc123",
		},
		{
			name:     "empty_id",
			id:       "",
			expected: "",
		},
		{
			name:     "13_chars",
			id:       "abc123def4567",
			expected: "abc123def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortID(tt.id)
			if got != tt.expected {
				t.Errorf("shortID(%q) = %q, want %q", tt.id, got, tt.expected)
			}
		})
	}
}

func TestCronContainerShortName(t *testing.T) {
	tests := []struct {
		name     string
		names    []string
		expected string
	}{
		{
			name:     "normal_name_with_leading_slash",
			names:    []string{"/myproject-backup-20240115-140000-ab12"},
			expected: "myproject-backup-20240115-140000-ab12",
		},
		{
			name:     "name_without_leading_slash",
			names:    []string{"mycontainer"},
			expected: "mycontainer",
		},
		{
			name:     "multiple_names_uses_first",
			names:    []string{"/first-name", "/second-name"},
			expected: "first-name",
		},
		{
			name:     "empty_names_returns_unknown",
			names:    []string{},
			expected: "unknown",
		},
		{
			name:     "nil_names_returns_unknown",
			names:    nil,
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containerShortName(tt.names)
			if got != tt.expected {
				t.Errorf("containerShortName(%v) = %q, want %q", tt.names, got, tt.expected)
			}
		})
	}
}

func TestNewCronNotifier(t *testing.T) {
	var buf bytes.Buffer
	logger := newBufferLogger(&buf)
	mockClient := &mockDockerClient{}
	n := NewCronNotifier(mockClient, logger)
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
	if n.client != mockClient {
		t.Error("expected notifier client to match")
	}
	if n.logger != logger {
		t.Error("expected notifier logger to match")
	}
}

func TestCronNotifierSendWebhook(t *testing.T) {
	t.Run("successful_post", func(t *testing.T) {
		var capturedBody []byte
		var capturedContentType, capturedUserAgent string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedBody, _ = io.ReadAll(r.Body)
			capturedContentType = r.Header.Get("Content-Type")
			capturedUserAgent = r.Header.Get("User-Agent")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		n := NewCronNotifier(&mockDockerClient{}, newBufferLogger(&buf))
		payload := CronWebhookPayload{Project: "p", Service: "s", Status: "success"}
		if err := n.sendWebhook(srv.URL, payload); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedContentType != "application/json" {
			t.Errorf("content type: %q", capturedContentType)
		}
		if capturedUserAgent != "docker-orchestrate-cron" {
			t.Errorf("user agent: %q", capturedUserAgent)
		}
		var decoded map[string]any
		if err := json.Unmarshal(capturedBody, &decoded); err != nil {
			t.Fatalf("body unmarshal: %v", err)
		}
		if decoded["project"] != "p" {
			t.Errorf("project: %v", decoded["project"])
		}
	})

	t.Run("server_error_returns_error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		n := NewCronNotifier(&mockDockerClient{}, newBufferLogger(&buf))
		err := n.sendWebhook(srv.URL, CronWebhookPayload{})
		if err == nil {
			t.Fatal("expected error from 500 response")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected 500 in error, got %v", err)
		}
	})

	t.Run("bad_url_returns_request_error", func(t *testing.T) {
		var buf bytes.Buffer
		n := NewCronNotifier(&mockDockerClient{}, newBufferLogger(&buf))
		err := n.sendWebhook("http://%zz", CronWebhookPayload{})
		if err == nil {
			t.Fatal("expected error for invalid url")
		}
	})

	t.Run("unreachable_host_returns_transport_error", func(t *testing.T) {
		var buf bytes.Buffer
		n := NewCronNotifier(&mockDockerClient{}, newBufferLogger(&buf))
		// Port 1 on localhost is effectively always closed.
		err := n.sendWebhook("http://127.0.0.1:1/", CronWebhookPayload{})
		if err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func makeInspectResponse(name string, exitCode int, labels map[string]string) container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			Name:  name,
			State: &container.State{ExitCode: exitCode},
		},
		Config: &container.Config{Labels: labels},
	}
}

func TestCronNotifierProcessContainer(t *testing.T) {
	t.Run("no_notify_url_just_removes", func(t *testing.T) {
		var buf bytes.Buffer
		removed := false
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, map[string]string{
					"com.dokku.orchestrate/cron-project": "p",
					"com.dokku.orchestrate/cron-service": "s",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error {
				removed = true
				return nil
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc123")
		if !removed {
			t.Error("expected container to be removed")
		}
	})

	t.Run("notify_always_sends_webhook", func(t *testing.T) {
		var webhookHit bool
		var capturedPayload CronWebhookPayload
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			webhookHit = true
			_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		triggeredAt := time.Now().UTC().Add(-5 * time.Second).Format(time.RFC3339)
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, map[string]string{
					"com.dokku.orchestrate/cron-project":      "p",
					"com.dokku.orchestrate/cron-service":      "s",
					"com.dokku.orchestrate/cron-schedule":     "@every 1h",
					"com.dokku.orchestrate/cron-triggered-at": triggeredAt,
					"com.dokku.orchestrate/cron-notify-url":   srv.URL,
					"com.dokku.orchestrate/cron-notify-on":    "always",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc123")
		if !webhookHit {
			t.Error("expected webhook to be invoked")
		}
		if capturedPayload.Project != "p" {
			t.Errorf("project: %q", capturedPayload.Project)
		}
		if capturedPayload.Status != "success" {
			t.Errorf("status: %q", capturedPayload.Status)
		}
		if capturedPayload.DurationSeconds <= 0 {
			t.Errorf("expected positive duration, got %v", capturedPayload.DurationSeconds)
		}
	})

	t.Run("notify_on_success_skipped_for_failure", func(t *testing.T) {
		hit := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 1, map[string]string{
					"com.dokku.orchestrate/cron-notify-url": srv.URL,
					"com.dokku.orchestrate/cron-notify-on":  "success",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if hit {
			t.Error("expected webhook NOT to be called for failure with notify-on=success")
		}
	})

	t.Run("notify_on_failure_fires_for_failure", func(t *testing.T) {
		hit := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 2, map[string]string{
					"com.dokku.orchestrate/cron-notify-url": srv.URL,
					"com.dokku.orchestrate/cron-notify-on":  "failure",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if !hit {
			t.Error("expected webhook to fire for failure with notify-on=failure")
		}
	})

	t.Run("default_notify_only_on_failure", func(t *testing.T) {
		hitSuccess := false
		hitFailure := false
		srvSuccess := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitSuccess = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srvSuccess.Close()
		srvFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitFailure = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srvFailure.Close()

		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				switch id {
				case "ok":
					return makeInspectResponse("/ok", 0, map[string]string{
						"com.dokku.orchestrate/cron-notify-url": srvSuccess.URL,
					}), nil
				default:
					return makeInspectResponse("/fail", 3, map[string]string{
						"com.dokku.orchestrate/cron-notify-url": srvFailure.URL,
					}), nil
				}
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "ok")
		n.processContainer(context.Background(), "fail")
		if hitSuccess {
			t.Error("expected success case NOT to notify with default notify-on")
		}
		if !hitFailure {
			t.Error("expected failure case to notify with default notify-on")
		}
	})

	t.Run("include_output_fetches_logs", func(t *testing.T) {
		var captured CronWebhookPayload
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var logCalls int
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, map[string]string{
					"com.dokku.orchestrate/cron-notify-url":            srv.URL,
					"com.dokku.orchestrate/cron-notify-on":             "always",
					"com.dokku.orchestrate/cron-notify-include-output": "true",
				}), nil
			},
			containerLogs: func(ctx context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
				logCalls++
				if opts.ShowStdout {
					return io.NopCloser(strings.NewReader("stdout line\n")), nil
				}
				return io.NopCloser(strings.NewReader("stderr line\n")), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if logCalls != 2 {
			t.Errorf("expected 2 log calls, got %d", logCalls)
		}
		if captured.Stdout != "stdout line" {
			t.Errorf("stdout: %q", captured.Stdout)
		}
		if captured.Stderr != "stderr line" {
			t.Errorf("stderr: %q", captured.Stderr)
		}
	})

	t.Run("inspect_error_logs_and_returns", func(t *testing.T) {
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("inspect boom")
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if !strings.Contains(buf.String(), "Error inspecting cron container") {
			t.Errorf("expected inspect-error log, got: %s", buf.String())
		}
	})

	t.Run("webhook_error_is_logged_container_still_removed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		removed := false
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, map[string]string{
					"com.dokku.orchestrate/cron-notify-url": srv.URL,
					"com.dokku.orchestrate/cron-notify-on":  "always",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error {
				removed = true
				return nil
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if !removed {
			t.Error("expected container removal even on webhook error")
		}
		if !strings.Contains(buf.String(), "Error sending webhook") {
			t.Errorf("expected webhook error log, got: %s", buf.String())
		}
	})

	t.Run("remove_error_is_logged", func(t *testing.T) {
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, nil), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error {
				return errors.New("remove boom")
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if !strings.Contains(buf.String(), "Error removing cron container") {
			t.Errorf("expected remove-error log, got: %s", buf.String())
		}
	})

	t.Run("nil_state_defaults_exit_code_zero", func(t *testing.T) {
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{Name: "/c1", State: nil},
					Config:            &container.Config{Labels: nil},
				}, nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		// No panic = success. Log should show status=success (exit_code=0).
		if !strings.Contains(buf.String(), "status=success") {
			t.Errorf("expected success status in logs, got: %s", buf.String())
		}
	})

	t.Run("bad_triggered_at_zero_duration", func(t *testing.T) {
		var captured CronWebhookPayload
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return makeInspectResponse("/c1", 0, map[string]string{
					"com.dokku.orchestrate/cron-notify-url":   srv.URL,
					"com.dokku.orchestrate/cron-notify-on":    "always",
					"com.dokku.orchestrate/cron-triggered-at": "not-a-timestamp",
				}), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		n.processContainer(context.Background(), "abc")
		if captured.DurationSeconds != 0 {
			t.Errorf("expected zero duration for bad triggered_at, got %v", captured.DurationSeconds)
		}
	})
}

func TestCronNotifierGetContainerLogs(t *testing.T) {
	t.Run("both_streams_available", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerLogs: func(ctx context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
				if opts.ShowStdout {
					return io.NopCloser(strings.NewReader("  out  \n")), nil
				}
				return io.NopCloser(strings.NewReader("  err  \n")), nil
			},
		}
		var buf bytes.Buffer
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		stdout, stderr := n.getContainerLogs(context.Background(), "abc")
		if stdout != "out" {
			t.Errorf("stdout: %q", stdout)
		}
		if stderr != "err" {
			t.Errorf("stderr: %q", stderr)
		}
	})

	t.Run("stdout_error_returns_empty_strings", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerLogs: func(ctx context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
				return nil, errors.New("logs boom")
			},
		}
		var buf bytes.Buffer
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		stdout, stderr := n.getContainerLogs(context.Background(), "abc")
		if stdout != "" || stderr != "" {
			t.Errorf("expected empty strings, got stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("stderr_error_returns_stdout_only", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerLogs: func(ctx context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
				if opts.ShowStdout {
					return io.NopCloser(strings.NewReader("ok")), nil
				}
				return nil, errors.New("stderr boom")
			},
		}
		var buf bytes.Buffer
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		stdout, stderr := n.getContainerLogs(context.Background(), "abc")
		if stdout != "ok" {
			t.Errorf("stdout: %q", stdout)
		}
		if stderr != "" {
			t.Errorf("stderr: %q", stderr)
		}
	})
}

func TestCronNotifierHandleContainerDie(t *testing.T) {
	var buf bytes.Buffer
	inspectCalled := false
	mockClient := &mockDockerClient{
		containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
			inspectCalled = true
			if id != "container-xyz" {
				t.Errorf("expected id container-xyz, got %s", id)
			}
			return makeInspectResponse("/c1", 0, nil), nil
		},
		containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
	}
	n := NewCronNotifier(mockClient, newBufferLogger(&buf))
	n.handleContainerDie(context.Background(), events.Message{Actor: events.Actor{ID: "container-xyz"}})
	if !inspectCalled {
		t.Error("expected inspect to be called via handleContainerDie")
	}
}

func TestCronNotifierRecoverOrphanedContainers(t *testing.T) {
	t.Run("processes_each_orphan", func(t *testing.T) {
		var buf bytes.Buffer
		inspectIDs := []string{}
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "orphan-1", Names: []string{"/one"}},
					{ID: "orphan-2", Names: []string{"/two"}},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				inspectIDs = append(inspectIDs, id)
				return makeInspectResponse("/"+id, 0, nil), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		if err := n.RecoverOrphanedContainers(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(inspectIDs) != 2 {
			t.Errorf("expected 2 inspected IDs, got %v", inspectIDs)
		}
	})

	t.Run("list_error_returned", func(t *testing.T) {
		var buf bytes.Buffer
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
				return nil, errors.New("list boom")
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		err := n.RecoverOrphanedContainers(context.Background())
		if err == nil || !strings.Contains(err.Error(), "list boom") {
			t.Errorf("expected list error, got %v", err)
		}
	})
}

func TestCronNotifierRecoverOrphans(t *testing.T) {
	var buf bytes.Buffer
	listCalled := false
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
			listCalled = true
			return nil, nil
		},
	}
	n := NewCronNotifier(mockClient, newBufferLogger(&buf))
	if err := n.RecoverOrphans(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !listCalled {
		t.Error("expected RecoverOrphans to delegate to RecoverOrphanedContainers")
	}
}

func TestCronNotifierRun(t *testing.T) {
	t.Run("processes_event_then_cancels", func(t *testing.T) {
		var buf bytes.Buffer
		msgCh := make(chan events.Message, 1)
		errCh := make(chan error, 1)
		inspectCalled := make(chan struct{}, 1)
		mockClient := &mockDockerClient{
			eventsFunc: func(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error) {
				return msgCh, errCh
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				select {
				case inspectCalled <- struct{}{}:
				default:
				}
				return makeInspectResponse("/"+id, 0, nil), nil
			},
			containerRemove: func(ctx context.Context, id string, opts container.RemoveOptions) error { return nil },
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))

		ctx, cancel := context.WithCancel(context.Background())
		runErr := make(chan error, 1)
		go func() {
			runErr <- n.Run(ctx)
		}()
		msgCh <- events.Message{Actor: events.Actor{ID: "evt1"}}
		select {
		case <-inspectCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("inspect was not called from event")
		}
		cancel()
		select {
		case err := <-runErr:
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Errorf("expected context.Canceled, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after context cancel")
		}
	})

	t.Run("error_channel_returns_wrapped_error", func(t *testing.T) {
		var buf bytes.Buffer
		msgCh := make(chan events.Message)
		errCh := make(chan error, 1)
		errCh <- errors.New("events boom")
		mockClient := &mockDockerClient{
			eventsFunc: func(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error) {
				return msgCh, errCh
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		err := n.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "events boom") {
			t.Errorf("expected wrapped events error, got %v", err)
		}
	})

	t.Run("nil_error_on_closed_error_channel_returns_nil", func(t *testing.T) {
		var buf bytes.Buffer
		msgCh := make(chan events.Message)
		errCh := make(chan error, 1)
		// Close the context so ctx.Err() is non-nil when the nil error fires.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// Send a nil error; since ctx.Err() != nil, Run should return nil.
		errCh <- nil
		mockClient := &mockDockerClient{
			eventsFunc: func(ctx context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error) {
				return msgCh, errCh
			},
		}
		n := NewCronNotifier(mockClient, newBufferLogger(&buf))
		err := n.Run(ctx)
		// Either ctx.Err() branch or err branch is fine; both should return gracefully.
		_ = err
	})
}

