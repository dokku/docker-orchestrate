package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CronInstallCommand generates and installs init system service configs for the cron scheduler
type CronInstallCommand struct {
	command.Meta

	configDir   string
	dryRun      bool
	envFiles    []string
	files       []string
	initSystem  string
	projectName string
	split       bool
}

func (c *CronInstallCommand) Name() string {
	return "cron install"
}

func (c *CronInstallCommand) Synopsis() string {
	return "Generate and install init system service configs for the cron scheduler"
}

func (c *CronInstallCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *CronInstallCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Install systemd service":                        fmt.Sprintf("%s %s --init systemd -f docker-compose.yml -p myproject", appName, c.Name()),
		"Install split systemd services":                 fmt.Sprintf("%s %s --init systemd --split -f docker-compose.yml -p myproject", appName, c.Name()),
		"Install runit service":                          fmt.Sprintf("%s %s --init runit -f docker-compose.yml -p myproject", appName, c.Name()),
		"Dry run to preview generated config":            fmt.Sprintf("%s %s --init systemd --dry-run -f docker-compose.yml -p myproject", appName, c.Name()),
		"Install systemd service for a config directory": fmt.Sprintf("%s %s --init systemd --config-dir /etc/docker-orchestrate", appName, c.Name()),
	}
}

func (c *CronInstallCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *CronInstallCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *CronInstallCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *CronInstallCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	f.StringVar(&c.configDir, "config-dir", "", "directory to scan for projects (multi-project mode)")
	f.BoolVar(&c.dryRun, "dry-run", false, "print generated configs without installing")
	f.StringSliceVar(&c.envFiles, "env-file", []string{}, "one or more paths to environment files")
	f.StringSliceVarP(&c.files, "file", "f", []string{}, "one or more paths to Compose files")
	f.StringVar(&c.initSystem, "init", "systemd", "init system to generate configs for (systemd, runit)")
	f.StringVarP(&c.projectName, "project-name", "p", "", "the name of the project")
	f.BoolVar(&c.split, "split", false, "generate separate services for scheduler and notifier")
	return f
}

func (c *CronInstallCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{
			"--config-dir":   complete.PredictDirs("*"),
			"--dry-run":      complete.PredictNothing,
			"--env-file":     complete.PredictFiles("*"),
			"-f":             complete.PredictFiles("*"),
			"--file":         complete.PredictFiles("*"),
			"--init":         complete.PredictSet("systemd", "runit"),
			"-p":             complete.PredictAnything,
			"--project-name": complete.PredictAnything,
			"--split":        complete.PredictNothing,
		},
	)
}

func (c *CronInstallCommand) Run(args []string) int {
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

	// Determine the binary path
	binaryPath, err := os.Executable()
	if err != nil {
		binaryPath = "docker-orchestrate"
	}

	// Build the project flags to pass through to the cron command
	var projectFlags string
	if c.configDir != "" {
		projectFlags = fmt.Sprintf("--config-dir %s", c.configDir)
	} else {
		parts := []string{}
		for _, f := range c.files {
			parts = append(parts, fmt.Sprintf("-f %s", f))
		}
		if c.projectName != "" {
			parts = append(parts, fmt.Sprintf("-p %s", c.projectName))
		}
		for _, ef := range c.envFiles {
			parts = append(parts, fmt.Sprintf("--env-file %s", ef))
		}
		projectFlags = strings.Join(parts, " ")
	}

	// Determine the service name
	serviceName := "docker-orchestrate-cron"
	if c.projectName != "" {
		serviceName = fmt.Sprintf("docker-orchestrate-cron-%s", c.projectName)
	}

	data := serviceTemplateData{
		BinaryPath:   binaryPath,
		ProjectFlags: projectFlags,
		ServiceName:  serviceName,
	}

	switch c.initSystem {
	case "systemd":
		return c.installSystemd(data, logger)
	case "runit":
		return c.installRunit(data, logger)
	}

	return 0
}

type serviceTemplateData struct {
	BinaryPath   string
	ProjectFlags string
	ServiceName  string
}

func (c *CronInstallCommand) installSystemd(data serviceTemplateData, logger *command.ZerologUi) int {
	if c.split {
		return c.installSystemdSplit(data, logger)
	}
	return c.installSystemdCombined(data, logger)
}

func (c *CronInstallCommand) installSystemdCombined(data serviceTemplateData, logger *command.ZerologUi) int {
	tmpl := template.Must(template.New("systemd").Parse(systemdCombinedTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating systemd unit: %v", err))
		return 1
	}

	unitPath := fmt.Sprintf("/etc/systemd/system/%s.service", data.ServiceName)

	if c.dryRun {
		logger.Info(fmt.Sprintf("Would write %s:", unitPath))
		c.Ui.Output(buf.String())
		return 0
	}

	if err := os.WriteFile(unitPath, buf.Bytes(), 0644); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", unitPath, err))
		return 1
	}

	logger.Info(fmt.Sprintf("Installed systemd service: %s", unitPath))
	logger.Info("Run the following to enable and start:")
	logger.Info(fmt.Sprintf("  systemctl daemon-reload"))
	logger.Info(fmt.Sprintf("  systemctl enable --now %s.service", data.ServiceName))
	return 0
}

func (c *CronInstallCommand) installSystemdSplit(data serviceTemplateData, logger *command.ZerologUi) int {
	// Generate scheduler service
	runData := data
	runData.ServiceName = data.ServiceName + "-run"
	tmpl := template.Must(template.New("systemd-run").Parse(systemdRunTemplate))
	var runBuf bytes.Buffer
	if err := tmpl.Execute(&runBuf, runData); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating systemd run unit: %v", err))
		return 1
	}

	// Generate notifier service
	notifyData := data
	notifyData.ServiceName = data.ServiceName + "-notify"
	tmpl = template.Must(template.New("systemd-notify").Parse(systemdNotifyTemplate))
	var notifyBuf bytes.Buffer
	if err := tmpl.Execute(&notifyBuf, notifyData); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating systemd notify unit: %v", err))
		return 1
	}

	runUnitPath := fmt.Sprintf("/etc/systemd/system/%s.service", runData.ServiceName)
	notifyUnitPath := fmt.Sprintf("/etc/systemd/system/%s.service", notifyData.ServiceName)

	if c.dryRun {
		logger.Info(fmt.Sprintf("Would write %s:", runUnitPath))
		c.Ui.Output(runBuf.String())
		logger.Info(fmt.Sprintf("Would write %s:", notifyUnitPath))
		c.Ui.Output(notifyBuf.String())
		return 0
	}

	if err := os.WriteFile(runUnitPath, runBuf.Bytes(), 0644); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", runUnitPath, err))
		return 1
	}
	logger.Info(fmt.Sprintf("Installed systemd service: %s", runUnitPath))

	if err := os.WriteFile(notifyUnitPath, notifyBuf.Bytes(), 0644); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", notifyUnitPath, err))
		return 1
	}
	logger.Info(fmt.Sprintf("Installed systemd service: %s", notifyUnitPath))

	logger.Info("Run the following to enable and start:")
	logger.Info(fmt.Sprintf("  systemctl daemon-reload"))
	logger.Info(fmt.Sprintf("  systemctl enable --now %s.service %s.service", runData.ServiceName, notifyData.ServiceName))
	return 0
}

func (c *CronInstallCommand) installRunit(data serviceTemplateData, logger *command.ZerologUi) int {
	if c.split {
		return c.installRunitSplit(data, logger)
	}
	return c.installRunitCombined(data, logger)
}

func (c *CronInstallCommand) installRunitCombined(data serviceTemplateData, logger *command.ZerologUi) int {
	tmpl := template.Must(template.New("runit").Parse(runitRunTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating runit run script: %v", err))
		return 1
	}

	serviceDir := filepath.Join("/etc/sv", data.ServiceName)
	runPath := filepath.Join(serviceDir, "run")

	if c.dryRun {
		logger.Info(fmt.Sprintf("Would write %s:", runPath))
		c.Ui.Output(buf.String())
		return 0
	}

	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error creating directory %s: %v", serviceDir, err))
		return 1
	}

	if err := os.WriteFile(runPath, buf.Bytes(), 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", runPath, err))
		return 1
	}

	logger.Info(fmt.Sprintf("Installed runit service: %s", serviceDir))
	logger.Info("Run the following to enable:")
	logger.Info(fmt.Sprintf("  ln -s %s /etc/service/%s", serviceDir, data.ServiceName))
	return 0
}

func (c *CronInstallCommand) installRunitSplit(data serviceTemplateData, logger *command.ZerologUi) int {
	// Generate scheduler run script
	runData := data
	runData.ServiceName = data.ServiceName + "-run"
	tmpl := template.Must(template.New("runit-run").Parse(runitCronRunTemplate))
	var runBuf bytes.Buffer
	if err := tmpl.Execute(&runBuf, runData); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating runit run script: %v", err))
		return 1
	}

	// Generate notifier run script
	notifyData := data
	notifyData.ServiceName = data.ServiceName + "-notify"
	tmpl = template.Must(template.New("runit-notify").Parse(runitNotifyRunTemplate))
	var notifyBuf bytes.Buffer
	if err := tmpl.Execute(&notifyBuf, notifyData); err != nil {
		c.Ui.Error(fmt.Sprintf("Error generating runit notify script: %v", err))
		return 1
	}

	runServiceDir := filepath.Join("/etc/sv", runData.ServiceName)
	runPath := filepath.Join(runServiceDir, "run")
	notifyServiceDir := filepath.Join("/etc/sv", notifyData.ServiceName)
	notifyPath := filepath.Join(notifyServiceDir, "run")

	if c.dryRun {
		logger.Info(fmt.Sprintf("Would write %s:", runPath))
		c.Ui.Output(runBuf.String())
		logger.Info(fmt.Sprintf("Would write %s:", notifyPath))
		c.Ui.Output(notifyBuf.String())
		return 0
	}

	if err := os.MkdirAll(runServiceDir, 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error creating directory %s: %v", runServiceDir, err))
		return 1
	}
	if err := os.WriteFile(runPath, runBuf.Bytes(), 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", runPath, err))
		return 1
	}
	logger.Info(fmt.Sprintf("Installed runit service: %s", runServiceDir))

	if err := os.MkdirAll(notifyServiceDir, 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error creating directory %s: %v", notifyServiceDir, err))
		return 1
	}
	if err := os.WriteFile(notifyPath, notifyBuf.Bytes(), 0755); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing %s: %v", notifyPath, err))
		return 1
	}
	logger.Info(fmt.Sprintf("Installed runit service: %s", notifyServiceDir))

	logger.Info("Run the following to enable:")
	logger.Info(fmt.Sprintf("  ln -s %s /etc/service/%s", runServiceDir, runData.ServiceName))
	logger.Info(fmt.Sprintf("  ln -s %s /etc/service/%s", notifyServiceDir, notifyData.ServiceName))
	return 0
}

// Systemd templates

var systemdCombinedTemplate = `[Unit]
Description=Docker Orchestrate Cron ({{.ServiceName}})
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart={{.BinaryPath}} cron {{.ProjectFlags}}
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`

var systemdRunTemplate = `[Unit]
Description=Docker Orchestrate Cron Scheduler ({{.ServiceName}})
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart={{.BinaryPath}} cron run {{.ProjectFlags}}
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`

var systemdNotifyTemplate = `[Unit]
Description=Docker Orchestrate Cron Notifier ({{.ServiceName}})
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart={{.BinaryPath}} cron notify
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`

// Runit templates

var runitRunTemplate = `#!/bin/sh
exec chpst {{.BinaryPath}} cron {{.ProjectFlags}}
`

var runitCronRunTemplate = `#!/bin/sh
exec chpst {{.BinaryPath}} cron run {{.ProjectFlags}}
`

var runitNotifyRunTemplate = `#!/bin/sh
exec chpst {{.BinaryPath}} cron notify
`
