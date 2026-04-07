package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

type DeployCommand struct {
	command.Meta

	build                 bool
	containerNameTemplate string
	envFiles              []string
	files                 []string
	force                 bool
	profiles              []string
	projectDirectory      string
	projectName           string
	pull                  string
	replicas              int
	skipDatabases         bool
}

func (c *DeployCommand) Name() string {
	return "deploy"
}

func (c *DeployCommand) Synopsis() string {
	return "Deploy a Compose project"
}

func (c *DeployCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *DeployCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Deploy the entire Compose project": fmt.Sprintf("%s %s", appName, c.Name()),
		"Deploy a specific service":         fmt.Sprintf("%s %s web", appName, c.Name()),
	}
}

func (c *DeployCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to deploy",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

func (c *DeployCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *DeployCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *DeployCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.BoolVar(&c.build, "build", false, "build images before deploying")
	f.BoolVar(&c.force, "force", false, "force deploy even if service config is unchanged")
	f.IntVar(&c.replicas, "replicas", 0, "the number of replicas to deploy")
	f.StringSliceVar(&c.profiles, "profile", []string{}, "one or more profiles to enable")
	f.StringVar(&c.containerNameTemplate, "container-name-template", "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}", "the template for the container name")
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringVar(&c.projectDirectory, "project-directory", "", "the path to the project directory")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	f.StringVar(&c.pull, "pull", "", "pull image policy (always, missing, never)")
	f.BoolVar(&c.skipDatabases, "skip-databases", false, "whether to skip deploying databases")
	return f
}

func (c *DeployCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--build":                   complete.PredictNothing,
			"--container-name-template": complete.PredictAnything,
			"--force":                   complete.PredictNothing,
			"--env-file":                complete.PredictFiles("*"),
			"--file":                    complete.PredictFiles("*"),
			"--profiles":                complete.PredictAnything,
			"--project-directory":       complete.PredictDirs("*"),
			"--project-name":            complete.PredictAnything,
			"--pull":                    complete.PredictSet("always", "missing", "never"),
			"--replicas":                complete.PredictAnything,
			"--skip-databases":          complete.PredictNothing,
		},
	)
}

func (c *DeployCommand) Run(args []string) int {
	flags := c.FlagSet()
	flags.Usage = func() { c.Ui.Output(c.Help()) }
	if err := flags.Parse(args); err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	validPullPolicies := []string{"always", "missing", "never"}
	if c.pull != "" && !slices.Contains(validPullPolicies, c.pull) {
		c.Ui.Error(fmt.Sprintf("invalid --pull value %q: must be one of: always, missing, never", c.pull))
		return 1
	}

	if len(c.files) == 0 {
		detectedFile, detectErr := internal.ComposeFile()
		if detectErr != nil {
			c.Ui.Error(detectErr.Error())
			return 1
		}
		c.files = []string{detectedFile}
	}

	if c.projectDirectory == "" {
		c.projectDirectory = filepath.Dir(c.files[0])
	}

	if c.projectName == "" {
		c.projectName = filepath.Base(filepath.Dir(c.files[0]))
	}

	var envVars map[string]string
	if len(c.envFiles) > 0 {
		var envErr error
		envVars, envErr = dotenv.Read(c.envFiles...)
		if envErr != nil {
			c.Ui.Error(fmt.Sprintf("error reading env files: %v", envErr))
			return 1
		}
	}

	ctx := context.Background()
	project, err := internal.ComposeProject(ctx, c.projectName, c.files, c.profiles, c.envFiles)
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	client, err := internal.NewDockerClient()
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	logger, ok := c.Ui.(*command.ZerologUi)
	if !ok {
		c.Ui.Error("UI is not a ZerologUi")
		return 1
	}

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		if c.replicas > 0 {
			c.Ui.Error("--replicas flag requires a service name argument")
			return 1
		}

		logger.LogHeader1(fmt.Sprintf("Deploying entire project from %s", strings.Join(c.files, ", ")))
		err = internal.DeployProject(ctx, internal.DeployProjectInput{
			Build:                 c.build,
			Client:                client,
			ComposeFiles:          c.files,
			ContainerNameTemplate: c.containerNameTemplate,
			EnvFiles:              c.envFiles,
			EnvVars:               envVars,
			Force:                 c.force,
			Logger:                logger,
			Project:               project,
			ProjectName:           c.projectName,
			PullPolicy:            c.pull,
			SkipDatabases:         c.skipDatabases,
		})
		if err != nil {
			c.Ui.Error(err.Error())
			return 1
		}
		logger.Info("Entire project deployed")
		return 0
	}

	logger.LogHeader2(fmt.Sprintf("Deploying service %s", serviceName))
	err = internal.DeployService(ctx, internal.DeployServiceInput{
		Build:                 c.build,
		Client:                client,
		ComposeFiles:          c.files,
		ContainerNameTemplate: c.containerNameTemplate,
		EnvFiles:              c.envFiles,
		EnvVars:               envVars,
		Force:                 c.force,
		Logger:                logger,
		Project:               project,
		ProjectName:           c.projectName,
		PullPolicy:            c.pull,
		Replicas:              c.replicas,
		ServiceName:           serviceName,
		SkipDatabases:         c.skipDatabases,
	})
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	return 0
}
