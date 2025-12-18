package internal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/mitchellh/cli"
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

			logger := cli.NewMockUi()

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

			err := DeployService(input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			// Check if the output contains the expected target-replicas
			expectedMsg := fmt.Sprintf("target-replicas=%d", tt.expectedReplicas)

			output := logger.OutputWriter.String()
			if !strings.Contains(output, expectedMsg) {
				t.Errorf("expected replica count %d in output, but not found. Output: %s", tt.expectedReplicas, output)
			}
		})
	}
}
