package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CronRunCommand runs the cron scheduler and spawner without the notifier
type CronRunCommand struct {
	command.Meta

	build          bool
	configDir      string
	envFiles       []string
	files          []string
	projectName    string
	pull           string
	reloadInterval string
	timezone       string
}

func (c *CronRunCommand) Name() string {
	return "cron run"
}

func (c *CronRunCommand) Synopsis() string {
	return "Run the cron scheduler and spawner without the notifier"
}

func (c *CronRunCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *CronRunCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Run cron scheduler for a single project":   fmt.Sprintf("%s %s -f docker-compose.yml -p myproject", appName, c.Name()),
		"Run cron scheduler for a config directory": fmt.Sprintf("%s %s --config-dir /etc/docker-orchestrate", appName, c.Name()),
	}
}

func (c *CronRunCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *CronRunCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *CronRunCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *CronRunCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.BoolVar(&c.build, "build", false, "build images before running cron tasks")
	f.StringVar(&c.configDir, "config-dir", "", "directory to scan for projects (multi-project mode)")
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	f.StringVar(&c.pull, "pull", "", "pull image policy (always, missing, never)")
	f.StringVar(&c.reloadInterval, "reload-interval", "60s", "config reload interval")
	f.StringVar(&c.timezone, "timezone", "", "global default timezone for cron schedules")
	return f
}

func (c *CronRunCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--build":           complete.PredictNothing,
			"--config-dir":      complete.PredictDirs("*"),
			"--env-file":        complete.PredictFiles("*"),
			"-f":                complete.PredictFiles("*"),
			"--file":            complete.PredictFiles("*"),
			"-p":                complete.PredictAnything,
			"--project-name":    complete.PredictAnything,
			"--pull":            complete.PredictSet("always", "missing", "never"),
			"--reload-interval": complete.PredictAnything,
			"--timezone":        complete.PredictAnything,
		},
	)
}

func (c *CronRunCommand) Run(args []string) int {
	flags := c.FlagSet()
	flags.Usage = func() { c.Ui.Output(c.Help()) }
	if err := flags.Parse(args); err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	logger, ok := c.Ui.(*command.ZerologUi)
	if !ok {
		c.Ui.Error("UI is not a ZerologUi")
		return 1
	}

	// Validate: either single-project flags or config-dir, not both
	hasSingleProject := len(c.files) > 0 || c.projectName != ""
	hasMultiProject := c.configDir != ""
	if hasSingleProject && hasMultiProject {
		c.Ui.Error("Cannot use both single-project flags (-f/-p) and --config-dir together")
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}
	if !hasSingleProject && !hasMultiProject {
		c.Ui.Error("Either -f/-p (single project) or --config-dir (multi-project) is required")
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	reloadInterval, err := time.ParseDuration(c.reloadInterval)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Invalid --reload-interval: %v", err))
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load projects
	loadProjects := c.projectLoader()
	projects, err := loadProjects()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading projects: %v", err))
		return 1
	}

	// Create scheduler with spawner
	spawner := func(spawnCtx context.Context, project internal.CronProject, service internal.CronService) error {
		return internal.SpawnCronTask(spawnCtx, internal.SpawnCronTaskInput{
			Build:      c.build,
			Executor:   nil,
			Logger:     logger,
			Project:    project,
			PullPolicy: c.pull,
			Service:    service,
		})
	}

	scheduler, err := internal.NewCronScheduler(projects, spawner, logger)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error creating scheduler: %v", err))
		return 1
	}

	scheduler.Start()
	logger.Info(fmt.Sprintf("Cron scheduler started with %d entries (no notifier)", scheduler.Entries()))

	// Start config reloader
	scheduler.RunConfigReloader(ctx, reloadInterval, loadProjects)

	// Block on SIGTERM/SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info(fmt.Sprintf("Received signal %s, shutting down", sig))

	// Stop scheduler
	stopCtx := scheduler.Stop()
	<-stopCtx.Done()

	cancel()

	logger.Info("Cron scheduler stopped")
	return 0
}

// projectLoader returns a function that loads cron projects based on the configured mode
func (c *CronRunCommand) projectLoader() func() ([]internal.CronProject, error) {
	if c.configDir != "" {
		return func() ([]internal.CronProject, error) {
			return internal.ScanProjects(context.Background(), c.configDir, c.timezone)
		}
	}

	return func() ([]internal.CronProject, error) {
		files := c.files
		if len(files) == 0 {
			detectedFile, err := internal.ComposeFile()
			if err != nil {
				return nil, err
			}
			files = []string{detectedFile}
		}

		projectName := c.projectName
		if projectName == "" {
			projectName = "default"
		}

		project, err := internal.LoadSingleProject(context.Background(), files, c.envFiles, projectName, c.timezone)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, nil
		}
		return []internal.CronProject{*project}, nil
	}
}
