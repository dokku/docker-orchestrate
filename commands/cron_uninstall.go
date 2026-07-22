package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CronUninstallCommand removes installed init system service configs
type CronUninstallCommand struct {
	command.Meta

	dryRun        bool
	includeNotify bool
	initSystem    string
	projectName   string
	split         bool
}

func (c *CronUninstallCommand) Name() string {
	return "cron uninstall"
}

func (c *CronUninstallCommand) Synopsis() string {
	return "Remove installed init system service configs for the cron scheduler"
}

func (c *CronUninstallCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *CronUninstallCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Uninstall systemd service":                fmt.Sprintf("%s %s --init systemd -p myproject", appName, c.Name()),
		"Uninstall split systemd services":         fmt.Sprintf("%s %s --init systemd --split -p myproject", appName, c.Name()),
		"Uninstall runit service":                  fmt.Sprintf("%s %s --init runit -p myproject", appName, c.Name()),
		"Dry run to preview what would be removed": fmt.Sprintf("%s %s --init systemd --dry-run -p myproject", appName, c.Name()),
		"Uninstall including the shared notifier":  fmt.Sprintf("%s %s --init systemd --split --include-notify", appName, c.Name()),
	}
}

func (c *CronUninstallCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *CronUninstallCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *CronUninstallCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *CronUninstallCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.BoolVar(&c.dryRun, "dry-run", false, "print what would be removed without removing")
	f.BoolVar(&c.includeNotify, "include-notify", false, "also remove the shared notifier service (split mode only)")
	f.StringVar(&c.initSystem, "init", "systemd", "init system to remove configs for (systemd, runit)")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	f.BoolVar(&c.split, "split", false, "remove separate scheduler and notifier services")
	return f
}

func (c *CronUninstallCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--dry-run":        complete.PredictNothing,
			"--include-notify": complete.PredictNothing,
			"--init":           complete.PredictSet("systemd", "runit"),
			"-p":               complete.PredictAnything,
			"--project-name":   complete.PredictAnything,
			"--split":          complete.PredictNothing,
		},
	)
}

func (c *CronUninstallCommand) Run(args []string) int {
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

	validInitSystems := []string{"systemd", "runit"}
	found := false
	for _, s := range validInitSystems {
		if c.initSystem == s {
			found = true
			break
		}
	}
	if !found {
		c.Ui.Error(fmt.Sprintf("Invalid --init value %q: must be one of: %s", c.initSystem, strings.Join(validInitSystems, ", ")))
		return 1
	}

	// Determine the service name
	serviceName := "docker-orchestrate-cron"
	if c.projectName != "" {
		serviceName = fmt.Sprintf("docker-orchestrate-cron-%s", c.projectName)
	}

	switch c.initSystem {
	case "systemd":
		return c.uninstallSystemd(serviceName, logger)
	case "runit":
		return c.uninstallRunit(serviceName, logger)
	}

	return 0
}

func (c *CronUninstallCommand) uninstallSystemd(serviceName string, logger *command.ZerologUi) int {
	var paths []string

	if c.split {
		paths = append(paths, fmt.Sprintf("/etc/systemd/system/%s-run.service", serviceName))
		if c.includeNotify {
			paths = append(paths, fmt.Sprintf("/etc/systemd/system/%s-notify.service", serviceName))
		}
	} else {
		paths = append(paths, fmt.Sprintf("/etc/systemd/system/%s.service", serviceName))
	}

	return c.removeFiles(paths, logger)
}

func (c *CronUninstallCommand) uninstallRunit(serviceName string, logger *command.ZerologUi) int {
	var paths []string
	var symlinks []string

	if c.split {
		runName := serviceName + "-run"
		paths = append(paths, filepath.Join("/etc/sv", runName))
		symlinks = append(symlinks, filepath.Join("/etc/service", runName))
		if c.includeNotify {
			notifyName := serviceName + "-notify"
			paths = append(paths, filepath.Join("/etc/sv", notifyName))
			symlinks = append(symlinks, filepath.Join("/etc/service", notifyName))
		}
	} else {
		paths = append(paths, filepath.Join("/etc/sv", serviceName))
		symlinks = append(symlinks, filepath.Join("/etc/service", serviceName))
	}

	// Remove symlinks first
	for _, link := range symlinks {
		if c.dryRun {
			logger.Info(fmt.Sprintf("Would remove symlink: %s", link))
			continue
		}
		if _, err := os.Lstat(link); err == nil {
			if err := os.Remove(link); err != nil {
				c.Ui.Error(fmt.Sprintf("Error removing symlink %s: %v", link, err))
				return 1
			}
			logger.Info(fmt.Sprintf("Removed symlink: %s", link))
		}
	}

	return c.removeDirs(paths, logger)
}

func (c *CronUninstallCommand) removeFiles(paths []string, logger *command.ZerologUi) int {
	for _, path := range paths {
		if c.dryRun {
			logger.Info(fmt.Sprintf("Would remove: %s", path))
			continue
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			logger.Info(fmt.Sprintf("Not found (skipping): %s", path))
			continue
		}

		if err := os.Remove(path); err != nil {
			c.Ui.Error(fmt.Sprintf("Error removing %s: %v", path, err))
			return 1
		}
		logger.Info(fmt.Sprintf("Removed: %s", path))
	}

	if !c.dryRun {
		logger.Info("Service configs removed. You may need to run: systemctl daemon-reload")
	}
	return 0
}

func (c *CronUninstallCommand) removeDirs(paths []string, logger *command.ZerologUi) int {
	for _, path := range paths {
		if c.dryRun {
			logger.Info(fmt.Sprintf("Would remove directory: %s", path))
			continue
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			logger.Info(fmt.Sprintf("Not found (skipping): %s", path))
			continue
		}

		if err := os.RemoveAll(path); err != nil {
			c.Ui.Error(fmt.Sprintf("Error removing %s: %v", path, err))
			return 1
		}
		logger.Info(fmt.Sprintf("Removed directory: %s", path))
	}
	return 0
}
