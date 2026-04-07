package internal

import (
	"encoding/json"
	"testing"
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
