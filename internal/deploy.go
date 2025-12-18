package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/mitchellh/cli"
)

// DeployProjectInput is the input for the DeployProject function
type DeployProjectInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ComposeFile is the path to the compose file
	ComposeFile string
	// ContainerNameTemplate is the Go template for container names
	ContainerNameTemplate string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger cli.Ui
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
}

// DeployProject deploys a project
func DeployProject(input DeployProjectInput) error {
	// deploy each service in the project
	// start with the web service if it exists, and then process everything else in alphabetical order
	for _, service := range input.Project.Services {
		if service.Name == "web" {
			err := DeployService(DeployServiceInput{
				Client:                input.Client,
				ComposeFile:           input.ComposeFile,
				ContainerNameTemplate: input.ContainerNameTemplate,
				Executor:              input.Executor,
				Logger:                input.Logger,
				Project:               input.Project,
				ProjectName:           input.ProjectName,
				ServiceName:           service.Name,
			})
			if err != nil {
				return err
			}
		}
	}
	for _, service := range input.Project.Services {
		if service.Name == "web" {
			continue
		}

		err := DeployService(DeployServiceInput{
			Client:                input.Client,
			ComposeFile:           input.ComposeFile,
			ContainerNameTemplate: input.ContainerNameTemplate,
			Executor:              input.Executor,
			Logger:                input.Logger,
			Project:               input.Project,
			ProjectName:           input.ProjectName,
			ServiceName:           service.Name,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// DeployServiceInput is the input for the DeployService function
type DeployServiceInput struct {
	// Client is the Docker client to use
	Client DockerClientInterface
	// ComposeFile is the path to the compose file
	ComposeFile string
	// ContainerNameTemplate is the Go template for container names
	ContainerNameTemplate string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger cli.Ui
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
	// Replicas is the number of replicas to deploy
	Replicas int
	// ServiceName is the name of the service
	ServiceName string
}

// DeployService deploys a single service
func DeployService(input DeployServiceInput) error {
	if input.ComposeFile == "" {
		return fmt.Errorf("compose file is required")
	}

	if input.ProjectName == "" {
		return fmt.Errorf("project name is required")
	}

	if input.Project == nil {
		return fmt.Errorf("project is required")
	}

	if input.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}

	var service *types.ServiceConfig
	for _, s := range input.Project.Services {
		if s.Name == input.ServiceName {
			service = &s
			break
		}
	}
	if service == nil {
		return fmt.Errorf("service %s not found in compose file", input.ServiceName)
	}

	// get the number of containers that should be running
	//   from the `input.Replicas` field if specified
	//   or the `service.[service-name].deploy.replicas` field in the compose file
	//   or the `service.[service-name].scale` field in the compose file
	var replicas int
	if input.Replicas > 0 {
		replicas = input.Replicas
	}
	if replicas == 0 && service.Deploy.Replicas != nil {
		replicas = int(*service.Deploy.Replicas)
	}
	if replicas == 0 && service.Scale != nil {
		replicas = int(*service.Scale)
	}
	if replicas == 0 {
		return fmt.Errorf("service %s has no service.scale or service.deploy.replicas defined", input.ServiceName)
	}

	// Get update_config settings
	updateConfig := service.Deploy.UpdateConfig
	if updateConfig == nil {
		// Default update config if not specified
		parallelismVal := uint64(1)
		updateConfig = &types.UpdateConfig{
			Parallelism: &parallelismVal,
			Delay:       types.Duration(10 * time.Second),
			Monitor:     types.Duration(5 * time.Second),
		}
	}

	// Validate failure_action - only support "pause"
	if updateConfig.FailureAction != "" && updateConfig.FailureAction != "pause" {
		return fmt.Errorf("failure_action must be 'pause' (got: %s)", updateConfig.FailureAction)
	}

	// Get defaults
	parallelism := 1
	if updateConfig.Parallelism != nil {
		parallelism = int(*updateConfig.Parallelism)
	}
	delay := 0 * time.Second
	if updateConfig.Delay > 0 {
		delay = time.Duration(updateConfig.Delay)
	}
	monitor := 5 * time.Second
	if updateConfig.Monitor > 0 {
		monitor = time.Duration(updateConfig.Monitor)
	}
	maxFailureRatio := updateConfig.MaxFailureRatio
	order := "stop-first"
	if updateConfig.Order != "" {
		order = string(updateConfig.Order)
	}

	healthcheckCommand := ""
	if updateConfig.Extensions != nil {
		if cmd, ok := updateConfig.Extensions["x-healthcheck-command"].(string); ok {
			healthcheckCommand = cmd
		}
	}

	ctx := context.Background()
	projectDir := filepath.Dir(input.ComposeFile)

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Get current running containers
	currentContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting current containers: %v", err)
	}

	// Scale down if needed (before rolling update)
	if len(currentContainers) > replicas {
		err := scaleDownContainers(ctx, ScaleDownContainersInput{
			Client:            input.Client,
			ComposeFile:       input.ComposeFile,
			CurrentContainers: currentContainers,
			CurrentReplicas:   len(currentContainers),
			DesiredReplicas:   replicas,
			Logger:            input.Logger,
			ProjectName:       input.ProjectName,
			ServiceName:       input.ServiceName,
		})
		if err != nil {
			return err
		}
	}

	// refresh the current containers
	containersToUpdate, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting updated containers: %v", err)
	}

	// Perform rolling update on existing containers first
	if len(containersToUpdate) > replicas {
		// Only update up to the target replica count
		containersToUpdate = containersToUpdate[:replicas]
	}
	// sort containersToUpdate by oldest first
	sortContainersByCreationTime(containersToUpdate, false)

	var rollingUpdateOutput RollingUpdateOutput
	if len(containersToUpdate) > 0 {
		rollingUpdateOutput, err = rollingUpdateContainers(ctx, RollingUpdateInput{
			Client:             input.Client,
			ComposeFile:        input.ComposeFile,
			ContainersToUpdate: containersToUpdate,
			CurrentReplicas:    len(containersToUpdate),
			Delay:              delay,
			DesiredReplicas:    replicas,
			Executor:           executor,
			FailureAction:      updateConfig.FailureAction,
			HealthcheckCommand: healthcheckCommand,
			Logger:             input.Logger,
			MaxFailureRatio:    maxFailureRatio,
			Monitor:            monitor,
			Order:              order,
			Parallelism:        parallelism,
			ProjectDir:         projectDir,
			ProjectName:        input.ProjectName,
			ServiceName:        input.ServiceName,
		})
		if err != nil {
			return fmt.Errorf("error rolling update containers: %v", err)
		}
	}

	// Get updated container count after rolling update
	updatedContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting updated containers: %v", err)
	}

	// Scale up if needed (only after existing containers are replaced)
	if len(updatedContainers) < replicas {
		err := scaleUpContainers(ctx, ScaleUpContainersInput{
			Client:             input.Client,
			ComposeFile:        input.ComposeFile,
			CurrentReplicas:    len(updatedContainers),
			Delay:              delay,
			DesiredReplicas:    replicas,
			Executor:           executor,
			ExistingContainers: updatedContainers,
			FailureAction:      string(updateConfig.FailureAction),
			HealthcheckCommand: healthcheckCommand,
			Logger:             input.Logger,
			MaxFailureRatio:    maxFailureRatio,
			Monitor:            monitor,
			Parallelism:        parallelism,
			ProjectDir:         projectDir,
			ProjectName:        input.ProjectName,
			ServiceName:        input.ServiceName,
		})
		if err != nil {
			return err
		}
	}

	// Get final container count
	finalContainers, err := composeContainers(ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting final container count: %v", err)
	}

	// Rename all containers to follow the naming convention
	err = renameContainersToConvention(ctx, RenameContainersToConventionInput{
		Client:       input.Client,
		Containers:   finalContainers,
		ProjectName:  input.ProjectName,
		ServiceName:  input.ServiceName,
		NameTemplate: input.ContainerNameTemplate,
	})
	if err != nil {
		return fmt.Errorf("error renaming containers: %v", err)
	}

	input.Logger.Output(fmt.Sprintf("Deployment complete: expected=%d, actual=%d failures=%d", replicas, len(finalContainers), rollingUpdateOutput.Failures))
	return nil
}
