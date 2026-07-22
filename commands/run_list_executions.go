package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// RunListExecutionsCommand lists past and running one-shot service executions
type RunListExecutionsCommand struct {
	command.Meta

	projectName string
}

func (c *RunListExecutionsCommand) Name() string {
	return "run list-executions"
}

func (c *RunListExecutionsCommand) Synopsis() string {
	return "List past and running one-shot service executions"
}

func (c *RunListExecutionsCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *RunListExecutionsCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"List all executions":           fmt.Sprintf("%s %s", appName, c.Name()),
		"List executions for a project": fmt.Sprintf("%s %s -p myproject", appName, c.Name()),
	}
}

func (c *RunListExecutionsCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *RunListExecutionsCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *RunListExecutionsCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *RunListExecutionsCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.StringVarP(&c.projectName, "project-name", "p", "", "filter executions by project name")
	return f
}

func (c *RunListExecutionsCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--project-name": complete.PredictAnything,
		},
	)
}

func (c *RunListExecutionsCommand) Run(args []string) int {
	flags := c.FlagSet()
	flags.Usage = func() { c.Ui.Output(c.Help()) }
	if err := flags.Parse(args); err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	ctx := context.Background()
	client, err := internal.NewDockerClient()
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	defer client.Close()

	executions, err := internal.ListRunExecutions(ctx, internal.ListRunExecutionsInput{
		Client:      client,
		ProjectName: c.projectName,
	})
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	if len(executions) == 0 {
		c.Ui.Output("No executions found")
		return 0
	}

	c.Ui.Output(fmt.Sprintf("%-50s %-20s %-10s %-10s %s", "CONTAINER", "SERVICE", "STATUS", "EXIT CODE", "TRIGGERED AT"))
	for _, exec := range executions {
		exitCode := "-"
		if exec.Status == "exited" {
			exitCode = fmt.Sprintf("%d", exec.ExitCode)
		}
		c.Ui.Output(fmt.Sprintf("%-50s %-20s %-10s %-10s %s", exec.ContainerName, exec.Service, exec.Status, exitCode, exec.TriggeredAt))
	}

	return 0
}
