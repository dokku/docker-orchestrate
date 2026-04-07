package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/docker-orchestrate/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CronNotifyCommand runs only the cron notifier (Docker event listener and webhook sender)
type CronNotifyCommand struct {
	command.Meta
}

func (c *CronNotifyCommand) Name() string {
	return "cron notify"
}

func (c *CronNotifyCommand) Synopsis() string {
	return "Run the cron notifier (Docker event listener and webhook sender)"
}

func (c *CronNotifyCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *CronNotifyCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Run the cron notifier": fmt.Sprintf("%s %s", appName, c.Name()),
	}
}

func (c *CronNotifyCommand) Arguments() []command.Argument {
	return []command.Argument{}
}

func (c *CronNotifyCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

func (c *CronNotifyCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *CronNotifyCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	return f
}

func (c *CronNotifyCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		complete.Flags{},
	)
}

func (c *CronNotifyCommand) Run(args []string) int {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create Docker client
	client, err := internal.NewDockerClient()
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	defer client.Close()

	// Create notifier and recover orphans
	notifier := internal.NewCronNotifier(client, logger)
	notifier.RecoverOrphans(ctx)

	logger.Info("Cron notifier started, listening for Docker events")

	// Start notifier in a goroutine
	notifierDone := make(chan struct{})
	go func() {
		notifier.Run(ctx)
		close(notifierDone)
	}()

	// Block on SIGTERM/SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info(fmt.Sprintf("Received signal %s, shutting down", sig))

	cancel()
	<-notifierDone

	logger.Info("Cron notifier stopped")
	return 0
}
