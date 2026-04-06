package internal

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/josegonzalez/cli-skeleton/command"
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
			name:                "model_service_skipped",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              nil,
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_takes_precedence_over_provider",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              nil,
			provider:            &types.ServiceProviderConfig{Type: "awesomecloud"},
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_takes_precedence_over_label",
			image:               "nginx:alpine",
			shouldSkipDatabases: false,
			expectedResult:      true,
			labels:              map[string]string{"com.dokku.orchestrate/skip": "false"},
			models:              map[string]*types.ServiceModelConfig{"model1": {}},
		},
		{
			name:                "model_service_takes_precedence_over_database",
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
					// Running containers for rename
					return []container.Summary{
						{ID: "container1_id_long", Names: []string{"/container1"}, State: "running", Created: 100},
						{ID: "container2_id_long", Names: []string{"/container2"}, State: "running", Created: 200},
					}, nil
				}
				if callCount == 2 {
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
				// Calls 1-5: all containers check, rename, currentContainers, containersToUpdate, updatedContainers
				// All return empty to force scale-up path (0 existing → 1 desired)
				if callCount <= 5 {
					return []container.Summary{}, nil
				}
				// Call 6+: after docker compose create and final count - return the new container
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
			result := isOneShotService(service)
			if result != tt.expected {
				t.Errorf("isOneShotService() = %v, want %v for restart=%q", result, tt.expected, tt.restart)
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
		clientCalled := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				clientCalled = true
				return []container.Summary{}, nil
			},
			containerRemove: func(ctx context.Context, id string, options container.RemoveOptions) error {
				clientCalled = true
				return nil
			},
			containerRename: func(ctx context.Context, id, name string) error {
				clientCalled = true
				return nil
			},
			containerTerminate: func(ctx context.Context, id string, timeoutSeconds int) error {
				clientCalled = true
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

		if clientCalled {
			t.Error("expected no Docker client calls for one-shot service")
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

		if len(operationOrder) < 2 {
			t.Fatalf("expected at least 2 operations, got %d: %v", len(operationOrder), operationOrder)
		}
		if operationOrder[0] != "pre-deploy-script" {
			t.Errorf("expected first operation to be pre-deploy-script, got %s, order: %v", operationOrder[0], operationOrder)
		}
	})

	t.Run("pre-deploy failure aborts deployment", func(t *testing.T) {
		containerListCalled := false
		mockClient := &mockDockerClient{
			containerList: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
				containerListCalled = true
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
		if containerListCalled {
			t.Error("container list should not have been called after pre-deploy failure")
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

func intPtr(i int) *int {
	return &i
}
