package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/compose"
	"github.com/docker/docker/api/types/container"
	"github.com/josegonzalez/cli-skeleton/command"
	parser "github.com/novln/docker-parser"
)

// DeployProjectInput is the input for the DeployProject function
type DeployProjectInput struct {
	// Build is whether to build images before deploying
	Build bool
	// Client is the Docker client to use
	Client DockerClientInterface
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// ContainerNameTemplate is the Go template for container names
	ContainerNameTemplate string
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables from env files
	EnvVars map[string]string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
	// PullPolicy is the pull policy override from the CLI flag (always, missing, never)
	PullPolicy string
	// SkipDatabases is whether to skip deploying databases
	SkipDatabases bool
}

// DeployProject deploys a project
func DeployProject(ctx context.Context, input DeployProjectInput) error {
	orderedServices, err := OrderServices(ctx, input)
	if err != nil {
		return err
	}

	for _, serviceName := range orderedServices {
		input.Logger.LogHeader2(fmt.Sprintf("Deploying service %s", serviceName))
		err = DeployService(ctx, DeployServiceInput{
			Build:                 input.Build,
			Client:                input.Client,
			ComposeFiles:          input.ComposeFiles,
			ContainerNameTemplate: input.ContainerNameTemplate,
			EnvFiles:              input.EnvFiles,
			EnvVars:               input.EnvVars,
			Executor:              input.Executor,
			Logger:                input.Logger,
			Project:               input.Project,
			ProjectName:           input.ProjectName,
			PullPolicy:            input.PullPolicy,
			ServiceName:           serviceName,
			SkipDatabases:         input.SkipDatabases,
		})
		if err != nil {
			return err
		}
	}

	return RemoveMissingServices(ctx, input, orderedServices)
}

func RemoveMissingServices(ctx context.Context, input DeployProjectInput, orderedServices []string) error {
	// Query all containers with the project label
	allContainers, err := composeContainers(ctx, ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
	})
	if err != nil {
		return fmt.Errorf("error querying containers: %v", err)
	}

	// Extract service names from containers and log any that aren't in orderedServices
	servicesToRemove := map[string]bool{}
	for _, c := range allContainers {
		if c.Labels != nil {
			if serviceName, ok := c.Labels["com.docker.compose.service"]; ok {
				if slices.Contains(orderedServices, serviceName) {
					continue
				}
				servicesToRemove[serviceName] = true
			}
		}
	}

	for serviceName := range servicesToRemove {
		currentContainers, err := composeContainers(ctx, ComposeContainersInput{
			Client:      input.Client,
			ProjectName: input.ProjectName,
			ServiceName: serviceName,
			Status:      "running",
		})
		if err != nil {
			return fmt.Errorf("error getting current containers: %v", err)
		}

		input.Logger.LogHeader2(fmt.Sprintf("Removing service %s", serviceName))
		err = scaleDownContainers(ctx, ScaleDownContainersInput{
			Client:                      input.Client,
			ComposeFiles:                input.ComposeFiles,
			CurrentContainers:           currentContainers,
			CurrentReplicas:             len(currentContainers),
			DesiredReplicas:             0,
			EnvFiles:                    input.EnvFiles,
			EnvVars:                     input.EnvVars,
			Executor:                    input.Executor,
			Logger:                      input.Logger,
			PostStopHostCommand:         "",
			PostStopHostCommandDetached: false,
			PreStopCommand:              "",
			PreStopHooks:                nil,
			PreStopHostCommand:          "",
			PreStopHostCommandDetached:  false,
			ProjectName:                 input.ProjectName,
			ServiceName:                 serviceName,
			SkipDatabases:               input.SkipDatabases,
			StopTimeout:                 10,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// DeployServiceInput is the input for the DeployService function
type DeployServiceInput struct {
	// Build is whether to build images before deploying
	Build bool
	// Client is the Docker client to use
	Client DockerClientInterface
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// ContainerNameTemplate is the Go template for container names
	ContainerNameTemplate string
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// EnvVars is the parsed environment variables from env files
	EnvVars map[string]string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Project is the project configuration
	Project *types.Project
	// ProjectName is the name of the project
	ProjectName string
	// PullPolicy is the pull policy override from the CLI flag (always, missing, never)
	PullPolicy string
	// Replicas is the number of replicas to deploy
	Replicas int
	// ServiceName is the name of the service
	ServiceName string
	// SkipDatabases is whether to skip deploying databases
	SkipDatabases bool
}

// DeployService deploys a single service
func DeployService(ctx context.Context, input DeployServiceInput) error {
	if len(input.ComposeFiles) == 0 {
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

	skipService := shouldSkipService(ShouldSkipServiceInput{
		Service:             service,
		ShouldSkipDatabases: input.SkipDatabases,
		Logger:              input.Logger,
	})
	if skipService {
		return nil
	}

	// Detect one-shot service and branch to dedicated handler
	if isOneShotService(service) {
		return deployOneShotService(ctx, DeployOneShotServiceInput{
			Build:        input.Build,
			ComposeFiles: input.ComposeFiles,
			EnvFiles:     input.EnvFiles,
			Executor:     input.Executor,
			Logger:       input.Logger,
			ProjectName:  input.ProjectName,
			PullPolicy:   input.PullPolicy,
			Service:      service,
			ServiceName:  input.ServiceName,
		})
	}

	pullPolicy, buildImage, err := ResolvePullPolicy(input.PullPolicy, input.Build, service)
	if err != nil {
		return err
	}

	replicas := ServiceReplicas(input, service)

	// Compute stop timeout from stop_grace_period, defaulting to 10 seconds
	stopTimeout := 10
	if service.StopGracePeriod != nil {
		stopTimeout = int(time.Duration(*service.StopGracePeriod).Seconds())
		if stopTimeout <= 0 {
			stopTimeout = 10
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
	healthcheckWait := time.Duration(0)
	waitAfterHealthy := time.Duration(0)
	preStopHostCommand := ""
	preStopCommand := ""
	postStopHostCommand := ""
	preStopHostCommandDetached := false
	postStopHostCommandDetached := false
	if updateConfig.Extensions != nil {
		if cmd, ok := updateConfig.Extensions["x-healthcheck-host-command"].(string); ok {
			healthcheckHostCommand = cmd
		}
		if waitStr, ok := updateConfig.Extensions["x-healthcheck-wait"].(string); ok {
			parsed, err := time.ParseDuration(waitStr)
			if err != nil {
				return fmt.Errorf("invalid x-healthcheck-wait duration %q: %v", waitStr, err)
			}
			healthcheckWait = parsed
		}
		if waitStr, ok := updateConfig.Extensions["x-wait-after-healthy"].(string); ok {
			parsed, err := time.ParseDuration(waitStr)
			if err != nil {
				return fmt.Errorf("invalid x-wait-after-healthy duration %q: %v", waitStr, err)
			}
			waitAfterHealthy = parsed
		}
		if cmd, ok := updateConfig.Extensions["x-pre-stop-host-command"].(string); ok {
			preStopHostCommand = cmd
		}
		if cmd, ok := updateConfig.Extensions["x-pre-stop-command"].(string); ok {
			preStopCommand = cmd
		}
		if cmd, ok := updateConfig.Extensions["x-post-stop-host-command"].(string); ok {
			postStopHostCommand = cmd
		}
		if detached, err := parseDetachedFlag(updateConfig.Extensions, "x-pre-stop-host-command-detached"); err != nil {
			return err
		} else {
			preStopHostCommandDetached = detached
		}
		if detached, err := parseDetachedFlag(updateConfig.Extensions, "x-post-stop-host-command-detached"); err != nil {
			return err
		} else {
			postStopHostCommandDetached = detached
		}
	}

	preStopHooks := service.PreStop
	postStartHooks := service.PostStart

	projectDir := filepath.Dir(input.ComposeFiles[0])

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Clean up non-running containers before deploy
	allContainers, err := composeContainers(ctx, ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("error getting all containers for cleanup: %v", err)
	}

	for _, c := range allContainers {
		if c.State == "running" {
			continue
		}

		containerName := c.ID
		if len(containerName) > 12 {
			containerName = containerName[:12]
		}
		if len(c.Names) > 0 {
			containerName = strings.TrimPrefix(c.Names[0], "/")
		}

		input.Logger.LogHeader2(fmt.Sprintf("Removing non-running container %s (state=%s)", containerName, c.State))
		if err := input.Client.ContainerRemove(ctx, c.ID, container.RemoveOptions{}); err != nil {
			input.Logger.Warn(fmt.Sprintf("Failed to remove container %s: %v", containerName, err))
		}
	}

	// Rename running containers to temporary names to avoid conflicts with docker compose
	// naming during rolling updates and scale operations. The final renameContainersToConvention
	// at the end of deploy assigns the proper names.
	runningForRename, err := composeContainers(ctx, ComposeContainersInput{
		Client:      input.Client,
		ProjectName: input.ProjectName,
		ServiceName: input.ServiceName,
		Status:      "running",
	})
	if err != nil {
		return fmt.Errorf("error getting running containers for rename: %v", err)
	}

	for _, c := range runningForRename {
		tmpName := c.ID[:12] + "-tmp"
		if err := input.Client.ContainerRename(ctx, c.ID, tmpName); err != nil {
			input.Logger.Warn(fmt.Sprintf("Failed to rename container %s to temp name: %v", c.ID[:12], err))
		}
	}

	// Pre-build image when build flag is set
	if buildImage {
		input.Logger.Info(fmt.Sprintf("Building image for service %s", input.ServiceName))
		buildArgs := []string{"compose"}
		buildArgs = append(buildArgs, composeFileArgs(input.ComposeFiles)...)
		buildArgs = append(buildArgs, envFileArgs(input.EnvFiles)...)
		buildArgs = append(buildArgs, "-p", input.ProjectName, "build", input.ServiceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             buildArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error building image for service %s: %v", input.ServiceName, err)
		}
	}

	// Pre-pull image when pull policy is "always" and not building
	if pullPolicy == "always" && !buildImage {
		input.Logger.Info(fmt.Sprintf("Pulling image for service %s", input.ServiceName))
		pullArgs := []string{"compose"}
		pullArgs = append(pullArgs, composeFileArgs(input.ComposeFiles)...)
		pullArgs = append(pullArgs, envFileArgs(input.EnvFiles)...)
		pullArgs = append(pullArgs, "-p", input.ProjectName, "pull", input.ServiceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             pullArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error pulling image for service %s: %v", input.ServiceName, err)
		}
	}

	// Get current running containers
	currentContainers, err := composeContainers(ctx, ComposeContainersInput{
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
			Client:                      input.Client,
			ComposeFiles:                input.ComposeFiles,
			CurrentContainers:           currentContainers,
			CurrentReplicas:             len(currentContainers),
			DesiredReplicas:             replicas,
			EnvFiles:                    input.EnvFiles,
			EnvVars:                     input.EnvVars,
			Executor:                    executor,
			Logger:                      input.Logger,
			PostStopHostCommand:         postStopHostCommand,
			PostStopHostCommandDetached: postStopHostCommandDetached,
			PreStopCommand:              preStopCommand,
			PreStopHooks:                preStopHooks,
			PreStopHostCommand:          preStopHostCommand,
			PreStopHostCommandDetached:  preStopHostCommandDetached,
			ProjectName:                 input.ProjectName,
			ServiceName:                 input.ServiceName,
			StopTimeout:                 stopTimeout,
		})
		if err != nil {
			return err
		}
	}

	// refresh the current containers
	containersToUpdate, err := composeContainers(ctx, ComposeContainersInput{
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
			Build:                       buildImage,
			Client:                      input.Client,
			ComposeFiles:                input.ComposeFiles,
			ContainersToUpdate:          containersToUpdate,
			CurrentReplicas:             len(containersToUpdate),
			Delay:                       delay,
			DesiredReplicas:             replicas,
			EnvFiles:                    input.EnvFiles,
			EnvVars:                     input.EnvVars,
			Executor:                    executor,
			FailureAction:               updateConfig.FailureAction,
			HealthcheckCommand:          healthcheckHostCommand,
			HealthcheckWait:             healthcheckWait,
			WaitAfterHealthy:            waitAfterHealthy,
			Logger:                      input.Logger,
			MaxFailureRatio:             maxFailureRatio,
			Monitor:                     monitor,
			Order:                       order,
			Parallelism:                 parallelism,
			PostStopHostCommand:         postStopHostCommand,
			PostStopHostCommandDetached: postStopHostCommandDetached,
			PreStopCommand:              preStopCommand,
			PreStopHooks:                preStopHooks,
			PreStopHostCommand:          preStopHostCommand,
			PreStopHostCommandDetached:  preStopHostCommandDetached,
			ProjectDir:                  projectDir,
			ProjectName:                 input.ProjectName,
			PullPolicy:                  pullPolicy,
			ServiceName:                 input.ServiceName,
			StopTimeout:                 stopTimeout,
		})
		if err != nil {
			return fmt.Errorf("error rolling update containers: %v", err)
		}
	}

	// Get updated container count after rolling update
	updatedContainers, err := composeContainers(ctx, ComposeContainersInput{
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
			Build:                       buildImage,
			Client:                      input.Client,
			ComposeFiles:                input.ComposeFiles,
			CurrentReplicas:             len(updatedContainers),
			Delay:                       delay,
			DesiredReplicas:             replicas,
			EnvFiles:                    input.EnvFiles,
			EnvVars:                     input.EnvVars,
			Executor:                    executor,
			ExistingContainers:          updatedContainers,
			FailureAction:               string(updateConfig.FailureAction),
			HealthcheckCommand:          healthcheckHostCommand,
			HealthcheckWait:             healthcheckWait,
			WaitAfterHealthy:            waitAfterHealthy,
			Logger:                      input.Logger,
			MaxFailureRatio:             maxFailureRatio,
			Monitor:                     monitor,
			Parallelism:                 parallelism,
			PostStartHooks:              postStartHooks,
			PostStopHostCommand:         postStopHostCommand,
			PostStopHostCommandDetached: postStopHostCommandDetached,
			PreStopCommand:              preStopCommand,
			PreStopHooks:                preStopHooks,
			PreStopHostCommand:          preStopHostCommand,
			PreStopHostCommandDetached:  preStopHostCommandDetached,
			PullPolicy:                  pullPolicy,
			ProjectDir:                  projectDir,
			ProjectName:                 input.ProjectName,
			ServiceName:                 input.ServiceName,
			StopTimeout:                 stopTimeout,
		})
		if err != nil {
			return err
		}
	}

	// Get final container count
	finalContainers, err := composeContainers(ctx, ComposeContainersInput{
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

// OrderServices orders the services in the project in dependency order
// deploy each service in the project
// 1. one-shot services (restart: "no") with no dependencies are deployed first
// 2. then the web service if it exists and has no dependencies
// 3. then everything else in dependency order
// if the web service has dependencies, skip it and deploy all services in dependency order
func OrderServices(ctx context.Context, input DeployProjectInput) ([]string, error) {
	// Collect one-shot services with no dependencies
	oneShotNoDeps := []string{}
	for _, service := range input.Project.Services {
		if isOneShotService(&service) && len(service.DependsOn) == 0 {
			oneShotNoDeps = append(oneShotNoDeps, service.Name)
		}
	}

	hasWeb := false
	skipWeb := true
	for _, service := range input.Project.Services {
		if service.Name == "web" {
			hasWeb = true
			if len(service.DependsOn) > 0 {
				skipWeb = false
				continue
			}
			break
		}
	}

	dependencyOrder := []string{}
	// Add one-shot services without dependencies first
	dependencyOrder = append(dependencyOrder, oneShotNoDeps...)

	// Add web next if it has no dependencies and wasn't already added as a one-shot
	if hasWeb && skipWeb && !slices.Contains(oneShotNoDeps, "web") {
		dependencyOrder = append(dependencyOrder, "web")
	}

	var mu sync.Mutex
	err := compose.InDependencyOrder(ctx, input.Project, func(c context.Context, name string) error {
		// Skip services already added
		if slices.Contains(dependencyOrder, name) {
			return nil
		}

		service, err := input.Project.GetService(name)
		if err != nil {
			return err
		}
		mu.Lock()
		dependencyOrder = append(dependencyOrder, service.Name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}

	return dependencyOrder, nil
}

// parseDetachedFlag parses and validates a detached flag from extensions
// Returns true if the value is boolean true, false if boolean false or not set, and an error for invalid values
func parseDetachedFlag(extensions map[string]interface{}, key string) (bool, error) {
	val, ok := extensions[key]
	if !ok {
		return false, nil
	}

	boolVal, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean (got %T)", key, val)
	}

	return boolVal, nil
}

// ShouldSkipScaleDownServiceInput is the input for the shouldSkipScaleDownService function
type ShouldSkipScaleDownServiceInput struct {
	// Container is the container to check
	Container container.Summary
	// ServiceName is the name of the service
	ServiceName string
	// ShouldSkipDatabases is whether to skip interacting with databases
	ShouldSkipDatabases bool
	// Logger is the logger to use
	Logger *command.ZerologUi
}

// shouldSkipScaleDownService returns true if the service should be skipped
func shouldSkipScaleDownService(input ShouldSkipScaleDownServiceInput) bool {
	if input.Container.Labels != nil {
		if skipValue, ok := input.Container.Labels["com.dokku.orchestrate/skip"]; ok && skipValue == "true" {
			input.Logger.Info(fmt.Sprintf("Skipping service with skip label: service=%s", input.ServiceName))
			return true
		}
	}

	if input.ShouldSkipDatabases && isDatabaseService(input.Container.Image, input.Logger) {
		return true
	}
	return false
}

// ShouldSkipServiceInput is the input for the shouldSkipService function
type ShouldSkipServiceInput struct {
	// Logger is the logger to use
	Logger *command.ZerologUi
	// Service is the service configuration
	Service *types.ServiceConfig
	// ShouldSkipDatabases is whether to skip deploying databases
	ShouldSkipDatabases bool
	// SilenceLogging is whether to silence logging
	SilenceLogging bool
}

// shouldSkipService returns true if the service should be skipped
func shouldSkipService(input ShouldSkipServiceInput) bool {
	// skip model services
	if len(input.Service.Models) > 0 {
		if !input.SilenceLogging {
			input.Logger.Info(fmt.Sprintf("Skipping model service: service=%s", input.Service.Name))
		}
		return true
	}

	// skip provider services
	if input.Service.Provider != nil {
		if !input.SilenceLogging {
			input.Logger.Info(fmt.Sprintf("Skipping provider service: service=%s", input.Service.Name))
		}
		return true
	}

	// Check for skip label
	if input.Service.Labels != nil {
		if skipValue, ok := input.Service.Labels["com.dokku.orchestrate/skip"]; ok && skipValue == "true" {
			if !input.SilenceLogging {
				input.Logger.Info(fmt.Sprintf("Skipping service with skip label: service=%s", input.Service.Name))
			}
			return true
		}
	}

	if input.ShouldSkipDatabases && isDatabaseService(input.Service.Image, input.Logger) {
		return true
	}
	return false
}

// isDatabaseService returns true if the service is a database service
func isDatabaseService(serviceImage string, logger *command.ZerologUi) bool {
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
	parsedImage, err := parser.Parse(serviceImage)
	if err != nil {
		logger.Error(fmt.Sprintf("error parsing image %s: %v", serviceImage, err))
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

// ResolvePullPolicy resolves the effective pull policy and build flag from CLI flags and compose spec.
// CLI flags take precedence. If neither pull policy is set, defaults to "missing".
// Compose spec "if_not_present" is mapped to "missing".
// Compose spec "build" sets pull to "never" and build to true (requires a build section).
// The --build CLI flag enables building for services that have a build section.
func ResolvePullPolicy(cliPullPolicy string, cliBuild bool, service *types.ServiceConfig) (string, bool, error) {
	build := false

	// Resolve build flag
	if service.PullPolicy == types.PullPolicyBuild {
		if service.Build == nil {
			return "", false, fmt.Errorf("pull_policy 'build' requires a build section in service %s", service.Name)
		}
		build = true
	} else if cliBuild && service.Build != nil {
		build = true
	}

	// Resolve pull policy
	if cliPullPolicy != "" {
		return cliPullPolicy, build, nil
	}

	switch service.PullPolicy {
	case "":
		return "missing", build, nil
	case types.PullPolicyAlways:
		return "always", build, nil
	case types.PullPolicyNever:
		return "never", build, nil
	case types.PullPolicyMissing:
		return "missing", build, nil
	case types.PullPolicyIfNotPresent:
		return "missing", build, nil
	case types.PullPolicyBuild:
		return "never", build, nil
	case types.PullPolicyRefresh:
		return "", false, fmt.Errorf("pull_policy '%s' is not supported by docker-orchestrate", service.PullPolicy)
	default:
		return "", false, fmt.Errorf("pull_policy '%s' is not supported by docker-orchestrate", service.PullPolicy)
	}
}

// ServiceReplicas returns the number of containers that should be running
// get the number of containers that should be running
//
//	from the `input.Replicas` field if specified
//	or the `service.[service-name].deploy.replicas` field in the compose file
//	or the `service.[service-name].scale` field in the compose file
//	or 1 if none of the above are specified
func ServiceReplicas(input DeployServiceInput, service *types.ServiceConfig) int {
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
	return replicas
}

// isOneShotService returns true if the service is a one-shot service (restart: "no")
func isOneShotService(service *types.ServiceConfig) bool {
	return service.Restart == "no"
}

// DeployOneShotServiceInput is the input for the deployOneShotService function
type DeployOneShotServiceInput struct {
	// Build is whether to build images before running
	Build bool
	// ComposeFiles is the list of paths to the compose files
	ComposeFiles []string
	// EnvFiles is the list of paths to the env files
	EnvFiles []string
	// Executor is the command executor to use
	Executor CommandExecutor
	// Logger is the logger to use
	Logger *command.ZerologUi
	// ProjectName is the name of the project
	ProjectName string
	// PullPolicy is the pull policy override from the CLI flag
	PullPolicy string
	// Service is the service configuration
	Service *types.ServiceConfig
	// ServiceName is the name of the service
	ServiceName string
}

// deployOneShotService runs a one-shot service (restart: "no") to completion.
// It runs `docker compose run --rm --no-deps <service>`, blocks until exit,
// and returns an error on non-zero exit code.
func deployOneShotService(ctx context.Context, input DeployOneShotServiceInput) error {
	pullPolicy, buildImage, err := ResolvePullPolicy(input.PullPolicy, input.Build, input.Service)
	if err != nil {
		return err
	}

	projectDir := filepath.Dir(input.ComposeFiles[0])

	executor := input.Executor
	if executor == nil {
		executor = ExecCommand
	}

	// Pre-build image when build flag is set
	if buildImage {
		input.Logger.Info(fmt.Sprintf("Building image for one-shot service %s", input.ServiceName))
		buildArgs := []string{"compose"}
		buildArgs = append(buildArgs, composeFileArgs(input.ComposeFiles)...)
		buildArgs = append(buildArgs, envFileArgs(input.EnvFiles)...)
		buildArgs = append(buildArgs, "-p", input.ProjectName, "build", input.ServiceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             buildArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error building image for one-shot service %s: %v", input.ServiceName, err)
		}
	}

	// Pre-pull image when pull policy is "always" and not building
	if pullPolicy == "always" && !buildImage {
		input.Logger.Info(fmt.Sprintf("Pulling image for one-shot service %s", input.ServiceName))
		pullArgs := []string{"compose"}
		pullArgs = append(pullArgs, composeFileArgs(input.ComposeFiles)...)
		pullArgs = append(pullArgs, envFileArgs(input.EnvFiles)...)
		pullArgs = append(pullArgs, "-p", input.ProjectName, "pull", input.ServiceName)
		_, err = executor(ctx, ExecCommandInput{
			Command:          "docker",
			Args:             pullArgs,
			WorkingDirectory: projectDir,
		})
		if err != nil {
			return fmt.Errorf("error pulling image for one-shot service %s: %v", input.ServiceName, err)
		}
	}

	// Run the one-shot service
	input.Logger.Info(fmt.Sprintf("Running one-shot service %s", input.ServiceName))
	runArgs := []string{"compose"}
	runArgs = append(runArgs, composeFileArgs(input.ComposeFiles)...)
	runArgs = append(runArgs, envFileArgs(input.EnvFiles)...)
	runArgs = append(runArgs, "-p", input.ProjectName, "run", "--rm", "--no-deps", input.ServiceName)
	resp, err := executor(ctx, ExecCommandInput{
		Command:          "docker",
		Args:             runArgs,
		WorkingDirectory: projectDir,
	})
	if err != nil {
		return fmt.Errorf("one-shot service %s failed (exit code %d): %v", input.ServiceName, resp.ExitCode, err)
	}

	input.Logger.Info(fmt.Sprintf("One-shot service %s completed successfully", input.ServiceName))
	return nil
}
