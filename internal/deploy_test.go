package internal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"
	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/josegonzalez/cli-skeleton/command"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/zerolog"
)

func TestDeployServiceReplicaOverride(t *testing.T) {
	threeReplicas := 3
	fiveReplicas := 5

	tests := []struct {
		name             string
		inputReplicas    int
		deployReplicas   *int
		scaleReplicas    *int
		expectedReplicas int
		expectError      bool
	}{
		{
			name:             "override_specified",
			inputReplicas:    10,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 10,
		},
		{
			name:             "no_override_use_deploy_replicas",
			inputReplicas:    0,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 3,
		},
		{
			name:             "no_override_no_deploy_use_scale",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 5,
		},
		{
			name:             "no_replicas_defined_defaults_to_one",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    nil,
			expectedReplicas: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockDockerClient{
				containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
					// Return empty list so no updates/scaling happens for simplicity in this test
					return []container.Summary{}, nil
				},
			}

			mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{ExitCode: 0}, nil
			}

			project := &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
						Deploy: &types.DeployConfig{
							Replicas: tt.deployReplicas,
						},
						Scale: tt.scaleReplicas,
					},
				},
			}

			var buf bytes.Buffer
			logger := &command.ZerologUi{
				StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				OriginalFields:    nil,
				Ui:                nil,
				OutputIndentField: false,
			}

			input := DeployServiceInput{
				Client:                mockClient,
				Executor:              mockExecutor,
				ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
				ContainerNameTemplate: "{{.ServiceName}}",
				Logger:                logger,
				Project:               project,
				ProjectName:           "test",
				Replicas:              tt.inputReplicas,
				ServiceName:           "web",
			}

			err := DeployService(context.Background(), input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			// Check if the output contains the expected target-replicas
			expectedMsg := fmt.Sprintf("target-replicas=%d", tt.expectedReplicas)

			output := buf.String()
			if !strings.Contains(output, expectedMsg) {
				t.Errorf("expected replica count %d in output, but not found. Output: %s", tt.expectedReplicas, output)
			}
		})
	}
}

func TestDeployServiceStopGracePeriod(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("custom stop_grace_period is passed through", func(t *testing.T) {
		var capturedTimeout int
		callCount := 0
		gracePeriod := types.Duration(30 * time.Second)

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				// First call: non-running containers check, second: running containers
				if callCount < 2 {
					callCount++
					return []container.Summary{
						{ID: "old1_container_id", Names: []string{"/old1"}, State: "running", Created: 100},
					}, nil
				}
				callCount++
				return []container.Summary{}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				capturedTimeout = timeoutSeconds
				return nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:            "web",
					StopGracePeriod: &gracePeriod,
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		// We expect the deploy to proceed and pass 30 as the stop timeout
		// The deploy will attempt to scale down from 1 to 1 (no-op), then rolling update
		_ = DeployService(context.Background(), input)

		if capturedTimeout != 0 && capturedTimeout != 30 {
			t.Errorf("expected timeout 30, got %d", capturedTimeout)
		}
	})

	t.Run("nil stop_grace_period defaults to 10", func(t *testing.T) {
		var capturedTimeout int
		terminateCalled := false
		callCount := 0

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				if callCount == 0 {
					callCount++
					// Return containers for non-running check
					return []container.Summary{
						{ID: "old1_container_id", Names: []string{"/old1"}, State: "running", Created: 100},
						{ID: "old2_container_id", Names: []string{"/old2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 1 {
					callCount++
					// Return running containers for rename
					return []container.Summary{
						{ID: "old1_container_id", Names: []string{"/old1"}, State: "running", Created: 100},
						{ID: "old2_container_id", Names: []string{"/old2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 2 {
					callCount++
					// Return running containers for scale down check
					return []container.Summary{
						{ID: "old1_container_id", Names: []string{"/old1"}, State: "running", Created: 100},
						{ID: "old2_container_id", Names: []string{"/old2"}, State: "running", Created: 200},
					}, nil
				}
				callCount++
				return []container.Summary{}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminateCalled = true
				capturedTimeout = timeoutSeconds
				return nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		oneReplica := 1
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:            "web",
					StopGracePeriod: nil,
					Deploy: &types.DeployConfig{
						Replicas: &oneReplica,
					},
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		_ = DeployService(context.Background(), input)

		if terminateCalled && capturedTimeout != 10 {
			t.Errorf("expected default timeout 10, got %d", capturedTimeout)
		}
	})
}

func TestDeployServiceCleansUpNonRunningContainers(t *testing.T) {
	tests := []struct {
		name               string
		allContainers      []container.Summary
		expectedRemovals   []string
		removeError        error
		expectDeployError  bool
	}{
		{
			name: "removes_exited_containers",
			allContainers: []container.Summary{
				{ID: "aaaa11111111aaaa11111111", Names: []string{"/web-1"}, State: "running"},
				{ID: "bbbb22222222bbbb22222222", Names: []string{"/web-2"}, State: "exited"},
				{ID: "cccc33333333cccc33333333", Names: []string{"/web-3"}, State: "exited"},
			},
			expectedRemovals: []string{"bbbb22222222bbbb22222222", "cccc33333333cccc33333333"},
		},
		{
			name: "removes_dead_containers",
			allContainers: []container.Summary{
				{ID: "aaaa11111111aaaa11111111", Names: []string{"/web-1"}, State: "running"},
				{ID: "bbbb22222222bbbb22222222", Names: []string{"/web-2"}, State: "dead"},
			},
			expectedRemovals: []string{"bbbb22222222bbbb22222222"},
		},
		{
			name: "removes_created_containers",
			allContainers: []container.Summary{
				{ID: "aaaa11111111aaaa11111111", Names: []string{"/web-1"}, State: "created"},
			},
			expectedRemovals: []string{"aaaa11111111aaaa11111111"},
		},
		{
			name: "no_non_running_containers",
			allContainers: []container.Summary{
				{ID: "aaaa11111111aaaa11111111", Names: []string{"/web-1"}, State: "running"},
			},
			expectedRemovals: []string{},
		},
		{
			name:             "no_containers",
			allContainers:    []container.Summary{},
			expectedRemovals: []string{},
		},
		{
			name: "remove_failure_continues",
			allContainers: []container.Summary{
				{ID: "aaaa11111111aaaa11111111", Names: []string{"/web-1"}, State: "exited"},
			},
			expectedRemovals: []string{"aaaa11111111aaaa11111111"},
			removeError:      fmt.Errorf("container in use"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var removedIDs []string

			firstCall := true
			mockClient := &mockDockerClient{
				containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
					statusFilter := options.Filters.Get("status")
					if len(statusFilter) == 0 && firstCall {
						// First unfiltered call: return all containers for cleanup
						firstCall = false
						return tt.allContainers, nil
					}
					// All subsequent calls (filtered to running): return empty
					// so no rolling update or scaling happens
					return []container.Summary{}, nil
				},
				containerRemove: func(ctx context.Context, id string, options container.RemoveOptions) error {
					removedIDs = append(removedIDs, id)
					if !options.RemoveVolumes {
						t.Errorf("expected RemoveVolumes=true for container %s, got false", id)
					}
					return tt.removeError
				},
			}

			mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{ExitCode: 0}, nil
			}

			project := &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
					},
				},
			}

			var buf bytes.Buffer
			logger := &command.ZerologUi{
				StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				OriginalFields:    nil,
				Ui:                nil,
				OutputIndentField: false,
			}

			input := DeployServiceInput{
				Client:                mockClient,
				Executor:              mockExecutor,
				ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
				ContainerNameTemplate: "{{.ServiceName}}",
				Logger:                logger,
				Project:               project,
				ProjectName:           "test",
				ServiceName:           "web",
			}

			err := DeployService(context.Background(), input)

			if tt.expectDeployError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(removedIDs) != len(tt.expectedRemovals) {
				t.Errorf("expected %d removals, got %d. Removed: %v", len(tt.expectedRemovals), len(removedIDs), removedIDs)
				return
			}

			for i, expectedID := range tt.expectedRemovals {
				if removedIDs[i] != expectedID {
					t.Errorf("expected removal[%d] = %s, got %s", i, expectedID, removedIDs[i])
				}
			}
		})
	}
}

func TestIsDatabaseService(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	tests := []struct {
		name           string
		image          string
		expectedResult bool
		expectError    bool
	}{
		// Database images - various formats
		{
			name:           "postgres_library",
			image:          "postgres:14",
			expectedResult: true,
		},
		{
			name:           "postgres_full_library",
			image:          "library/postgres:14",
			expectedResult: true,
		},
		{
			name:           "postgres_with_registry",
			image:          "docker.io/library/postgres:14",
			expectedResult: true,
		},
		{
			name:           "postgres_custom_registry",
			image:          "myregistry.com/library/postgres:latest",
			expectedResult: true,
		},
		{
			name:           "mysql_library",
			image:          "mysql:8.0",
			expectedResult: true,
		},
		{
			name:           "redis_library",
			image:          "redis:7-alpine",
			expectedResult: true,
		},
		{
			name:           "mariadb",
			image:          "mariadb:10.11",
			expectedResult: true,
		},
		{
			name:           "mongo_library",
			image:          "mongo:7",
			expectedResult: true,
		},
		{
			name:           "clickhouse",
			image:          "clickhouse/clickhouse-server:latest",
			expectedResult: true,
		},
		{
			name:           "elasticsearch_library",
			image:          "elasticsearch:8.11.0",
			expectedResult: true,
		},
		{
			name:           "rabbitmq_library",
			image:          "rabbitmq:3-management",
			expectedResult: true,
		},
		{
			name:           "couchdb_library",
			image:          "couchdb:3.3",
			expectedResult: true,
		},
		{
			name:           "memcached_library",
			image:          "memcached:1.6",
			expectedResult: true,
		},
		{
			name:           "nats_library",
			image:          "nats:2.10",
			expectedResult: true,
		},
		{
			name:           "rethinkdb_library",
			image:          "rethinkdb:2.4",
			expectedResult: true,
		},
		{
			name:           "solr_library",
			image:          "solr:9.4",
			expectedResult: true,
		},
		{
			name:           "meilisearch",
			image:          "getmeili/meilisearch:v1.5",
			expectedResult: true,
		},
		{
			name:           "typesense",
			image:          "typesense/typesense:0.25",
			expectedResult: true,
		},
		{
			name:           "grafana_graphite",
			image:          "dokku/docker-grafana-graphite:latest",
			expectedResult: true,
		},
		{
			name:           "pushpin",
			image:          "fanout/pushpin:latest",
			expectedResult: true,
		},
		{
			name:           "omnisci",
			image:          "omnisci/core-os-cpu:latest",
			expectedResult: true,
		},
		// Non-database images
		{
			name:           "nginx",
			image:          "nginx:alpine",
			expectedResult: false,
		},
		{
			name:           "node",
			image:          "node:20",
			expectedResult: false,
		},
		{
			name:           "python",
			image:          "python:3.11",
			expectedResult: false,
		},
		{
			name:           "custom_app",
			image:          "myapp/web:latest",
			expectedResult: false,
		},
		// Invalid images
		{
			name:           "invalid_image",
			image:          "invalid:image:tag:too:many:colons",
			expectedResult: false,
			expectError:    true,
		},
		{
			name:           "empty_image",
			image:          "",
			expectedResult: false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()

			result := isDatabaseService(tt.image, logger)

			if result != tt.expectedResult {
				t.Errorf("isDatabaseService() = %v, want %v for image %s", result, tt.expectedResult, tt.image)
			}

			if tt.expectError {
				output := buf.String()
				if !strings.Contains(output, "error parsing image") {
					t.Errorf("expected error message in output, got: %s", output)
				}
			}

			if tt.expectedResult && !tt.expectError {
				output := buf.String()
				if !strings.Contains(output, "Skipping detected database service") {
					t.Errorf("expected skip message in output, got: %s", output)
				}
			}
		})
	}
}

func TestShouldSkipService(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	tests := []struct {
		name                string
		image               string
		shouldSkipDatabases bool
		expectedResult      bool
		labels              map[string]string
		provider            *types.ServiceProviderConfig
		models              map[string]*types.ServiceModelConfig
	}{
		{
			name:                "skip_databases_true_database_service",
			image:               "postgres:14",
			shouldSkipDatabases: true,
			expectedResult:      true,
		},
		{
			name:                "skip_databases_true_non_database_service",
			image:               "nginx:alpine",
			shouldSkipDatabases: true,
			expectedResult:      false,
		},
		{
			name:                "skip_databases_false_database_service",
			image:               "postgres:14",
			shouldSkipDatabases: false,
			expectedResult:      false,
		},
		{
			name:                "skip_databases_false_non_database_service",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
		},
		{
			name:                "skip_databases_true_mysql",
			image:               "mysql:8.0",
			shouldSkipDatabases: true,
			expectedResult:      true,
		},
		{
			name:                "skip_databases_true_redis",
			image:               "redis:7",
			shouldSkipDatabases: true,
			expectedResult:      true,
		},
		{
			name:                "skip_databases_true_mariadb",
			image:               "mariadb:10.11",
			shouldSkipDatabases: true,
			expectedResult:      true,
		},
		{
			name:                "skip_label_true",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "true"},
		},
		{
			name:                "skip_label_false",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "false"},
		},
		{
			name:                "skip_label_other_value",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "yes"},
		},
		{
			name:                "skip_label_true_takes_precedence_over_database",
			image:               "postgres:14",
			shouldSkipDatabases: true,
			expectedResult:      true,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "true"},
		},
		{
			name:                "skip_label_false_still_checks_database",
			image:               "postgres:14",
			shouldSkipDatabases: true,
			expectedResult:      true,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "false"},
		},
		{
			name:                "no_skip_label_normal_behavior",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
			labels:              nil,
		},
		{
			name:                "provider_service_skipped",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              nil,
			provider:            &types.ServiceProviderConfig{Type: "awesomecloud"},
		},
		{
			name:                "provider_service_takes_precedence_over_label",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "false"},
			provider:            &types.ServiceProviderConfig{Type: "awesomecloud"},
		},
		{
			name:                "provider_service_takes_precedence_over_database",
			image:               "postgres:14",
			shouldSkipDatabases: true,
			expectedResult:      true,
			labels:              nil,
			provider:            &types.ServiceProviderConfig{Type: "awesomecloud"},
		},
		{
			name:                "model_service_not_skipped",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
			labels:              nil,
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_with_provider_still_skipped_by_provider",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              nil,
			provider:            &types.ServiceProviderConfig{Type: "awesomecloud"},
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_with_false_skip_label_not_skipped",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      false,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "false"},
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_with_database_still_skipped_by_database",
			image:               "postgres:14",
			shouldSkipDatabases: true,
			expectedResult:      true,
			labels:              nil,
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			service := &types.ServiceConfig{
				Name:  "test-service",
				Image: tt.image,
			}

			if tt.labels != nil {
				service.Labels = tt.labels
			}

			if tt.provider != nil {
				service.Provider = tt.provider
			}

			if tt.models != nil {
				service.Models = tt.models
			}

			result := shouldSkipService(ShouldSkipServiceInput{
				Service:             service,
				ShouldSkipDatabases: tt.shouldSkipDatabases,
				Logger:              logger,
			})

			if result != tt.expectedResult {
				t.Errorf("shouldSkipService() = %v, want %v for image %s with shouldSkipDatabases=%v", result, tt.expectedResult, tt.image, tt.shouldSkipDatabases)
			}
		})
	}
}

func TestDeployServiceValidation(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}
	mockClient := &mockDockerClient{}
	minimalProject := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{Name: "web"},
		},
	}

	tests := []struct {
		name        string
		input       DeployServiceInput
		expectedErr string
	}{
		{
			name: "empty_compose_files",
			input: DeployServiceInput{
				Client:      mockClient,
				Logger:      logger,
				Project:     minimalProject,
				ProjectName: "test",
				ServiceName: "web",
			},
			expectedErr: "compose file is required",
		},
		{
			name: "empty_project_name",
			input: DeployServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yml"},
				Logger:       logger,
				Project:      minimalProject,
				ServiceName:  "web",
			},
			expectedErr: "project name is required",
		},
		{
			name: "nil_project",
			input: DeployServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yml"},
				Logger:       logger,
				ProjectName:  "test",
				ServiceName:  "web",
			},
			expectedErr: "project is required",
		},
		{
			name: "empty_service_name",
			input: DeployServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yml"},
				Logger:       logger,
				Project:      minimalProject,
				ProjectName:  "test",
			},
			expectedErr: "service name is required",
		},
		{
			name: "service_not_in_project",
			input: DeployServiceInput{
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yml"},
				Logger:       logger,
				Project:      minimalProject,
				ProjectName:  "test",
				ServiceName:  "missing-service",
			},
			expectedErr: "not found in compose file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := DeployService(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("expected error containing %q, got: %v", tt.expectedErr, err)
			}
		})
	}
}

func TestDeployServiceSkipsCronService(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}
	mockClient := &mockDockerClient{}
	project := &types.Project{
		Services: types.Services{
			"backup": types.ServiceConfig{
				Name: "backup",
				Extensions: map[string]interface{}{
					"x-cron": map[string]interface{}{"schedule": "@every 1h"},
				},
			},
		},
	}
	err := DeployService(context.Background(), DeployServiceInput{
		Client:       mockClient,
		ComposeFiles: []string{"/tmp/docker-compose.yml"},
		Logger:       logger,
		Project:      project,
		ProjectName:  "test",
		ServiceName:  "backup",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Skipping cron-scheduled service") {
		t.Errorf("expected skip log, got: %s", buf.String())
	}
}

func TestShouldSkipServiceCronAndSilenceLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("cron_service_is_skipped", func(t *testing.T) {
		buf.Reset()
		service := &types.ServiceConfig{
			Name:  "backup",
			Image: "nginx:alpine",
			Extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{"schedule": "@every 1h"},
			},
		}
		if !shouldSkipService(ShouldSkipServiceInput{
			Service: service,
			Logger:  logger,
		}) {
			t.Error("expected cron service to be skipped")
		}
		if !strings.Contains(buf.String(), "Skipping cron-scheduled service") {
			t.Errorf("expected cron skip log, got: %s", buf.String())
		}
	})

	t.Run("silence_logging_cron", func(t *testing.T) {
		buf.Reset()
		service := &types.ServiceConfig{
			Name: "backup",
			Extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{"schedule": "@every 1h"},
			},
		}
		if !shouldSkipService(ShouldSkipServiceInput{
			Service:        service,
			Logger:         logger,
			SilenceLogging: true,
		}) {
			t.Error("expected cron service to be skipped")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output with SilenceLogging, got: %s", buf.String())
		}
	})

	t.Run("silence_logging_provider", func(t *testing.T) {
		buf.Reset()
		service := &types.ServiceConfig{
			Name:     "x",
			Provider: &types.ServiceProviderConfig{Type: "demo"},
		}
		if !shouldSkipService(ShouldSkipServiceInput{
			Service:        service,
			Logger:         logger,
			SilenceLogging: true,
		}) {
			t.Error("expected provider service to be skipped")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output with SilenceLogging, got: %s", buf.String())
		}
	})

	t.Run("silence_logging_skip_label", func(t *testing.T) {
		buf.Reset()
		service := &types.ServiceConfig{
			Name:   "x",
			Labels: map[string]string{"com.dokku.orchestrate/skip": "true"},
		}
		if !shouldSkipService(ShouldSkipServiceInput{
			Service:        service,
			Logger:         logger,
			SilenceLogging: true,
		}) {
			t.Error("expected labeled service to be skipped")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output with SilenceLogging, got: %s", buf.String())
		}
	})
}

func TestOrderServices(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	tests := []struct {
		name          string
		project       *types.Project
		expectedOrder []string
		expectError   bool
		description   string
	}{
		{
			name: "web_service_no_dependencies",
			project: &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
					},
					"api": types.ServiceConfig{
						Name: "api",
					},
					"worker": types.ServiceConfig{
						Name: "worker",
					},
				},
			},
			expectedOrder: []string{"web", "api", "worker"},
			description:   "Web service should be first when it has no dependencies",
		},
		{
			name: "web_service_with_dependencies",
			project: &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
						DependsOn: map[string]types.ServiceDependency{
							"api": {},
						},
					},
					"api": types.ServiceConfig{
						Name: "api",
					},
					"worker": types.ServiceConfig{
						Name: "worker",
					},
				},
			},
			expectedOrder: []string{"api", "worker", "web"},
			description:   "Web service should follow dependency order when it has dependencies",
		},
		{
			name: "no_web_service",
			project: &types.Project{
				Services: types.Services{
					"api": types.ServiceConfig{
						Name: "api",
					},
					"worker": types.ServiceConfig{
						Name: "worker",
					},
					"db": types.ServiceConfig{
						Name: "db",
					},
				},
			},
			expectedOrder: []string{"api", "db", "worker"},
			description:   "Services should be ordered by dependency when no web service exists",
		},
		{
			name: "complex_dependencies",
			project: &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
					},
					"api": types.ServiceConfig{
						Name: "api",
						DependsOn: map[string]types.ServiceDependency{
							"db": {},
						},
					},
					"db": types.ServiceConfig{
						Name: "db",
					},
					"worker": types.ServiceConfig{
						Name: "worker",
						DependsOn: map[string]types.ServiceDependency{
							"db": {},
						},
					},
				},
			},
			expectedOrder: []string{"web", "db", "api", "worker"},
			description:   "Web first, then dependencies ordered correctly",
		},
		{
			name: "web_with_multiple_dependencies",
			project: &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
						DependsOn: map[string]types.ServiceDependency{
							"api":    {},
							"worker": {},
						},
					},
					"api": types.ServiceConfig{
						Name: "api",
						DependsOn: map[string]types.ServiceDependency{
							"db": {},
						},
					},
					"db": types.ServiceConfig{
						Name: "db",
					},
					"worker": types.ServiceConfig{
						Name: "worker",
					},
				},
			},
			expectedOrder: []string{"db", "api", "worker", "web"},
			description:   "Web with multiple dependencies should follow dependency order",
		},
		{
			name: "single_service",
			project: &types.Project{
				Services: types.Services{
					"web": types.ServiceConfig{
						Name: "web",
					},
				},
			},
			expectedOrder: []string{"web"},
			description:   "Single service should return that service",
		},
		{
			name: "single_service_no_web",
			project: &types.Project{
				Services: types.Services{
					"api": types.ServiceConfig{
						Name: "api",
					},
				},
			},
			expectedOrder: []string{"api"},
			description:   "Single non-web service should return that service",
		},
		{
			name: "empty_project",
			project: &types.Project{
				Services: types.Services{},
			},
			expectedOrder: []string{},
			description:   "Empty project should return empty slice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := DeployProjectInput{
				Project: tt.project,
				Logger:  logger,
			}

			result, err := OrderServices(ctx, input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expectedOrder) {
				t.Errorf("expected %d services, got %d. Expected: %v, Got: %v", len(tt.expectedOrder), len(result), tt.expectedOrder, result)
				return
			}

			// Check that web is first if it has no dependencies
			if len(tt.project.Services) > 0 {
				webService, hasWeb := tt.project.Services["web"]
				if hasWeb && len(webService.DependsOn) == 0 {
					if result[0] != "web" {
						t.Errorf("expected web to be first, got %s. Order: %v", result[0], result)
					}
				}
			}

			// Check that the order respects dependencies
			// For each service, ensure its dependencies come before it
			serviceIndex := make(map[string]int)
			for i, name := range result {
				serviceIndex[name] = i
			}

			for _, serviceName := range result {
				service, ok := tt.project.Services[serviceName]
				if !ok {
					continue
				}

				for depName := range service.DependsOn {
					depIndex, depExists := serviceIndex[depName]
					serviceIdx := serviceIndex[serviceName]
					if depExists && depIndex >= serviceIdx {
						t.Errorf("dependency violation: %s depends on %s but %s comes after %s. Order: %v", serviceName, depName, depName, serviceName, result)
					}
				}
			}

			// Verify all expected services are present
			expectedSet := make(map[string]bool)
			for _, name := range tt.expectedOrder {
				expectedSet[name] = true
			}

			resultSet := make(map[string]bool)
			for _, name := range result {
				resultSet[name] = true
			}

			if len(expectedSet) != len(resultSet) {
				t.Errorf("service count mismatch. Expected: %v, Got: %v", tt.expectedOrder, result)
			}

			for name := range expectedSet {
				if !resultSet[name] {
					t.Errorf("missing service %s in result. Expected: %v, Got: %v", name, tt.expectedOrder, result)
				}
			}
		})
	}
}

func TestServiceReplicas(t *testing.T) {
	threeReplicas := 3
	fiveReplicas := 5
	tenReplicas := 10

	tests := []struct {
		name             string
		inputReplicas    int
		deployReplicas   *int
		scaleReplicas    *int
		expectedReplicas int
	}{
		{
			name:             "override_specified",
			inputReplicas:    10,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 10,
		},
		{
			name:             "no_override_use_deploy_replicas",
			inputReplicas:    0,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 3,
		},
		{
			name:             "no_override_no_deploy_use_scale",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 5,
		},
		{
			name:             "no_replicas_defined_defaults_to_one",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    nil,
			expectedReplicas: 1,
		},
		{
			name:             "override_zero_ignored",
			inputReplicas:    0,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 3,
		},
		{
			name:             "override_negative_ignored",
			inputReplicas:    -1,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 3,
		},
		{
			name:             "deploy_replicas_zero_is_valid",
			inputReplicas:    0,
			deployReplicas:   func() *int { z := 0; return &z }(),
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 0,
		},
		{
			name:             "deploy_replicas_zero_no_scale_is_zero",
			inputReplicas:    0,
			deployReplicas:   func() *int { z := 0; return &z }(),
			scaleReplicas:    nil,
			expectedReplicas: 0,
		},
		{
			name:             "scale_zero_is_valid",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    func() *int { z := 0; return &z }(),
			expectedReplicas: 0,
		},
		{
			name:             "override_takes_precedence_over_all",
			inputReplicas:    tenReplicas,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 10,
		},
		{
			name:             "deploy_replicas_takes_precedence_over_scale",
			inputReplicas:    0,
			deployReplicas:   &threeReplicas,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 3,
		},
		{
			name:             "service_with_no_deploy_config",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    &fiveReplicas,
			expectedReplicas: 5,
		},
		{
			name:             "service_with_no_deploy_config_no_scale",
			inputReplicas:    0,
			deployReplicas:   nil,
			scaleReplicas:    nil,
			expectedReplicas: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &types.ServiceConfig{
				Name: "test-service",
			}

			if tt.deployReplicas != nil {
				service.Deploy = &types.DeployConfig{
					Replicas: tt.deployReplicas,
				}
			}

			service.Scale = tt.scaleReplicas

			input := DeployServiceInput{
				Replicas: tt.inputReplicas,
			}

			result := ServiceReplicas(input, service)

			if result != tt.expectedReplicas {
				t.Errorf("ServiceReplicas() = %d, want %d for inputReplicas=%d, deployReplicas=%v, scaleReplicas=%v",
					result, tt.expectedReplicas, tt.inputReplicas, tt.deployReplicas, tt.scaleReplicas)
			}
		})
	}
}

func TestParseDetachedFlag(t *testing.T) {
	tests := []struct {
		name        string
		extensions  map[string]interface{}
		key         string
		expected    bool
		expectError bool
		errorMsg    string
	}{
		{
			name:        "boolean true",
			extensions:  map[string]interface{}{"x-test-detached": true},
			key:         "x-test-detached",
			expected:    true,
			expectError: false,
		},
		{
			name:        "boolean false",
			extensions:  map[string]interface{}{"x-test-detached": false},
			key:         "x-test-detached",
			expected:    false,
			expectError: false,
		},
		{
			name:        "missing key",
			extensions:  map[string]interface{}{},
			key:         "x-test-detached",
			expected:    false,
			expectError: false,
		},
		{
			name:        "string value",
			extensions:  map[string]interface{}{"x-test-detached": "true"},
			key:         "x-test-detached",
			expected:    false,
			expectError: true,
			errorMsg:    "must be a boolean",
		},
		{
			name:        "integer value",
			extensions:  map[string]interface{}{"x-test-detached": 1},
			key:         "x-test-detached",
			expected:    false,
			expectError: true,
			errorMsg:    "must be a boolean",
		},
		{
			name:        "nil value",
			extensions:  map[string]interface{}{"x-test-detached": nil},
			key:         "x-test-detached",
			expected:    false,
			expectError: true,
			errorMsg:    "must be a boolean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDetachedFlag(tt.extensions, tt.key)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestDeployServicePreStopHooks(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("pre_stop hooks are threaded through to scale-down", func(t *testing.T) {
		var hookCmds []string
		callCount := 0

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				if callCount == 0 {
					callCount++
					// Non-running containers check
					return []container.Summary{
						{ID: "container1_id_long", Names: []string{"/container1"}, State: "running", Created: 100},
						{ID: "container2_id_long", Names: []string{"/container2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 1 {
					callCount++
					// Config hash check (running containers)
					return []container.Summary{
						{ID: "container1_id_long", Names: []string{"/container1"}, State: "running", Created: 100},
						{ID: "container2_id_long", Names: []string{"/container2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 2 {
					callCount++
					// Running containers for rename
					return []container.Summary{
						{ID: "container1_id_long", Names: []string{"/container1"}, State: "running", Created: 100},
						{ID: "container2_id_long", Names: []string{"/container2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 3 {
					callCount++
					// Running containers for scale down check (2 running, want 1)
					return []container.Summary{
						{ID: "container1_id_long", Names: []string{"/container1"}, State: "running", Created: 100},
						{ID: "container2_id_long", Names: []string{"/container2"}, State: "running", Created: 200},
					}, nil
				}
				callCount++
				return []container.Summary{}, nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				return nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				// Track hook commands (not script commands)
				isScript := false
				for _, c := range config.Cmd {
					if strings.Contains(c, "pre-stop.sh") {
						isScript = true
						break
					}
				}
				if !isScript {
					hookCmds = append(hookCmds, strings.Join(config.Cmd, " "))
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
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					PreStop: []types.ServiceHook{
						{Command: types.ShellCommand{"nginx", "-s", "quit"}},
					},
					Deploy: &types.DeployConfig{
						Replicas: intPtr(1),
						UpdateConfig: &types.UpdateConfig{
							Parallelism: &parallelism,
							Order:       "stop-first",
						},
					},
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		_ = DeployService(context.Background(), input)

		// Verify the hook was executed during the scale-down path
		found := false
		for _, cmd := range hookCmds {
			if cmd == "nginx -s quit" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected hook command 'nginx -s quit' to be executed, got %v", hookCmds)
		}
	})
}

func TestDeployServicePostStartHooks(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("post_start hooks are threaded through to scale-up", func(t *testing.T) {
		var hookCmds []string
		callCount := 0

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				// Calls 1-6: all containers check, config hash check, rename, currentContainers, containersToUpdate, updatedContainers
				// All return empty to force scale-up path (0 existing → 1 desired)
				if callCount <= 6 {
					return []container.Summary{}, nil
				}
				// Call 7+: after docker compose create and final count - return the new container
				return []container.Summary{
					{ID: "new1_container_id_long", Names: []string{"/new1"}, State: "running", Created: 100},
				}, nil
			},
			containerStart: func(ctx context.Context, id string, options container.StartOptions) error {
				return nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						State: &container.State{Running: true},
					},
				}, nil
			},
			containerExecCreate: func(ctx context.Context, containerID string, config container.ExecOptions) (container.ExecCreateResponse, error) {
				hookCmds = append(hookCmds, strings.Join(config.Cmd, " "))
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

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					PostStart: []types.ServiceHook{
						{Command: types.ShellCommand{"sh", "-c", "echo started"}},
					},
					Deploy: &types.DeployConfig{
						Replicas: intPtr(1),
						UpdateConfig: &types.UpdateConfig{
							Parallelism: &parallelism,
							Order:       "start-first",
						},
					},
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		_ = DeployService(context.Background(), input)

		// Verify the post_start hook was executed during the scale-up path
		found := false
		for _, cmd := range hookCmds {
			if cmd == "sh -c echo started" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected hook command 'sh -c echo started' to be executed, got %v", hookCmds)
		}
	})
}

func TestDeployServiceEmptyComposeFiles(t *testing.T) {
	err := DeployService(context.Background(), DeployServiceInput{
		ComposeFiles: []string{},
		ProjectName:  "test",
		ServiceName:  "web",
	})
	if err == nil {
		t.Fatal("expected error for empty ComposeFiles, got nil")
	}
	if !strings.Contains(err.Error(), "compose file is required") {
		t.Errorf("expected 'compose file is required' error, got: %v", err)
	}
}

func TestDeployServiceMultipleComposeFiles(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml", "/tmp/docker-compose.override.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that executed docker compose commands contain both -f flags
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		foundFirst := false
		foundSecond := false
		for i, arg := range args {
			if arg == "-f" && i+1 < len(args) {
				if args[i+1] == "/tmp/docker-compose.yaml" {
					foundFirst = true
				}
				if args[i+1] == "/tmp/docker-compose.override.yaml" {
					foundSecond = true
				}
			}
		}
		if !foundFirst || !foundSecond {
			t.Errorf("expected both -f flags in compose command, got args: %v", args)
		}
	}
}

func TestDeployServiceEnvFiles(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		EnvFiles:              []string{"/tmp/app.env", "/tmp/secrets.env"},
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that executed docker compose commands contain --env-file flags
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		foundFirst := false
		foundSecond := false
		for i, arg := range args {
			if arg == "--env-file" && i+1 < len(args) {
				if args[i+1] == "/tmp/app.env" {
					foundFirst = true
				}
				if args[i+1] == "/tmp/secrets.env" {
					foundSecond = true
				}
			}
		}
		if !foundFirst || !foundSecond {
			t.Errorf("expected both --env-file flags in compose command, got args: %v", args)
		}
	}
}

func TestDeployServiceEnvVarsPassedToHooks(t *testing.T) {
	var capturedEnvs []map[string]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{
					ID:      "container123456",
					Names:   []string{"/test-web-1"},
					State:   "running",
					Created: 100,
					Labels: map[string]string{
						"com.docker.compose.project": "test",
						"com.docker.compose.service": "web",
					},
				},
			}, nil
		},
		containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID: id,
				},
				NetworkSettings: &container.NetworkSettings{},
			}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		if input.Env != nil {
			capturedEnvs = append(capturedEnvs, input.Env)
		}
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
				Deploy: &types.DeployConfig{
					UpdateConfig: &types.UpdateConfig{
						Order: "stop-first",
						Extensions: map[string]interface{}{
							"x-pre-stop-host-command":  "echo pre-stop",
							"x-post-stop-host-command": "echo post-stop",
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	envVars := map[string]string{
		"DB_HOST": "localhost",
		"DB_PORT": "5432",
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		EnvVars:               envVars,
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that env vars were passed to at least one host script execution
	if len(capturedEnvs) == 0 {
		t.Fatal("expected env vars to be passed to host script executions")
	}
	for _, env := range capturedEnvs {
		if env["DB_HOST"] != "localhost" {
			t.Errorf("expected DB_HOST=localhost, got %q", env["DB_HOST"])
		}
		if env["DB_PORT"] != "5432" {
			t.Errorf("expected DB_PORT=5432, got %q", env["DB_PORT"])
		}
	}
}

func TestResolvePullPolicy(t *testing.T) {
	buildConfig := &types.BuildConfig{Context: "."}

	tests := []struct {
		name           string
		cliPullPolicy  string
		cliBuild       bool
		servicePull    string
		serviceBuild   *types.BuildConfig
		expectedPolicy string
		expectedBuild  bool
		expectError    bool
		errorContains  string
	}{
		// CLI override cases
		{
			name:           "cli_always_overrides_compose",
			cliPullPolicy:  "always",
			servicePull:    "never",
			expectedPolicy: "always",
		},
		{
			name:           "cli_missing_overrides_compose",
			cliPullPolicy:  "missing",
			servicePull:    "always",
			expectedPolicy: "missing",
		},
		{
			name:           "cli_never_overrides_compose",
			cliPullPolicy:  "never",
			servicePull:    "",
			expectedPolicy: "never",
		},

		// Compose spec cases (no CLI override)
		{
			name:           "compose_always",
			servicePull:    "always",
			expectedPolicy: "always",
		},
		{
			name:           "compose_never",
			servicePull:    "never",
			expectedPolicy: "never",
		},
		{
			name:           "compose_missing",
			servicePull:    "missing",
			expectedPolicy: "missing",
		},
		{
			name:           "compose_if_not_present_maps_to_missing",
			servicePull:    "if_not_present",
			expectedPolicy: "missing",
		},

		// Default case
		{
			name:           "neither_set_defaults_to_missing",
			expectedPolicy: "missing",
		},

		// Build cases
		{
			name:           "compose_build_with_build_section",
			servicePull:    "build",
			serviceBuild:   buildConfig,
			expectedPolicy: "never",
			expectedBuild:  true,
		},
		{
			name:          "compose_build_without_build_section",
			servicePull:   "build",
			expectError:   true,
			errorContains: "requires a build section",
		},
		{
			name:           "cli_build_with_build_section",
			cliBuild:       true,
			serviceBuild:   buildConfig,
			expectedPolicy: "missing",
			expectedBuild:  true,
		},
		{
			name:           "cli_build_without_build_section",
			cliBuild:       true,
			expectedPolicy: "missing",
			expectedBuild:  false,
		},
		{
			name:           "cli_build_with_cli_pull_always",
			cliPullPolicy:  "always",
			cliBuild:       true,
			serviceBuild:   buildConfig,
			expectedPolicy: "always",
			expectedBuild:  true,
		},
		{
			name:           "cli_build_with_compose_pull_never",
			cliBuild:       true,
			servicePull:    "never",
			serviceBuild:   buildConfig,
			expectedPolicy: "never",
			expectedBuild:  true,
		},
		{
			name:           "compose_build_with_cli_pull_override",
			cliPullPolicy:  "always",
			servicePull:    "build",
			serviceBuild:   buildConfig,
			expectedPolicy: "always",
			expectedBuild:  true,
		},

		// Error cases
		{
			name:          "compose_refresh_rejected",
			servicePull:   "refresh",
			expectError:   true,
			errorContains: "not supported",
		},
		{
			name:          "compose_unknown_rejected",
			servicePull:   "daily",
			expectError:   true,
			errorContains: "not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &types.ServiceConfig{
				Name:       "web",
				PullPolicy: tt.servicePull,
				Build:      tt.serviceBuild,
			}

			result, build, err := ResolvePullPolicy(tt.cliPullPolicy, tt.cliBuild, service)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error to contain %q, got: %v", tt.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expectedPolicy {
				t.Errorf("expected policy %q, got %q", tt.expectedPolicy, result)
			}
			if build != tt.expectedBuild {
				t.Errorf("expected build %v, got %v", tt.expectedBuild, build)
			}
		})
	}
}

func TestDeployServicePullPolicy(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		PullPolicy:            "always",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the pre-pull command was executed (docker compose pull)
	foundPrePull := false
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "pull" && i+1 < len(args) && args[i+1] == "web" {
				foundPrePull = true
				break
			}
		}
	}
	if !foundPrePull {
		t.Error("expected docker compose pull command for always policy, got none")
	}

	// Verify that compose up/create commands contain --pull always
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundPull := false
		for i, arg := range args {
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "always" {
				foundPull = true
				break
			}
		}
		if !foundPull {
			t.Errorf("expected --pull always in compose command, got args: %v", args)
		}
	}
}

func TestDeployServicePullPolicyFromComposeSpec(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:       "web",
				Image:      "nginx:latest",
				PullPolicy: "always",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the pre-pull command was executed
	foundPrePull := false
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "pull" && i+1 < len(args) && args[i+1] == "web" {
				foundPrePull = true
				break
			}
		}
	}
	if !foundPrePull {
		t.Error("expected docker compose pull command for compose spec always policy, got none")
	}

	// Verify --pull always in compose up/create commands
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundPull := false
		for i, arg := range args {
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "always" {
				foundPull = true
				break
			}
		}
		if !foundPull {
			t.Errorf("expected --pull always in compose command, got args: %v", args)
		}
	}
}

func TestDeployServicePullPolicyIfNotPresent(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:       "web",
				Image:      "nginx:latest",
				PullPolicy: "if_not_present",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NO pre-pull command (only always triggers pre-pull)
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "pull" && i+1 < len(args) && args[i+1] == "web" {
				t.Error("unexpected docker compose pull command for if_not_present policy")
			}
		}
	}

	// Verify --pull missing in compose up/create commands
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundPull := false
		for i, arg := range args {
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "missing" {
				foundPull = true
				break
			}
		}
		if !foundPull {
			t.Errorf("expected --pull missing in compose command, got args: %v", args)
		}
	}
}

func TestDeployServicePullPolicyDefault(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NO pre-pull command (default is missing, not always)
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "pull" && i+1 < len(args) && args[i+1] == "web" {
				t.Error("unexpected docker compose pull command for default policy")
			}
		}
	}

	// Verify --pull missing in compose up/create commands
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundPull := false
		for i, arg := range args {
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "missing" {
				foundPull = true
				break
			}
		}
		if !foundPull {
			t.Errorf("expected --pull missing in compose command, got args: %v", args)
		}
	}
}

func TestDeployServicePullPolicyBuild(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:       "web",
				Image:      "nginx:latest",
				PullPolicy: "build",
				Build:      &types.BuildConfig{Context: "."},
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify pre-build command was executed
	foundPreBuild := false
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "build" && i+1 < len(args) && args[i+1] == "web" {
				foundPreBuild = true
				break
			}
		}
	}
	if !foundPreBuild {
		t.Error("expected docker compose build command for pull_policy: build, got none")
	}

	// Verify NO pre-pull command
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "pull" && i+1 < len(args) && args[i+1] == "web" {
				t.Error("unexpected docker compose pull command when building")
			}
		}
	}

	// Verify compose up/create commands contain --build and --pull never
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundBuild := false
		foundPullNever := false
		for i, arg := range args {
			if arg == "--build" {
				foundBuild = true
			}
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "never" {
				foundPullNever = true
			}
		}
		if !foundBuild {
			t.Errorf("expected --build in compose command, got args: %v", args)
		}
		if !foundPullNever {
			t.Errorf("expected --pull never in compose command, got args: %v", args)
		}
	}
}

func TestDeployServicePullPolicyBuildNoBuildSection(t *testing.T) {
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:       "web",
				Image:      "nginx:latest",
				PullPolicy: "build",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err == nil {
		t.Fatal("expected error for pull_policy: build without build section, got nil")
	}
	if !strings.Contains(err.Error(), "requires a build section") {
		t.Errorf("expected error to contain 'requires a build section', got: %v", err)
	}
}

func TestDeployServiceBuildFlag(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
				Build: &types.BuildConfig{Context: "."},
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Build:                 true,
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify pre-build command was executed
	foundPreBuild := false
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "build" && i+1 < len(args) && args[i+1] == "web" {
				foundPreBuild = true
				break
			}
		}
	}
	if !foundPreBuild {
		t.Error("expected docker compose build command for --build flag, got none")
	}

	// Verify compose up/create commands contain --build and --pull missing (default)
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		foundBuild := false
		foundPullMissing := false
		for i, arg := range args {
			if arg == "--build" {
				foundBuild = true
			}
			if arg == "--pull" && i+1 < len(args) && args[i+1] == "missing" {
				foundPullMissing = true
			}
		}
		if !foundBuild {
			t.Errorf("expected --build in compose command, got args: %v", args)
		}
		if !foundPullMissing {
			t.Errorf("expected --pull missing in compose command, got args: %v", args)
		}
	}
}

func TestDeployServiceBuildFlagNoBuildSection(t *testing.T) {
	var executedArgs [][]string

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executedArgs = append(executedArgs, input.Args)
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name:  "web",
				Image: "nginx:latest",
			},
		},
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Build:                 true,
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NO pre-build command (service has no build section)
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		for i, arg := range args {
			if arg == "build" && i+1 < len(args) && args[i+1] == "web" {
				t.Error("unexpected docker compose build command for service without build section")
			}
		}
	}

	// Verify compose up/create commands do NOT contain --build
	for _, args := range executedArgs {
		if args[0] != "compose" {
			continue
		}
		hasUp := false
		hasCreate := false
		for _, arg := range args {
			if arg == "up" {
				hasUp = true
			}
			if arg == "create" {
				hasCreate = true
			}
		}
		if !hasUp && !hasCreate {
			continue
		}
		for _, arg := range args {
			if arg == "--build" {
				t.Errorf("unexpected --build in compose command for service without build section, got args: %v", args)
			}
		}
	}
}

func TestDeployServiceWaitAfterHealthy(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("valid x-wait-after-healthy is parsed without error", func(t *testing.T) {
		buf.Reset()
		callCount := 0

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Extensions: map[string]interface{}{
								"x-wait-after-healthy": "5s",
							},
						},
					},
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid x-wait-after-healthy returns error", func(t *testing.T) {
		buf.Reset()
		callCount := 0

		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Extensions: map[string]interface{}{
								"x-wait-after-healthy": "not-a-duration",
							},
						},
					},
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		}

		err := DeployService(context.Background(), input)
		if err == nil {
			t.Fatal("expected error for invalid duration, got nil")
		}
		if !strings.Contains(err.Error(), "invalid x-wait-after-healthy duration") {
			t.Errorf("expected error about invalid duration, got: %v", err)
		}
	})
}

func TestIsOneShotService(t *testing.T) {
	tests := []struct {
		name     string
		restart  string
		expected bool
	}{
		{name: "restart_no", restart: "no", expected: true},
		{name: "restart_empty", restart: "", expected: false},
		{name: "restart_always", restart: "always", expected: false},
		{name: "restart_on_failure", restart: "on-failure", expected: false},
		{name: "restart_unless_stopped", restart: "unless-stopped", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &types.ServiceConfig{
				Name:    "test",
				Restart: tt.restart,
			}
			result := IsOneShotService(service)
			if result != tt.expected {
				t.Errorf("IsOneShotService() = %v, want %v for restart=%q", result, tt.expected, tt.restart)
			}
		})
	}
}

func TestDeployOneShotService(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("runs_docker_compose_run_rm_no_deps", func(t *testing.T) {
		var capturedArgs [][]string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			capturedArgs = append(capturedArgs, input.Args)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		mockClient := &mockDockerClient{}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(capturedArgs) != 1 {
			t.Fatalf("expected 1 command, got %d", len(capturedArgs))
		}

		args := capturedArgs[0]
		argsStr := strings.Join(args, " ")
		if !strings.Contains(argsStr, "run") {
			t.Errorf("expected 'run' in args, got: %v", args)
		}
		if !strings.Contains(argsStr, "--rm") {
			t.Errorf("expected '--rm' in args, got: %v", args)
		}
		if !strings.Contains(argsStr, "--no-deps") {
			t.Errorf("expected '--no-deps' in args, got: %v", args)
		}
		if !strings.Contains(argsStr, "migrate") {
			t.Errorf("expected 'migrate' in args, got: %v", args)
		}
		// Should NOT contain rolling update args
		if strings.Contains(argsStr, "up") {
			t.Errorf("one-shot should not use 'up', got: %v", args)
		}
		if strings.Contains(argsStr, "--scale") {
			t.Errorf("one-shot should not use '--scale', got: %v", args)
		}
		if strings.Contains(argsStr, "--detach") {
			t.Errorf("one-shot should not use '--detach', got: %v", args)
		}
	})

	t.Run("exit_0_returns_success", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		mockClient := &mockDockerClient{}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "One-shot service migrate completed successfully") {
			t.Errorf("expected success message in output, got: %s", output)
		}
	})

	t.Run("exit_1_returns_error", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("exit status 1")
		}

		mockClient := &mockDockerClient{}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "one-shot service migrate failed") {
			t.Errorf("expected error message about one-shot failure, got: %v", err)
		}
	})

	t.Run("no_docker_client_calls", func(t *testing.T) {
		removeCalled := false
		renameCalled := false
		terminateCalled := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
			containerRemove: func(ctx context.Context, id string, options container.RemoveOptions) error {
				removeCalled = true
				return nil
			},
			containerRename: func(ctx context.Context, id, name string) error {
				renameCalled = true
				return nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				terminateCalled = true
				return nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if removeCalled {
			t.Error("expected no container remove calls for one-shot service")
		}
		if renameCalled {
			t.Error("expected no container rename calls for one-shot service")
		}
		if terminateCalled {
			t.Error("expected no container terminate calls for one-shot service")
		}
	})

	t.Run("build_flag_builds_before_run", func(t *testing.T) {
		var capturedArgs [][]string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			capturedArgs = append(capturedArgs, input.Args)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		mockClient := &mockDockerClient{}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
					Build:   &types.BuildConfig{},
				},
			},
		}

		input := DeployServiceInput{
			Build:                 true,
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(capturedArgs) != 2 {
			t.Fatalf("expected 2 commands (build + run), got %d", len(capturedArgs))
		}

		buildArgs := strings.Join(capturedArgs[0], " ")
		if !strings.Contains(buildArgs, "build") {
			t.Errorf("expected first command to be build, got: %v", capturedArgs[0])
		}

		runArgs := strings.Join(capturedArgs[1], " ")
		if !strings.Contains(runArgs, "run") {
			t.Errorf("expected second command to be run, got: %v", capturedArgs[1])
		}
	})

	t.Run("pull_always_pulls_before_run", func(t *testing.T) {
		var capturedArgs [][]string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			capturedArgs = append(capturedArgs, input.Args)
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		mockClient := &mockDockerClient{}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		input := DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			PullPolicy:            "always",
			ServiceName:           "migrate",
		}

		err := DeployService(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(capturedArgs) != 2 {
			t.Fatalf("expected 2 commands (pull + run), got %d", len(capturedArgs))
		}

		pullArgs := strings.Join(capturedArgs[0], " ")
		if !strings.Contains(pullArgs, "pull") {
			t.Errorf("expected first command to be pull, got: %v", capturedArgs[0])
		}

		runArgs := strings.Join(capturedArgs[1], " ")
		if !strings.Contains(runArgs, "run") {
			t.Errorf("expected second command to be run, got: %v", capturedArgs[1])
		}
	})
}

func TestOrderServicesOneShotPriority(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("one_shot_no_deps_before_web_no_deps", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
			},
		}

		result, err := OrderServices(ctx, DeployProjectInput{
			Project: project,
			Logger:  logger,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// migrate should come before web
		migrateIdx := -1
		webIdx := -1
		for i, name := range result {
			if name == "migrate" {
				migrateIdx = i
			}
			if name == "web" {
				webIdx = i
			}
		}

		if migrateIdx == -1 {
			t.Fatal("migrate not found in result")
		}
		if webIdx == -1 {
			t.Fatal("web not found in result")
		}
		if migrateIdx >= webIdx {
			t.Errorf("expected migrate (idx=%d) before web (idx=%d), order: %v", migrateIdx, webIdx, result)
		}
	})

	t.Run("multiple_one_shots_no_deps_before_web", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
				},
				"seed": types.ServiceConfig{
					Name:    "seed",
					Restart: "no",
				},
			},
		}

		result, err := OrderServices(ctx, DeployProjectInput{
			Project: project,
			Logger:  logger,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Both one-shots should come before web
		webIdx := -1
		for i, name := range result {
			if name == "web" {
				webIdx = i
			}
		}

		for _, name := range []string{"migrate", "seed"} {
			idx := -1
			for i, n := range result {
				if n == name {
					idx = i
					break
				}
			}
			if idx == -1 {
				t.Fatalf("%s not found in result", name)
			}
			if idx >= webIdx {
				t.Errorf("expected %s (idx=%d) before web (idx=%d), order: %v", name, idx, webIdx, result)
			}
		}
	})

	t.Run("one_shot_with_deps_follows_dependency_order", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"db": types.ServiceConfig{
					Name: "db",
				},
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
					DependsOn: map[string]types.ServiceDependency{
						"db": {},
					},
				},
				"web": types.ServiceConfig{
					Name: "web",
					DependsOn: map[string]types.ServiceDependency{
						"migrate": {},
					},
				},
			},
		}

		result, err := OrderServices(ctx, DeployProjectInput{
			Project: project,
			Logger:  logger,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Order should be: db, migrate, web (dependency chain)
		serviceIndex := make(map[string]int)
		for i, name := range result {
			serviceIndex[name] = i
		}

		if serviceIndex["db"] >= serviceIndex["migrate"] {
			t.Errorf("expected db before migrate, order: %v", result)
		}
		if serviceIndex["migrate"] >= serviceIndex["web"] {
			t.Errorf("expected migrate before web, order: %v", result)
		}
	})

	t.Run("post_deploy_one_shot_depends_on_web_runs_after", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
				"warm-cache": types.ServiceConfig{
					Name:    "warm-cache",
					Restart: "no",
					DependsOn: map[string]types.ServiceDependency{
						"web": {},
					},
				},
			},
		}

		result, err := OrderServices(ctx, DeployProjectInput{
			Project: project,
			Logger:  logger,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		serviceIndex := make(map[string]int)
		for i, name := range result {
			serviceIndex[name] = i
		}

		if serviceIndex["web"] >= serviceIndex["warm-cache"] {
			t.Errorf("expected web before warm-cache, order: %v", result)
		}
	})

	t.Run("web_as_one_shot_not_duplicated", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:    "web",
					Restart: "no",
				},
				"api": types.ServiceConfig{
					Name: "api",
				},
			},
		}

		result, err := OrderServices(ctx, DeployProjectInput{
			Project: project,
			Logger:  logger,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// web should appear exactly once
		webCount := 0
		for _, name := range result {
			if name == "web" {
				webCount++
			}
		}
		if webCount != 1 {
			t.Errorf("expected web to appear once, appeared %d times, order: %v", webCount, result)
		}

		// web (one-shot) should still be first
		if result[0] != "web" {
			t.Errorf("expected web to be first, got %s, order: %v", result[0], result)
		}
	})
}

func TestDeployServicePreDeployHostCommand(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("pre-deploy runs before container operations", func(t *testing.T) {
		var operationOrder []string
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				operationOrder = append(operationOrder, "container_list")
				return []container.Summary{}, nil
			},
			containerRename: func(ctx context.Context, id, name string) error {
				operationOrder = append(operationOrder, "container_rename")
				return nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				operationOrder = append(operationOrder, "pre-deploy-script")
			} else {
				operationOrder = append(operationOrder, "docker-compose")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command": "echo pre-deploy",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Find the pre-deploy-script and verify it appears before docker-compose operations
		preDeployIdx := -1
		dockerComposeIdx := -1
		for i, op := range operationOrder {
			if op == "pre-deploy-script" && preDeployIdx == -1 {
				preDeployIdx = i
			}
			if op == "docker-compose" && dockerComposeIdx == -1 {
				dockerComposeIdx = i
			}
		}
		if preDeployIdx == -1 {
			t.Errorf("expected pre-deploy-script in operations, got: %v", operationOrder)
		}
		if dockerComposeIdx != -1 && preDeployIdx > dockerComposeIdx {
			t.Errorf("expected pre-deploy-script before docker-compose, got order: %v", operationOrder)
		}
	})

	t.Run("pre-deploy failure aborts deployment", func(t *testing.T) {
		dockerComposeRan := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("exit status 1")
			}
			dockerComposeRan = true
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command": "exit 1",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "pre-deploy host command failed") {
			t.Errorf("expected pre-deploy error, got: %v", err)
		}
		if dockerComposeRan {
			t.Error("docker compose should not have been called after pre-deploy failure")
		}
	})
}

func TestDeployServicePostDeployHostCommand(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("post-deploy runs after successful deploy", func(t *testing.T) {
		postDeployRan := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				postDeployRan = true
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-post-deploy-host-command": "echo post-deploy",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !postDeployRan {
			t.Error("expected post-deploy script to run")
		}
	})

	t.Run("post-deploy does NOT run on deploy failure", func(t *testing.T) {
		postDeployRan := false
		callCount := 0
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				if callCount == 1 {
					return nil, fmt.Errorf("simulated container list error")
				}
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				postDeployRan = true
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-post-deploy-host-command": "echo post-deploy",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err == nil {
			t.Fatal("expected error from deploy failure")
		}
		if postDeployRan {
			t.Error("post-deploy should NOT have run after deploy failure")
		}
	})
}

func TestDeployOneShotServiceDeployHooks(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("one-shot service runs pre and post deploy hooks", func(t *testing.T) {
		var scriptCount int
		mockClient := &mockDockerClient{}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				scriptCount++
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command":  "echo pre",
							"x-post-deploy-host-command": "echo post",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// pre-deploy + post-deploy = 2 scripts
		if scriptCount != 2 {
			t.Errorf("expected 2 script executions (pre + post), got %d", scriptCount)
		}
	})

	t.Run("one-shot failure skips post-deploy hook", func(t *testing.T) {
		var scriptCount int
		mockClient := &mockDockerClient{}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				scriptCount++
				return ExecCommandResponse{ExitCode: 0}, nil
			}
			// docker compose run fails
			return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("exit status 1")
		}

		project := &types.Project{
			Services: types.Services{
				"migrate": types.ServiceConfig{
					Name:    "migrate",
					Restart: "no",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command":  "echo pre",
							"x-post-deploy-host-command": "echo post",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "migrate",
		})
		if err == nil {
			t.Fatal("expected error from one-shot failure")
		}
		// Only pre-deploy should have run, not post-deploy
		if scriptCount != 1 {
			t.Errorf("expected 1 script execution (pre only), got %d", scriptCount)
		}
	})
}

func TestDeployProjectDeployHooks(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("project-level hooks bracket service deployments", func(t *testing.T) {
		var operationOrder []string
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				operationOrder = append(operationOrder, "script")
			} else {
				operationOrder = append(operationOrder, "docker-compose")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
			},
			Extensions: map[string]interface{}{
				"x-pre-deploy-host-command":  "echo project-pre",
				"x-post-deploy-host-command": "echo project-post",
			},
		}

		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(operationOrder) < 2 {
			t.Fatalf("expected at least 2 operations, got %d: %v", len(operationOrder), operationOrder)
		}
		// First operation should be project pre-deploy script
		if operationOrder[0] != "script" {
			t.Errorf("expected first operation to be script (project pre-deploy), got %s", operationOrder[0])
		}
		// Last operation should be project post-deploy script
		if operationOrder[len(operationOrder)-1] != "script" {
			t.Errorf("expected last operation to be script (project post-deploy), got %s", operationOrder[len(operationOrder)-1])
		}
	})

	t.Run("project pre-deploy failure aborts all deployments", func(t *testing.T) {
		serviceCalled := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				serviceCalled = true
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("exit status 1")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
			},
			Extensions: map[string]interface{}{
				"x-pre-deploy-host-command": "exit 1",
			},
		}

		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "project pre-deploy host command failed") {
			t.Errorf("expected project pre-deploy error, got: %v", err)
		}
		if serviceCalled {
			t.Error("no service operations should have run after project pre-deploy failure")
		}
	})

	t.Run("project post-deploy does NOT run on service failure", func(t *testing.T) {
		var scriptCount int
		callCount := 0
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				if callCount == 1 {
					return nil, fmt.Errorf("simulated error")
				}
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 0 && input.Args[0] == "-c" {
				scriptCount++
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
				},
			},
			Extensions: map[string]interface{}{
				"x-pre-deploy-host-command":  "echo pre",
				"x-post-deploy-host-command": "echo post",
			},
		}

		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       logger,
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil {
			t.Fatal("expected error from service failure")
		}
		// Only project pre-deploy should have run, not post-deploy
		if scriptCount != 1 {
			t.Errorf("expected 1 script (project pre only), got %d", scriptCount)
		}
	})
}

func TestEnsureModels(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("no_models_is_noop", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			t.Fatal("executor should not be called when there are no models")
			return ExecCommandResponse{}, nil
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  &types.Project{},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Also test nil project
		err = ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  nil,
		})
		if err != nil {
			t.Fatalf("unexpected error for nil project: %v", err)
		}
	})

	t.Run("plugin_missing_fails", func(t *testing.T) {
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if input.Command == "docker" && len(input.Args) > 0 && input.Args[0] == "model" && input.Args[1] == "version" {
				return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("docker: 'model' is not a docker command")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "docker-model plugin is not available") {
			t.Errorf("expected docker-model plugin error, got: %v", err)
		}
	})

	t.Run("pulls_missing_model", func(t *testing.T) {
		var commands []string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			cmd := input.Command + " " + strings.Join(input.Args, " ")
			commands = append(commands, cmd)
			if input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: "[]"}, nil
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify pull was called
		pullCalled := false
		configureCalled := false
		for _, cmd := range commands {
			if strings.Contains(cmd, "model pull ai/smollm2") {
				pullCalled = true
			}
			if strings.Contains(cmd, "model configure") && strings.Contains(cmd, "ai/smollm2") {
				configureCalled = true
			}
		}
		if !pullCalled {
			t.Error("expected docker model pull to be called for missing model")
		}
		if !configureCalled {
			t.Error("expected docker model configure to be called")
		}
	})

	t.Run("skips_pull_for_available_model", func(t *testing.T) {
		var commands []string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			cmd := input.Command + " " + strings.Join(input.Args, " ")
			commands = append(commands, cmd)
			if input.Args[1] == "ls" {
				return ExecCommandResponse{
					ExitCode: 0,
					Stdout:   `[{"Id": "ai/smollm2", "Tags": ["latest"]}]`,
				}, nil
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, cmd := range commands {
			if strings.Contains(cmd, "model pull") {
				t.Error("docker model pull should not be called for an available model")
			}
		}

		// Configure should still be called
		configureCalled := false
		for _, cmd := range commands {
			if strings.Contains(cmd, "model configure") {
				configureCalled = true
			}
		}
		if !configureCalled {
			t.Error("expected docker model configure to be called even for available model")
		}
	})

	t.Run("matches_model_by_tag", func(t *testing.T) {
		var commands []string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			cmd := input.Command + " " + strings.Join(input.Args, " ")
			commands = append(commands, cmd)
			if input.Args[1] == "ls" {
				return ExecCommandResponse{
					ExitCode: 0,
					Stdout:   `[{"Id": "some-id", "Tags": ["ai/smollm2"]}]`,
				}, nil
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, cmd := range commands {
			if strings.Contains(cmd, "model pull") {
				t.Error("docker model pull should not be called when model matched by tag")
			}
		}
	})

	t.Run("configures_with_context_size", func(t *testing.T) {
		var configureArgs string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 1 && input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: `[{"Id": "ai/smollm2", "Tags": []}]`}, nil
			}
			if len(input.Args) > 1 && input.Args[1] == "configure" {
				configureArgs = strings.Join(input.Args, " ")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model:       "ai/smollm2",
					ContextSize: 2048,
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(configureArgs, "--context-size 2048") {
			t.Errorf("expected configure args to include --context-size 2048, got: %s", configureArgs)
		}
	})

	t.Run("configures_with_runtime_flags", func(t *testing.T) {
		var configureArgs string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 1 && input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: `[{"Id": "ai/smollm2", "Tags": []}]`}, nil
			}
			if len(input.Args) > 1 && input.Args[1] == "configure" {
				configureArgs = strings.Join(input.Args, " ")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model:        "ai/smollm2",
					RuntimeFlags: []string{"--threads=4", "--gpu"},
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(configureArgs, "-- --threads=4 --gpu") {
			t.Errorf("expected configure args to include runtime flags after --, got: %s", configureArgs)
		}
	})

	t.Run("pull_failure_aborts", func(t *testing.T) {
		configureCalled := false
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 1 && input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: "[]"}, nil
			}
			if len(input.Args) > 1 && input.Args[1] == "pull" {
				return ExecCommandResponse{ExitCode: 1}, fmt.Errorf("network error")
			}
			if len(input.Args) > 1 && input.Args[1] == "configure" {
				configureCalled = true
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err == nil {
			t.Fatal("expected error from pull failure, got nil")
		}
		if !strings.Contains(err.Error(), "failed to pull model") {
			t.Errorf("expected pull failure error, got: %v", err)
		}
		if configureCalled {
			t.Error("configure should not be called after pull failure")
		}
	})

	t.Run("name_field_takes_precedence", func(t *testing.T) {
		var pullArgs string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 1 && input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: "[]"}, nil
			}
			if len(input.Args) > 1 && input.Args[1] == "pull" {
				pullArgs = strings.Join(input.Args, " ")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Name:  "custom-name",
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(pullArgs, "custom-name") {
			t.Errorf("expected pull to use Name field 'custom-name', got: %s", pullArgs)
		}
	})

	t.Run("model_field_used_when_no_name", func(t *testing.T) {
		var pullArgs string
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			if len(input.Args) > 1 && input.Args[1] == "ls" {
				return ExecCommandResponse{ExitCode: 0, Stdout: "[]"}, nil
			}
			if len(input.Args) > 1 && input.Args[1] == "pull" {
				pullArgs = strings.Join(input.Args, " ")
			}
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Models: types.Models{
				"llm": types.ModelConfig{
					Model: "ai/smollm2",
				},
			},
		}

		err := ensureModels(context.Background(), ensureModelsInput{
			Executor: mockExecutor,
			Logger:   logger,
			Project:  project,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(pullArgs, "ai/smollm2") {
			t.Errorf("expected pull to use Model field 'ai/smollm2', got: %s", pullArgs)
		}
	})
}

func TestDeployServiceDeployHooksDetached(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("detached flags are parsed correctly", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command":           "echo pre",
							"x-post-deploy-host-command":          "echo post",
							"x-pre-deploy-host-command-detached":  true,
							"x-post-deploy-host-command-detached": true,
						},
					},
				},
			},
		}

		// Should not error - detached flags should be parsed correctly
		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid detached flag produces error", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name: "web",
					Deploy: &types.DeployConfig{
						Extensions: map[string]interface{}{
							"x-pre-deploy-host-command":          "echo pre",
							"x-pre-deploy-host-command-detached": "not-a-bool",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err == nil {
			t.Fatal("expected error for invalid detached flag")
		}
	})
}

func TestDeployServiceStopCommandDetached(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	t.Run("stop command detached flags are parsed correctly", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Order: "start-first",
							Extensions: map[string]interface{}{
								"x-pre-stop-host-command":           "echo pre-stop",
								"x-post-stop-host-command":          "echo post-stop",
								"x-pre-stop-host-command-detached":  true,
								"x-post-stop-host-command-detached": true,
							},
						},
					},
				},
			},
		}

		// Should not error - detached flags should be parsed correctly
		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid stop command detached flag produces error", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "existing_cont_01", Names: []string{"/bats-web-1"}, State: "running", Created: 100},
				}, nil
			},
			containerInspect: func(ctx context.Context, id string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{
						ID: id,
					},
					NetworkSettings: &container.NetworkSettings{},
				}, nil
			},
		}

		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{ExitCode: 0}, nil
		}

		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						Replicas: intPtr(1),
						UpdateConfig: &types.UpdateConfig{
							Order: "start-first",
							Extensions: map[string]interface{}{
								"x-pre-stop-host-command":          "echo pre-stop",
								"x-pre-stop-host-command-detached": "not-a-bool",
							},
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err == nil {
			t.Fatal("expected error for invalid detached flag")
		}
		if !strings.Contains(err.Error(), "must be a boolean") {
			t.Errorf("expected error to contain 'must be a boolean', got: %v", err)
		}
	})
}

func TestDeployServiceNoDeployConfig(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	// Service with nil Deploy config should not error
	project := &types.Project{
		Services: types.Services{
			"web": types.ServiceConfig{
				Name: "web",
			},
		},
	}

	err := DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceConfigStatus(t *testing.T) {
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	tests := []struct {
		name           string
		force          bool
		buildImage     bool
		pullPolicy     string
		containers     []container.Summary
		replicas       int
		imageID        string
		expectedResult ServiceConfigStatus
	}{
		{
			name:           "no_containers_always_deploy",
			containers:     []container.Summary{},
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "all_hashes_match_same_replica_count",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       1,
			expectedResult: ServiceConfigUnchanged,
		},
		{
			name: "hash_mismatch",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: "wrong-hash"}},
			},
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "replica_count_mismatch_hashes_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       3,
			expectedResult: ServiceReplicaOnlyChange,
		},
		{
			name: "partial_hash_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "bbb_container_id", Labels: map[string]string{api.ConfigHashLabel: "wrong-hash"}},
			},
			replicas:       2,
			expectedResult: ServiceConfigChanged,
		},
		{
			name:  "force_always_deploys",
			force: true,
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name:       "build_image_always_deploys",
			buildImage: true,
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "missing_label_proceeds",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{}},
			},
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "multiple_containers_all_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "bbb_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "ccc_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       3,
			expectedResult: ServiceConfigUnchanged,
		},
		{
			name:       "pull_always_same_image_skips",
			pullPolicy: "always",
			containers: []container.Summary{
				{ID: "aaa_container_id", ImageID: "sha256:abc123", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			imageID:        "sha256:abc123",
			replicas:       1,
			expectedResult: ServiceConfigUnchanged,
		},
		{
			name:       "pull_always_different_image_deploys",
			pullPolicy: "always",
			containers: []container.Summary{
				{ID: "aaa_container_id", ImageID: "sha256:old_image", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			imageID:        "sha256:new_image",
			replicas:       1,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "scale_up_all_hashes_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       3,
			expectedResult: ServiceReplicaOnlyChange,
		},
		{
			name: "scale_down_all_hashes_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "bbb_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "ccc_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			replicas:       1,
			expectedResult: ServiceReplicaOnlyChange,
		},
		{
			name: "replica_mismatch_with_hash_mismatch",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: "wrong-hash"}},
			},
			replicas:       3,
			expectedResult: ServiceConfigChanged,
		},
		{
			name: "replica_mismatch_partial_hash_match",
			containers: []container.Summary{
				{ID: "aaa_container_id", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
				{ID: "bbb_container_id", Labels: map[string]string{api.ConfigHashLabel: "wrong-hash"}},
			},
			replicas:       3,
			expectedResult: ServiceConfigChanged,
		},
		{
			name:       "pull_always_replica_change_same_image",
			pullPolicy: "always",
			containers: []container.Summary{
				{ID: "aaa_container_id", ImageID: "sha256:abc123", Labels: map[string]string{api.ConfigHashLabel: expectedHash}},
			},
			imageID:        "sha256:abc123",
			replicas:       3,
			expectedResult: ServiceReplicaOnlyChange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := &command.ZerologUi{
				StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
				OriginalFields:    nil,
				Ui:                nil,
				OutputIndentField: false,
			}

			mockClient := &mockDockerClient{
				containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
					return tt.containers, nil
				},
				imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
					if tt.imageID != "" {
						return image.InspectResponse{ID: tt.imageID}, nil
					}
					return image.InspectResponse{}, fmt.Errorf("no image")
				},
			}

			mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
				return ExecCommandResponse{ExitCode: 0}, nil
			}

			result, err := serviceConfigStatus(context.Background(), ServiceConfigStatusInput{
				BuildImage:   tt.buildImage,
				Client:       mockClient,
				ComposeFiles: []string{"/tmp/docker-compose.yaml"},
				Executor:     mockExecutor,
				Force:        tt.force,
				Logger:       logger,
				ProjectName:  "test",
				PullPolicy:   tt.pullPolicy,
				Replicas:     tt.replicas,
				Service:      &service,
				ServiceName:  "web",
			})

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expectedResult {
				t.Errorf("expected %v, got %v", tt.expectedResult, result)
			}

			output := buf.String()
			if tt.expectedResult == ServiceConfigUnchanged {
				if !strings.Contains(output, "Skipping unchanged service") {
					t.Errorf("expected 'Skipping unchanged service' in output, got: %s", output)
				}
			}
			if tt.expectedResult == ServiceReplicaOnlyChange {
				if !strings.Contains(output, "Scaling replica-only change") {
					t.Errorf("expected 'Scaling replica-only change' in output, got: %s", output)
				}
			}
		})
	}
}

func TestDeployServiceSkipsUnchanged(t *testing.T) {
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	executorCalled := false
	callCount := 0
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			callCount++
			// First call: non-running containers cleanup check (no status filter)
			// Second call: config hash check (status=running)
			if callCount <= 2 {
				return []container.Summary{
					{
						ID:      "aaa_container_id",
						State:   "running",
						Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
						Created: 100,
					},
				}, nil
			}
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executorCalled = true
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	project := &types.Project{
		Services: types.Services{
			"web": service,
		},
	}

	err = DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Skipping unchanged service") {
		t.Errorf("expected 'Skipping unchanged service' in output, got: %s", output)
	}

	if executorCalled {
		t.Errorf("expected executor to not be called when service is unchanged")
	}
}

func TestDeployServiceReplicaOnlyScaleUp(t *testing.T) {
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	callCount := 0
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			callCount++
			// Return 1 container with matching hash for all calls
			return []container.Summary{
				{
					ID:      "aaa_container_id",
					State:   "running",
					Names:   []string{"/test-web-1"},
					Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
					Created: 100,
				},
			}, nil
		},
		containerRename: func(ctx context.Context, containerID, newName string) error {
			return nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	project := &types.Project{
		Services: types.Services{
			"web": service,
		},
	}

	err = DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		Replicas:              3,
		ServiceName:           "web",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Scaling replica-only change") {
		t.Errorf("expected 'Scaling replica-only change' in output, got: %s", output)
	}
	if strings.Contains(output, "Starting rolling update") {
		t.Errorf("expected no rolling update for replica-only change, got: %s", output)
	}
}

func TestDeployServiceReplicaOnlyScaleDown(t *testing.T) {
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	callCount := 0
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			callCount++
			// Return 3 containers with matching hashes
			return []container.Summary{
				{
					ID:      "aaa_container_id",
					State:   "running",
					Names:   []string{"/test-web-1"},
					Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
					Created: 100,
				},
				{
					ID:      "bbb_container_id",
					State:   "running",
					Names:   []string{"/test-web-2"},
					Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
					Created: 200,
				},
				{
					ID:      "ccc_container_id",
					State:   "running",
					Names:   []string{"/test-web-3"},
					Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
					Created: 300,
				},
			}, nil
		},
		containerRename: func(ctx context.Context, containerID, newName string) error {
			return nil
		},
		containerStop: func(ctx context.Context, containerID string, options container.StopOptions) error {
			return nil
		},
		containerRemove: func(ctx context.Context, containerID string, options container.RemoveOptions) error {
			return nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	project := &types.Project{
		Services: types.Services{
			"web": service,
		},
	}

	err = DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		Replicas:              1,
		ServiceName:           "web",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Scaling replica-only change") {
		t.Errorf("expected 'Scaling replica-only change' in output, got: %s", output)
	}
	if strings.Contains(output, "Starting rolling update") {
		t.Errorf("expected no rolling update for replica-only change, got: %s", output)
	}
	if !strings.Contains(output, "Scaling down containers") {
		t.Errorf("expected 'Scaling down containers' in output, got: %s", output)
	}
}

func TestDeployServiceForceOverridesHash(t *testing.T) {
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	callCount := 0
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			callCount++
			// Return matching containers for all calls
			return []container.Summary{
				{
					ID:      "aaa_container_id",
					State:   "running",
					Names:   []string{"/test-web-1"},
					Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
					Created: 100,
				},
			}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	project := &types.Project{
		Services: types.Services{
			"web": service,
		},
	}

	err = DeployService(context.Background(), DeployServiceInput{
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Force:                 true,
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, "Skipping unchanged service") {
		t.Errorf("expected 'Skipping unchanged service' NOT in output when force=true, got: %s", output)
	}
}

func TestDeployServiceBuildWithoutBuildSection(t *testing.T) {
	// Service with no build section - --build flag should not bypass hash check
	service := types.ServiceConfig{
		Name:  "web",
		Image: "nginx:latest",
		// No Build section
	}

	expectedHash, err := compose.ServiceHash(service)
	if err != nil {
		t.Fatalf("failed to compute service hash: %v", err)
	}

	executorCalled := false
	callCount := 0
	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			callCount++
			if callCount <= 2 {
				return []container.Summary{
					{
						ID:      "aaa_container_id",
						State:   "running",
						Labels:  map[string]string{api.ConfigHashLabel: expectedHash},
						Created: 100,
					},
				}, nil
			}
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		executorCalled = true
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	project := &types.Project{
		Services: types.Services{
			"web": service,
		},
	}

	err = DeployService(context.Background(), DeployServiceInput{
		Build:                 true, // --build flag set, but no build section on service
		Client:                mockClient,
		Executor:              mockExecutor,
		ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
		ContainerNameTemplate: "{{.ServiceName}}",
		Logger:                logger,
		Project:               project,
		ProjectName:           "test",
		ServiceName:           "web",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Skipping unchanged service") {
		t.Errorf("expected 'Skipping unchanged service' when --build is set but service has no build section, got: %s", output)
	}

	if executorCalled {
		t.Errorf("expected executor to not be called when service has no build section and config is unchanged")
	}
}

func TestWarnAnonymousVolumes(t *testing.T) {
	makeImageInspect := func(volumes map[string]struct{}) func(ctx context.Context, imageID string) (image.InspectResponse, error) {
		return func(ctx context.Context, imageID string) (image.InspectResponse, error) {
			return image.InspectResponse{
				Config: &dockerspec.DockerOCIImageConfig{
					ImageConfig: ocispec.ImageConfig{
						Volumes: volumes,
					},
				},
			}, nil
		}
	}

	tests := []struct {
		name            string
		imageInspect    func(ctx context.Context, imageID string) (image.InspectResponse, error)
		serviceImage    string
		serviceVolumes  []types.ServiceVolumeConfig
		expectWarnings  []string
		expectNoWarning bool
	}{
		{
			name:         "image_volume_no_mount",
			imageInspect: makeImageInspect(map[string]struct{}{"/var/lib/data": {}}),
			serviceImage: "myimage:latest",
			expectWarnings: []string{
				"has anonymous volume at /var/lib/data",
			},
		},
		{
			name:         "image_volume_anonymous_mount",
			imageInspect: makeImageInspect(map[string]struct{}{"/var/lib/data": {}}),
			serviceImage: "myimage:latest",
			serviceVolumes: []types.ServiceVolumeConfig{
				{Type: "volume", Source: "", Target: "/var/lib/data"},
			},
			expectWarnings: []string{
				"has anonymous volume at /var/lib/data",
			},
		},
		{
			name:         "image_volume_named_mount",
			imageInspect: makeImageInspect(map[string]struct{}{"/var/lib/data": {}}),
			serviceImage: "myimage:latest",
			serviceVolumes: []types.ServiceVolumeConfig{
				{Type: "volume", Source: "mydata", Target: "/var/lib/data"},
			},
			expectNoWarning: true,
		},
		{
			name:         "image_volume_bind_mount",
			imageInspect: makeImageInspect(map[string]struct{}{"/var/lib/data": {}}),
			serviceImage: "myimage:latest",
			serviceVolumes: []types.ServiceVolumeConfig{
				{Type: "bind", Source: "/host/path", Target: "/var/lib/data"},
			},
			expectNoWarning: true,
		},
		{
			name:            "no_image_volumes",
			imageInspect:    makeImageInspect(map[string]struct{}{}),
			serviceImage:    "myimage:latest",
			expectNoWarning: true,
		},
		{
			name: "image_inspect_error",
			imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
				return image.InspectResponse{}, errors.New("image not found")
			},
			serviceImage:    "myimage:latest",
			expectNoWarning: true,
		},
		{
			name:         "multiple_volumes_mixed",
			imageInspect: makeImageInspect(map[string]struct{}{"/var/lib/a": {}, "/var/lib/b": {}}),
			serviceImage: "myimage:latest",
			serviceVolumes: []types.ServiceVolumeConfig{
				{Type: "volume", Source: "vol-a", Target: "/var/lib/a"},
			},
			expectWarnings: []string{
				"has anonymous volume at /var/lib/b",
			},
		},
		{
			name: "nil_config",
			imageInspect: func(ctx context.Context, imageID string) (image.InspectResponse, error) {
				return image.InspectResponse{Config: nil}, nil
			},
			serviceImage:    "myimage:latest",
			expectNoWarning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockDockerClient{
				imageInspect: tt.imageInspect,
			}

			var buf bytes.Buffer
			logger := &command.ZerologUi{
				StderrLogger: zerolog.New(&buf).With().Timestamp().Logger(),
				StdoutLogger: zerolog.New(&buf).With().Timestamp().Logger(),
			}

			service := &types.ServiceConfig{
				Name:    "web",
				Image:   tt.serviceImage,
				Volumes: tt.serviceVolumes,
			}

			warnAnonymousVolumes(context.Background(), mockClient, logger, "testproject", "web", service)

			output := buf.String()

			if tt.expectNoWarning {
				if strings.Contains(output, "has anonymous volume") {
					t.Errorf("expected no warning, got: %s", output)
				}
				return
			}

			for _, expected := range tt.expectWarnings {
				if !strings.Contains(output, expected) {
					t.Errorf("expected warning containing %q, got: %s", expected, output)
				}
			}
		})
	}
}

func TestDeployServiceFailureActionValidation(t *testing.T) {
	var buf bytes.Buffer
	logger := &command.ZerologUi{
		StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
		OriginalFields:    nil,
		Ui:                nil,
		OutputIndentField: false,
	}

	mockClient := &mockDockerClient{
		containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{}, nil
		},
	}

	mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
		return ExecCommandResponse{ExitCode: 0}, nil
	}

	t.Run("failure_action rollback accepted", func(t *testing.T) {
		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Parallelism:   &parallelism,
							FailureAction: "rollback",
							Order:         "start-first",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error for failure_action rollback: %v", err)
		}
	})

	t.Run("failure_action pause accepted", func(t *testing.T) {
		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Parallelism:   &parallelism,
							FailureAction: "pause",
							Order:         "start-first",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error for failure_action pause: %v", err)
		}
	})

	t.Run("failure_action invalid rejected", func(t *testing.T) {
		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Parallelism:   &parallelism,
							FailureAction: "continue",
							Order:         "start-first",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err == nil {
			t.Fatal("expected error for invalid failure_action, got nil")
		}
		if !strings.Contains(err.Error(), "failure_action must be 'pause' or 'rollback'") {
			t.Errorf("expected error to contain 'failure_action must be 'pause' or 'rollback'', got '%s'", err.Error())
		}
	})

	t.Run("failure_action empty accepted", func(t *testing.T) {
		parallelism := uint64(1)
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{
					Name:  "web",
					Image: "nginx:latest",
					Deploy: &types.DeployConfig{
						UpdateConfig: &types.UpdateConfig{
							Parallelism:   &parallelism,
							FailureAction: "",
							Order:         "start-first",
						},
					},
				},
			},
		}

		err := DeployService(context.Background(), DeployServiceInput{
			Client:                mockClient,
			Executor:              mockExecutor,
			ComposeFiles:          []string{"/tmp/docker-compose.yaml"},
			ContainerNameTemplate: "{{.ServiceName}}",
			Logger:                logger,
			Project:               project,
			ProjectName:           "test",
			ServiceName:           "web",
		})
		if err != nil {
			t.Fatalf("unexpected error for empty failure_action: %v", err)
		}
	})
}

func intPtr(i int) *int {
	return &i
}

func TestRemoveMissingServices(t *testing.T) {
	makeLogger := func() (*command.ZerologUi, *bytes.Buffer) {
		var buf bytes.Buffer
		logger := &command.ZerologUi{
			StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			OriginalFields:    nil,
			Ui:                nil,
			OutputIndentField: false,
		}
		return logger, &buf
	}

	t.Run("first_list_error_returned", func(t *testing.T) {
		logger, _ := makeLogger()
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return nil, errors.New("list boom")
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err == nil || !strings.Contains(err.Error(), "error querying containers") {
			t.Errorf("expected list error, got: %v", err)
		}
	})

	t.Run("no_containers_noop", func(t *testing.T) {
		logger, _ := makeLogger()
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("all_containers_match_ordered_services", func(t *testing.T) {
		logger, _ := makeLogger()
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{
						ID:     "c1",
						Labels: map[string]string{"com.docker.compose.service": "web"},
					},
				}, nil
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("container_without_service_label_skipped", func(t *testing.T) {
		logger, _ := makeLogger()
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{
					{ID: "c1", Labels: nil},
					{ID: "c2", Labels: map[string]string{"some-other-label": "value"}},
				}, nil
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("extra_service_second_list_error", func(t *testing.T) {
		logger, _ := makeLogger()
		callCount := 0
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				if callCount == 1 {
					return []container.Summary{
						{
							ID:     "c-extra",
							Labels: map[string]string{"com.docker.compose.service": "legacy"},
						},
					}, nil
				}
				return nil, errors.New("current list boom")
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err == nil || !strings.Contains(err.Error(), "error getting current containers") {
			t.Errorf("expected current-list error, got: %v", err)
		}
	})

	t.Run("extra_service_removed_cleanly", func(t *testing.T) {
		logger, _ := makeLogger()
		callCount := 0
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				if callCount == 1 {
					return []container.Summary{
						{
							ID:     "c-extra",
							Labels: map[string]string{"com.docker.compose.service": "legacy"},
						},
					}, nil
				}
				// Second call returns empty so scaleDownContainers early-returns.
				return []container.Summary{}, nil
			},
		}
		err := RemoveMissingServices(context.Background(), DeployProjectInput{
			Client:      mockClient,
			Logger:      logger,
			ProjectName: "test",
		}, []string{"web"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 list calls, got %d", callCount)
		}
	})
}

func TestDeployProjectErrorPaths(t *testing.T) {
	makeLogger := func() *command.ZerologUi {
		var buf bytes.Buffer
		return &command.ZerologUi{
			StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			OriginalFields:    nil,
			Ui:                nil,
			OutputIndentField: false,
		}
	}

	t.Run("invalid_pre_deploy_detached_flag", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{}, nil
		}
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{Name: "web"},
			},
			Extensions: map[string]interface{}{
				"x-pre-deploy-host-command-detached": "not-a-bool",
			},
		}
		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       makeLogger(),
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil || !strings.Contains(err.Error(), "must be a boolean") {
			t.Errorf("expected boolean error, got: %v", err)
		}
	})

	t.Run("invalid_post_deploy_detached_flag", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{}, nil
		}
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{Name: "web"},
			},
			Extensions: map[string]interface{}{
				"x-post-deploy-host-command-detached": "not-a-bool",
			},
		}
		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       makeLogger(),
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil || !strings.Contains(err.Error(), "must be a boolean") {
			t.Errorf("expected boolean error, got: %v", err)
		}
	})

	t.Run("remove_missing_services_error_propagates", func(t *testing.T) {
		callCount := 0
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				callCount++
				// First calls (during DeployService) succeed; the
				// RemoveMissingServices call fails. Use a sentinel by
				// counting — DeployService calls containerList several
				// times; we fail on a late call.
				if callCount > 4 {
					return nil, errors.New("remove missing list boom")
				}
				return []container.Summary{}, nil
			},
		}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			return ExecCommandResponse{}, nil
		}
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{Name: "web"},
			},
		}
		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       makeLogger(),
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("expected a list error, got: %v", err)
		}
	})

	t.Run("project_post_deploy_command_failure", func(t *testing.T) {
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{}, nil
			},
		}
		mockExecutor := func(ctx context.Context, input ExecCommandInput) (ExecCommandResponse, error) {
			// Fail only when the post-deploy script runs (sh -c echo post-fail).
			if len(input.Args) > 0 && input.Args[0] == "-c" && strings.Contains(input.Args[1], "post-fail") {
				return ExecCommandResponse{ExitCode: 1}, errors.New("post-deploy boom")
			}
			return ExecCommandResponse{}, nil
		}
		project := &types.Project{
			Services: types.Services{
				"web": types.ServiceConfig{Name: "web"},
			},
			Extensions: map[string]interface{}{
				"x-post-deploy-host-command": "echo post-fail",
			},
		}
		err := DeployProject(context.Background(), DeployProjectInput{
			Client:       mockClient,
			Executor:     mockExecutor,
			ComposeFiles: []string{"/tmp/docker-compose.yaml"},
			Logger:       makeLogger(),
			Project:      project,
			ProjectName:  "test",
		})
		if err == nil || !strings.Contains(err.Error(), "project post-deploy host command failed") {
			t.Errorf("expected post-deploy failure, got: %v", err)
		}
	})
}

func TestShouldSkipScaleDownService(t *testing.T) {
	makeLogger := func() *command.ZerologUi {
		var buf bytes.Buffer
		return &command.ZerologUi{
			StderrLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			StdoutLogger:      zerolog.New(&buf).With().Timestamp().Logger(),
			OriginalFields:    nil,
			Ui:                nil,
			OutputIndentField: false,
		}
	}

	t.Run("no_labels_and_no_skip_databases_returns_false", func(t *testing.T) {
		result := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
			Container:   container.Summary{Image: "nginx"},
			ServiceName: "web",
			Logger:      makeLogger(),
		})
		if result {
			t.Error("expected false")
		}
	})

	t.Run("skip_label_returns_true", func(t *testing.T) {
		result := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
			Container: container.Summary{
				Image:  "nginx",
				Labels: map[string]string{"com.dokku.orchestrate/skip": "true"},
			},
			ServiceName: "web",
			Logger:      makeLogger(),
		})
		if !result {
			t.Error("expected true")
		}
	})

	t.Run("skip_label_false_value_falls_through", func(t *testing.T) {
		result := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
			Container: container.Summary{
				Image:  "nginx",
				Labels: map[string]string{"com.dokku.orchestrate/skip": "false"},
			},
			ServiceName: "web",
			Logger:      makeLogger(),
		})
		if result {
			t.Error("expected false for skip=false")
		}
	})

	t.Run("skip_databases_with_database_image_returns_true", func(t *testing.T) {
		result := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
			Container: container.Summary{
				Image: "postgres:16",
			},
			ServiceName:         "db",
			ShouldSkipDatabases: true,
			Logger:              makeLogger(),
		})
		if !result {
			t.Error("expected true for postgres image with skip_databases=true")
		}
	})

	t.Run("skip_databases_with_non_database_image_returns_false", func(t *testing.T) {
		result := shouldSkipScaleDownService(ShouldSkipScaleDownServiceInput{
			Container: container.Summary{
				Image: "nginx:latest",
			},
			ServiceName:         "web",
			ShouldSkipDatabases: true,
			Logger:              makeLogger(),
		})
		if result {
			t.Error("expected false for non-db image")
		}
	})
}
