package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const cronComposeContent = `services:
  backup:
    image: busybox
    command: ["echo", "backup"]
    x-cron:
      schedule: "@every 1h"
  regular:
    image: busybox
    command: ["echo", "hello"]
`

const noCronComposeContent = `services:
  web:
    image: busybox
    command: ["echo", "web"]
`

const invalidCronComposeContent = `services:
  broken:
    image: busybox
    x-cron:
      schedule: 123
`

const invalidCronDefaultsComposeContent = `x-cron-defaults:
  timezone: Not/A_Real_Zone
services:
  backup:
    image: busybox
    x-cron:
      schedule: "@every 1h"
`

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

func TestLoadSingleProject(t *testing.T) {
	t.Run("loads_cron_services", func(t *testing.T) {
		tmpDir := t.TempDir()
		composePath := filepath.Join(tmpDir, "docker-compose.yml")
		if err := os.WriteFile(composePath, []byte(cronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}

		project, err := LoadSingleProject(
			context.Background(),
			[]string{composePath},
			nil,
			"myproject",
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if project == nil {
			t.Fatal("expected project, got nil")
		}
		if project.Name != "myproject" {
			t.Errorf("project name: %q", project.Name)
		}
		if len(project.Services) != 1 {
			t.Fatalf("expected 1 cron service, got %d", len(project.Services))
		}
		if project.Services[0].Name != "backup" {
			t.Errorf("service name: %q", project.Services[0].Name)
		}
		if project.WorkingDirectory != tmpDir {
			t.Errorf("working dir: %q", project.WorkingDirectory)
		}
	})

	t.Run("no_cron_services_returns_empty_list", func(t *testing.T) {
		tmpDir := t.TempDir()
		composePath := filepath.Join(tmpDir, "docker-compose.yml")
		if err := os.WriteFile(composePath, []byte(noCronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}

		project, err := LoadSingleProject(
			context.Background(),
			[]string{composePath},
			nil,
			"myproject",
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if project == nil {
			t.Fatal("expected project, got nil")
		}
		if len(project.Services) != 0 {
			t.Errorf("expected 0 cron services, got %d", len(project.Services))
		}
	})

	t.Run("empty_compose_files_returns_error", func(t *testing.T) {
		_, err := LoadSingleProject(context.Background(), nil, nil, "p", "")
		if err == nil {
			t.Fatal("expected error for empty compose files")
		}
	})

	t.Run("invalid_compose_file_returns_error", func(t *testing.T) {
		_, err := LoadSingleProject(
			context.Background(),
			[]string{"/nonexistent/path/docker-compose.yml"},
			nil,
			"p",
			"",
		)
		if err == nil {
			t.Fatal("expected error for nonexistent compose file")
		}
	})

	t.Run("invalid_cron_config_returns_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		composePath := filepath.Join(tmpDir, "docker-compose.yml")
		if err := os.WriteFile(composePath, []byte(invalidCronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSingleProject(
			context.Background(),
			[]string{composePath},
			nil,
			"p",
			"",
		)
		if err == nil {
			t.Fatal("expected error for invalid x-cron config")
		}
	})

	t.Run("invalid_cron_defaults_returns_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		composePath := filepath.Join(tmpDir, "docker-compose.yml")
		if err := os.WriteFile(composePath, []byte(invalidCronDefaultsComposeContent), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSingleProject(
			context.Background(),
			[]string{composePath},
			nil,
			"p",
			"",
		)
		if err == nil {
			t.Fatal("expected error for invalid x-cron-defaults")
		}
	})
}

func TestResolveProjectConfigEmptyComposeFilePath(t *testing.T) {
	// Covers the `if f == "" { continue }` branch when COMPOSE_FILE has
	// a trailing ":" or stray empty entry.
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("COMPOSE_FILE=docker-compose.yml::\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte("version: '3'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	composeFiles, _, _, err := resolveProjectConfig(tmpDir, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(composeFiles) != 1 {
		t.Errorf("expected 1 compose file after skipping empty entries, got %d", len(composeFiles))
	}
}

func TestScanProjects(t *testing.T) {
	t.Run("discovers_projects_with_cron", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Project 1: has cron service
		proj1 := filepath.Join(tmpDir, "project-alpha")
		if err := os.MkdirAll(proj1, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj1, "docker-compose.yml"), []byte(cronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Project 2: no cron services → skipped
		proj2 := filepath.Join(tmpDir, "project-beta")
		if err := os.MkdirAll(proj2, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj2, "docker-compose.yml"), []byte(noCronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Project 3: empty directory → skipped
		proj3 := filepath.Join(tmpDir, "project-gamma")
		if err := os.MkdirAll(proj3, 0755); err != nil {
			t.Fatal(err)
		}

		// Non-directory entry → skipped
		if err := os.WriteFile(filepath.Join(tmpDir, "stray.txt"), []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}

		projects, err := ScanProjects(context.Background(), tmpDir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(projects) != 1 {
			t.Fatalf("expected 1 project, got %d: %+v", len(projects), projects)
		}
		if projects[0].Name != "project-alpha" {
			t.Errorf("project name: %q", projects[0].Name)
		}
	})

	t.Run("nonexistent_dir_returns_error", func(t *testing.T) {
		_, err := ScanProjects(context.Background(), "/nonexistent/scan/dir", "")
		if err == nil {
			t.Fatal("expected error for nonexistent config dir")
		}
	})

	t.Run("scan_error_logged_but_continues", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Valid project
		valid := filepath.Join(tmpDir, "valid")
		if err := os.MkdirAll(valid, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(valid, "docker-compose.yml"), []byte(cronComposeContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Broken project with invalid .docker-orchestrate YAML
		broken := filepath.Join(tmpDir, "broken")
		if err := os.MkdirAll(broken, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken, ".docker-orchestrate"), []byte("invalid: [yaml"), 0644); err != nil {
			t.Fatal(err)
		}

		projects, err := ScanProjects(context.Background(), tmpDir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Broken is logged and skipped; valid is returned.
		if len(projects) != 1 {
			t.Errorf("expected 1 project (valid only), got %d", len(projects))
		}
	})
}
