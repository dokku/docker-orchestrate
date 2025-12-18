package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

type DeployCommand struct {
	command.Meta

	file                  string
	projectDirectory      string
	projectName           string
	containerNameTemplate string
	replicas              int
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
	f.StringVar(&c.file, "file", "", "the path to the Compose file")
	f.StringVar(&c.projectDirectory, "project-directory", "", "the path to the project directory")
	f.StringVar(&c.projectName, "project-name", "", "the name of the project")
	f.StringVar(&c.containerNameTemplate, "container-name-template", "{{.ProjectName}}-{{.ServiceName}}-{{.InstanceID}}", "the template for the container name")
	f.IntVar(&c.replicas, "replicas", 0, "the number of replicas to deploy")
	return f
}

func (c *DeployCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--file":                    complete.PredictFiles("*"),
			"--project-directory":       complete.PredictDirs("*"),
			"--project-name":            complete.PredictAnything,
			"--container-name-template": complete.PredictAnything,
			"--replicas":                complete.PredictAnything,
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

	if c.file == "" {
		c.file, err = internal.ComposeFile()
		if err != nil {
			c.Ui.Error(err.Error())
			return 1
		}
	}

	if c.projectDirectory == "" {
		c.projectDirectory = filepath.Dir(c.file)
	}

	if c.projectName == "" {
		c.projectName = filepath.Base(filepath.Dir(c.file))
	}

	project, err := internal.ComposeProject(c.file)
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		if c.replicas > 0 {
			c.Ui.Error("--replicas flag requires a service name argument")
			return 1
		}

		c.Ui.Output(fmt.Sprintf("Deploying entire project from %s", c.file))
		err = internal.DeployProject(internal.DeployProjectInput{
			ComposeFile:           c.file,
			ContainerNameTemplate: c.containerNameTemplate,
			Logger:                c.Ui,
			Project:               project,
			ProjectName:           c.projectName,
		})
		if err != nil {
			c.Ui.Error(err.Error())
			return 1
		}
		return 0
	}

	c.Ui.Output(fmt.Sprintf("Deploying service %s", serviceName))
	err = internal.DeployService(internal.DeployServiceInput{
		ComposeFile:           c.file,
		ContainerNameTemplate: c.containerNameTemplate,
		Logger:                c.Ui,
		Project:               project,
		ProjectName:           c.projectName,
		Replicas:              c.replicas,
		ServiceName:           serviceName,
	})
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	return 0
}
