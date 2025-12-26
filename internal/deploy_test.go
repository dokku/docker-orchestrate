package internal

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
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
			name:           "no_replicas_defined_error",
			inputReplicas:  0,
			deployReplicas: nil,
			scaleReplicas:  nil,
			expectError:    true,
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
				ComposeFile:           "/tmp/docker-compose.yaml",
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
			service := &types.ServiceConfig{
				Name:  "test-service",
				Image: tt.image,
			}

			result := isDatabaseService(service, logger)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			service := &types.ServiceConfig{
				Name:  "test-service",
				Image: tt.image,
			}

			result := shouldSkipService(service, tt.shouldSkipDatabases, logger)

			if result != tt.expectedResult {
				t.Errorf("shouldSkipService() = %v, want %v for image %s with shouldSkipDatabases=%v", result, tt.expectedResult, tt.image, tt.shouldSkipDatabases)
			}
		})
	}
}
