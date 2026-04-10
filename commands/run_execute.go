package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// RunExecuteCommand runs a one-shot service on demand
type RunExecuteCommand struct {
	command.Meta

	build            bool
	detach           bool
	envFiles         []string
	files            []string
	profiles         []string
	projectDirectory string
	projectName      string
	pull             string
}

func (c *RunExecuteCommand) Name() string {
	return "run execute"
}

func (c *RunExecuteCommand) Synopsis() string {
	return "Run a one-shot service on demand"
}

func (c *RunExecuteCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *RunExecuteCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Run a one-shot service":            fmt.Sprintf("%s %s migrate", appName, c.Name()),
		"Run a service in detached mode":    fmt.Sprintf("%s %s --detach export", appName, c.Name()),
		"Build images before running":       fmt.Sprintf("%s %s --build seed", appName, c.Name()),
		"Run with a specific compose file":  fmt.Sprintf("%s %s -f docker-compose.prod.yaml -p myproject migrate", appName, c.Name()),
	}
}

func (c *RunExecuteCommand) Arguments() []command.Argument {
	return []command.Argument{
		{
			Name:        "service-name",
			Description: "the name of the one-shot service to run",
			Optional:    false,
			Type:        command.ArgumentString,
		},
	}
}

func (c *RunExecuteCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *RunExecuteCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *RunExecuteCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.BoolVar(&c.build, "build", false, "build images before running")
	f.BoolVar(&c.detach, "detach", false, "run the service in the background")
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringSliceVar(&c.profiles, "profile", []string{}, "one or more profiles to enable")
	f.StringVar(&c.projectDirectory, "project-directory", "", "the path to the project directory")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	f.StringVar(&c.pull, "pull", "", "pull image policy (always, missing, never)")
	return f
}

func (c *RunExecuteCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--build":             complete.PredictNothing,
			"--detach":            complete.PredictNothing,
			"--env-file":          complete.PredictFiles("*"),
			"--file":              complete.PredictFiles("*"),
			"--profile":           complete.PredictAnything,
			"--project-directory": complete.PredictDirs("*"),
			"--project-name":      complete.PredictAnything,
			"--pull":              complete.PredictSet("always", "missing", "never"),
		},
	)
}

func (c *RunExecuteCommand) Run(args []string) int {
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
	logger.LogHeader2(fmt.Sprintf("Running one-shot service %s", serviceName))
	err = internal.RunService(ctx, internal.RunServiceInput{
		Build:        c.build,
		Client:       client,
		ComposeFiles: c.files,
		Detach:       c.detach,
		EnvFiles:     c.envFiles,
		EnvVars:      envVars,
		Logger:       logger,
		Project:      project,
		ProjectName:  c.projectName,
		PullPolicy:   c.pull,
		ServiceName:  serviceName,
	})
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	if c.detach {
		logger.Info(fmt.Sprintf("Service %s started in background", serviceName))
	}

	return 0
}
