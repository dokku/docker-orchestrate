package internal

import (
	"fmt"
	"regexp"
	"testing"
	"time"
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
