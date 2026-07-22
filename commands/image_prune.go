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

// ImagePruneCommand removes images left over from previous builds of a project
type ImagePruneCommand struct {
	command.Meta

	dryRun           bool
	envFiles         []string
	files            []string
	profiles         []string
	projectDirectory string
	projectName      string
}

func (c *ImagePruneCommand) Name() string {
	return "image prune"
}

func (c *ImagePruneCommand) Synopsis() string {
	return "Remove images left over from previous builds of a project"
}

func (c *ImagePruneCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *ImagePruneCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Prune leftover images for the project": fmt.Sprintf("%s %s", appName, c.Name()),
		"Preview which images would be pruned":  fmt.Sprintf("%s %s --dry-run", appName, c.Name()),
		"Prune leftover images for a project":   fmt.Sprintf("%s %s -p myproject", appName, c.Name()),
	}
}

func (c *ImagePruneCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *ImagePruneCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *ImagePruneCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *ImagePruneCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.BoolVar(&c.dryRun, "dry-run", false, "report which images would be removed without removing them")
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringSliceVar(&c.profiles, "profile", []string{}, "one or more profiles to enable")
	f.StringVar(&c.projectDirectory, "project-directory", "", "the path to the project directory")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	return f
}

func (c *ImagePruneCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--dry-run":           complete.PredictNothing,
			"--env-file":          complete.PredictFiles("*"),
			"--file":              complete.PredictFiles("*"),
			"--profile":           complete.PredictAnything,
			"--project-directory": complete.PredictDirs("*"),
			"--project-name":      complete.PredictAnything,
		},
	)
}

func (c *ImagePruneCommand) Run(args []string) int {
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

	client, err := internal.NewDockerClient()
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	defer client.Close()

	logger, ok := c.Ui.(*command.ZerologUi)
	if !ok {
		c.Ui.Error("UI is not a ZerologUi")
		return 1
	}

	pruned, err := internal.PruneImages(ctx, internal.PruneImagesInput{
		Client:      client,
		DryRun:      c.dryRun,
		Logger:      logger,
		Project:     project,
		ProjectName: c.projectName,
	})
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	if len(pruned) == 0 {
		logger.Info("No leftover images found")
		return 0
	}

	if c.dryRun {
		logger.Info(fmt.Sprintf("%d image(s) would be pruned", len(pruned)))
	} else {
		logger.Info(fmt.Sprintf("Pruned %d image(s)", len(pruned)))
	}

	return 0
}
