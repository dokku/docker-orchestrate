package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCronParseEnvFile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expected    map[string]string
		expectError bool
	}{
		{
			name:    "valid_key_value_pairs",
			content: "FOO=bar\nBAZ=qux\n",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
		{
			name:    "comments_are_skipped",
			content: "# this is a comment\nFOO=bar\n# another comment\nBAZ=qux\n",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
		{
			name:    "empty_lines_are_skipped",
			content: "\n\nFOO=bar\n\n\nBAZ=qux\n\n",
			expected: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
		{
			name:    "double_quoted_values",
			content: "FOO=\"hello world\"\n",
			expected: map[string]string{
				"FOO": "hello world",
			},
		},
		{
			name:    "single_quoted_values",
			content: "FOO='hello world'\n",
			expected: map[string]string{
				"FOO": "hello world",
			},
		},
		{
			name:    "values_with_equals_sign",
			content: "DATABASE_URL=postgres://user:pass@host:5432/db?sslmode=require\n",
			expected: map[string]string{
				"DATABASE_URL": "postgres://user:pass@host:5432/db?sslmode=require",
			},
		},
		{
			name:    "whitespace_around_key_value",
			content: "  FOO  =  bar  \n",
			expected: map[string]string{
				"FOO": "bar",
			},
		},
		{
			name:     "empty_file",
			content:  "",
			expected: map[string]string{},
		},
		{
			name:     "only_comments_and_blanks",
			content:  "# comment\n\n# another\n",
			expected: map[string]string{},
		},
		{
			name:     "line_without_equals_is_skipped",
			content:  "NOEQUALS\nFOO=bar\n",
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:    "compose_file_colon_separated",
			content: "COMPOSE_FILE=docker-compose.yml:docker-compose.prod.yml\n",
			expected: map[string]string{
				"COMPOSE_FILE": "docker-compose.yml:docker-compose.prod.yml",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			envPath := filepath.Join(tmpDir, ".env")
			if err := os.WriteFile(envPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write temp env file: %v", err)
			}

			result, err := parseEnvFile(envPath)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d entries, got %d: %v", len(tt.expected), len(result), result)
			}
			for key, expectedVal := range tt.expected {
				gotVal, ok := result[key]
				if !ok {
					t.Errorf("missing key %q", key)
					continue
				}
				if gotVal != expectedVal {
					t.Errorf("key %q: expected %q, got %q", key, expectedVal, gotVal)
				}
			}
		})
	}
}

func TestCronParseEnvFileNotFound(t *testing.T) {
	t.Run("nonexistent_file_returns_error", func(t *testing.T) {
		_, err := parseEnvFile("/nonexistent/path/.env")
		if err == nil {
			t.Fatal("expected error for nonexistent file, got nil")
		}
	})
}

func TestCronResolveProjectConfig(t *testing.T) {
	tests := []struct {
		name              string
		setup             func(t *testing.T, dir string)
		defaultName       string
		expectFiles       int
		expectEnvFiles    int
		expectProjectName string
	}{
		{
			name: "docker_orchestrate_file",
			setup: func(t *testing.T, dir string) {
				content := "compose-files:\n  - docker-compose.yml\n  - docker-compose.prod.yml\nenv-files:\n  - .env.prod\n"
				if err := os.WriteFile(filepath.Join(dir, ".docker-orchestrate"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "myproject",
			expectFiles:       2,
			expectEnvFiles:    1,
			expectProjectName: "myproject",
		},
		{
			name: "env_file_with_compose_file",
			setup: func(t *testing.T, dir string) {
				content := "COMPOSE_FILE=docker-compose.yml:docker-compose.override.yml\nCOMPOSE_PROJECT_NAME=customname\n"
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "myproject",
			expectFiles:       2,
			expectProjectName: "customname",
		},
		{
			name: "default_docker_compose_yml_fallback",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("version: '3'\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "fallback",
			expectFiles:       1,
			expectProjectName: "fallback",
		},
		{
			name: "default_compose_yml_fallback",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("version: '3'\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "fallback2",
			expectFiles:       1,
			expectProjectName: "fallback2",
		},
		{
			name:              "empty_directory_returns_no_files",
			setup:             func(t *testing.T, dir string) {},
			defaultName:       "empty",
			expectFiles:       0,
			expectProjectName: "empty",
		},
		{
			name: "docker_orchestrate_with_absolute_paths",
			setup: func(t *testing.T, dir string) {
				content := "compose-files:\n  - /absolute/path/docker-compose.yml\n"
				if err := os.WriteFile(filepath.Join(dir, ".docker-orchestrate"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "abspath",
			expectFiles:       1,
			expectProjectName: "abspath",
		},
		{
			name: "env_file_with_only_project_name_falls_through_to_default",
			setup: func(t *testing.T, dir string) {
				content := "COMPOSE_PROJECT_NAME=customname\n"
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
				// Also create a compose.yaml so fallback finds something
				if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("version: '3'\n"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			defaultName:       "myproject",
			expectFiles:       1,
			expectProjectName: "customname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tt.setup(t, tmpDir)

			composeFiles, envFiles, projectName, err := resolveProjectConfig(tmpDir, tt.defaultName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(composeFiles) != tt.expectFiles {
				t.Errorf("expected %d compose files, got %d: %v", tt.expectFiles, len(composeFiles), composeFiles)
			}

			if tt.expectEnvFiles > 0 && len(envFiles) != tt.expectEnvFiles {
				t.Errorf("expected %d env files, got %d: %v", tt.expectEnvFiles, len(envFiles), envFiles)
			}

			if projectName != tt.expectProjectName {
				t.Errorf("expected project name %q, got %q", tt.expectProjectName, projectName)
			}
		})
	}
}

func TestCronResolveProjectConfigPriority(t *testing.T) {
	// When both .docker-orchestrate and .env exist, .docker-orchestrate should win
	t.Run("docker_orchestrate_takes_priority_over_env", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create .docker-orchestrate
		orchContent := "compose-files:\n  - orchestrate-compose.yml\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".docker-orchestrate"), []byte(orchContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Create .env with COMPOSE_FILE
		envContent := "COMPOSE_FILE=env-compose.yml\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0644); err != nil {
			t.Fatal(err)
		}

		composeFiles, _, _, err := resolveProjectConfig(tmpDir, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(composeFiles) != 1 {
			t.Fatalf("expected 1 compose file, got %d: %v", len(composeFiles), composeFiles)
		}

		expected := filepath.Join(tmpDir, "orchestrate-compose.yml")
		if composeFiles[0] != expected {
			t.Errorf("expected compose file from .docker-orchestrate (%q), got %q", expected, composeFiles[0])
		}
	})
}
