package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// RunListCommand lists one-shot services available to run
type RunListCommand struct {
	command.Meta

	envFiles         []string
	files            []string
	profiles         []string
	projectDirectory string
	projectName      string
}

func (c *RunListCommand) Name() string {
	return "run list"
}

func (c *RunListCommand) Synopsis() string {
	return "List one-shot services available to run"
}

func (c *RunListCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *RunListCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"List one-shot services": fmt.Sprintf("%s %s", appName, c.Name()),
		"List with a specific compose file": fmt.Sprintf("%s %s -f docker-compose.prod.yaml", appName, c.Name()),
	}
}

func (c *RunListCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *RunListCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *RunListCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *RunListCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringSliceVar(&c.profiles, "profile", []string{}, "one or more profiles to enable")
	f.StringVar(&c.projectDirectory, "project-directory", "", "the path to the project directory")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	return f
}

func (c *RunListCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--env-file":          complete.PredictFiles("*"),
			"--file":              complete.PredictFiles("*"),
			"--profile":           complete.PredictAnything,
			"--project-directory": complete.PredictDirs("*"),
			"--project-name":      complete.PredictAnything,
		},
	)
}

func (c *RunListCommand) Run(args []string) int {
	flags := c.FlagSet()
	flags.Usage = func() { c.Ui.Output(c.Help()) }
	if err := flags.Parse(args); err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
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

	ctx := context.Background()
	project, err := internal.ComposeProject(ctx, c.projectName, c.files, c.profiles, c.envFiles)
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	services := internal.ListOneShotServices(project)
	if len(services) == 0 {
		c.Ui.Output("No one-shot services found")
		return 0
	}

	for _, svc := range services {
		if svc.Command != "" {
			c.Ui.Output(fmt.Sprintf("%s\t%s", svc.Name, svc.Command))
		} else {
			c.Ui.Output(svc.Name)
		}
	}

	return 0
}
