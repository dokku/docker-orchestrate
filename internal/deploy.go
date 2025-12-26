package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/compose"
	"github.com/josegonzalez/cli-skeleton/command"
	parser "github.com/novln/docker-parser"
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
	Logger *command.ZerologUi
	// Profiles is the profiles to enable
	Profiles []string
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
	// SkipDatabases is whether to skip deploying databases
	SkipDatabases bool
}

// DeployProject deploys a project
func DeployProject(ctx context.Context, input DeployProjectInput) error {
	// deploy each service in the project
	// start with the web service if it exists, and then process everything else in dependency order
	// if the web service has dependencies, skip it and deploy all services in dependency order
	skipWeb := true
	for _, service := range input.Project.Services {
		if service.Name == "web" {
			if len(service.DependsOn) > 0 {
				skipWeb = false
				continue
			}

			input.Logger.LogHeader2(fmt.Sprintf("Deploying service %s", service.Name))
			err := DeployService(ctx, DeployServiceInput{
				Client:                input.Client,
				ComposeFile:           input.ComposeFile,
				ContainerNameTemplate: input.ContainerNameTemplate,
				Executor:              input.Executor,
				Logger:                input.Logger,
				Project:               input.Project,
				ProjectName:           input.ProjectName,
				ServiceName:           service.Name,
				SkipDatabases:         input.SkipDatabases,
			})
			if err != nil {
				return err
			}
		}
	}

	dependencyOrder := []string{}
	err := compose.InDependencyOrder(ctx, input.Project, func(c context.Context, name string) error {
		if name == "web" && skipWeb {
			return nil
		}

		service, err := input.Project.GetService(name)
		if err != nil {
			return err
		}
		dependencyOrder = append(dependencyOrder, service.Name)
		return nil
	})
	if err != nil {
		return err
	}

	for _, serviceName := range dependencyOrder {
		input.Logger.LogHeader2(fmt.Sprintf("Deploying service %s", serviceName))
		err = DeployService(ctx, DeployServiceInput{
			Client:                input.Client,
			ComposeFile:           input.ComposeFile,
			ContainerNameTemplate: input.ContainerNameTemplate,
			Executor:              input.Executor,
			Logger:                input.Logger,
			Project:               input.Project,
			ProjectName:           input.ProjectName,
			ServiceName:           serviceName,
			SkipDatabases:         input.SkipDatabases,
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
	Logger *command.ZerologUi
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
	// Replicas is the number of replicas to deploy
	Replicas int
	// ServiceName is the name of the service
	ServiceName string
	// SkipDatabases is whether to skip deploying databases
	SkipDatabases bool
}

// DeployService deploys a single service
func DeployService(ctx context.Context, input DeployServiceInput) error {
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

	if shouldSkipService(service, input.SkipDatabases, input.Logger) {
		return nil
	}

	// get the number of containers that should be running
	//   from the `input.Replicas` field if specified
	//   or the `service.[service-name].deploy.replicas` field in the compose file
	//   or the `service.[service-name].scale` field in the compose file
	//   or 1 if none of the above are specified
	var replicas int
	if input.Replicas > 0 {
		replicas = input.Replicas
	}

	if replicas == 0 {
		if service.Deploy != nil && service.Deploy.Replicas != nil {
			replicas = int(*service.Deploy.Replicas)
		} else if service.Scale != nil {
			replicas = int(*service.Scale)
		} else {
			replicas = 1
		}
	}

	// Get update_config settings
	var updateConfig *types.UpdateConfig
	if service.Deploy != nil && service.Deploy.UpdateConfig != nil {
		updateConfig = service.Deploy.UpdateConfig
	}
	if updateConfig == nil {
		// Default update config if not specified
		parallelismVal := uint64(1)
		updateConfig = &types.UpdateConfig{
			Parallelism:   &parallelismVal,
			Delay:         types.Duration(10 * time.Second),
			FailureAction: "pause",
			Monitor:       types.Duration(5 * time.Second),
			Order:         "start-first",
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

	healthcheckHostCommand := ""
	preStopHostCommand := ""
	postStopHostCommand := ""
	if updateConfig.Extensions != nil {
		if cmd, ok := updateConfig.Extensions["x-healthcheck-host-command"].(string); ok {
			healthcheckHostCommand = cmd
		}
		if cmd, ok := updateConfig.Extensions["x-pre-stop-host-command"].(string); ok {
			preStopHostCommand = cmd
		}
		if cmd, ok := updateConfig.Extensions["x-post-stop-host-command"].(string); ok {
			postStopHostCommand = cmd
		}
	}

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
			Client:              input.Client,
			ComposeFile:         input.ComposeFile,
			CurrentContainers:   currentContainers,
			CurrentReplicas:     len(currentContainers),
			DesiredReplicas:     replicas,
			Executor:            executor,
			Logger:              input.Logger,
			PostStopHostCommand: postStopHostCommand,
			PreStopHostCommand:  preStopHostCommand,
			ProjectName:         input.ProjectName,
			ServiceName:         input.ServiceName,
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
			Client:              input.Client,
			ComposeFile:         input.ComposeFile,
			ContainersToUpdate:  containersToUpdate,
			CurrentReplicas:     len(containersToUpdate),
			Delay:               delay,
			DesiredReplicas:     replicas,
			Executor:            executor,
			FailureAction:       updateConfig.FailureAction,
			HealthcheckCommand:  healthcheckHostCommand,
			Logger:              input.Logger,
			MaxFailureRatio:     maxFailureRatio,
			Monitor:             monitor,
			Order:               order,
			Parallelism:         parallelism,
			PostStopHostCommand: postStopHostCommand,
			PreStopHostCommand:  preStopHostCommand,
			ProjectDir:          projectDir,
			ProjectName:         input.ProjectName,
			ServiceName:         input.ServiceName,
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
			Client:              input.Client,
			ComposeFile:         input.ComposeFile,
			CurrentReplicas:     len(updatedContainers),
			Delay:               delay,
			DesiredReplicas:     replicas,
			Executor:            executor,
			ExistingContainers:  updatedContainers,
			FailureAction:       string(updateConfig.FailureAction),
			HealthcheckCommand:  healthcheckHostCommand,
			Logger:              input.Logger,
			MaxFailureRatio:     maxFailureRatio,
			Monitor:             monitor,
			Parallelism:         parallelism,
			PostStopHostCommand: postStopHostCommand,
			PreStopHostCommand:  preStopHostCommand,
			ProjectDir:          projectDir,
			ProjectName:         input.ProjectName,
			ServiceName:         input.ServiceName,
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

	input.Logger.Info(fmt.Sprintf("Deployment complete: service=%s, expected=%d, actual=%d failures=%d", input.ServiceName, replicas, len(finalContainers), rollingUpdateOutput.Failures))
	return nil
}

func shouldSkipService(service *types.ServiceConfig, shouldSkipDatabases bool, logger *command.ZerologUi) bool {
	if shouldSkipDatabases && isDatabaseService(service, logger) {
		return true
	}
	return false
}

func isDatabaseService(service *types.ServiceConfig, logger *command.ZerologUi) bool {
	databaseImageRepositories := []string{
		"clickhouse/clickhouse-server",
		"library/couchdb",
		"library/elasticsearch",
		"dokku/docker-grafana-graphite",
		"library/mariadb",
		"getmeili/meilisearch",
		"library/memcached",
		"library/mongo",
		"library/mysql",
		"library/nats",
		"omnisci/core-os-cpu",
		"library/postgres",
		"fanout/pushpin",
		"library/rabbitmq",
		"library/redis",
		"library/rethinkdb",
		"library/solr",
		"typesense/typesense",
	}
	parsedImage, err := parser.Parse(service.Image)
	if err != nil {
		logger.Error(fmt.Sprintf("error parsing image %s: %v", service.Image, err))
		return false
	}

	for _, databaseImageRepository := range databaseImageRepositories {
		if parsedImage.ShortName() == databaseImageRepository {
			logger.Info(fmt.Sprintf("Skipping detected database service: image=%s", strings.TrimPrefix(parsedImage.ShortName(), "library/")))
			return true
		}
	}

	return false
}
