package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/josegonzalez/cli-skeleton/command"
)

// CronNotifier listens for Docker container events and sends webhook
// notifications for completed cron tasks based on container labels.
type CronNotifier struct {
	client DockerClientInterface
	logger *command.ZerologUi
}

// CronWebhookPayload is the JSON payload sent to webhook endpoints
type CronWebhookPayload struct {
	Project         string  `json:"project"`
	Service         string  `json:"service"`
	Schedule        string  `json:"schedule"`
	Status          string  `json:"status"`
	ExitCode        int     `json:"exit_code"`
	DurationSeconds float64 `json:"duration_seconds"`
	TriggeredAt     string  `json:"triggered_at"`
	CompletedAt     string  `json:"completed_at"`
	ContainerName   string  `json:"container_name"`
	Stdout          string  `json:"stdout,omitempty"`
	Stderr          string  `json:"stderr,omitempty"`
}

// NewCronNotifier creates a new notifier that watches Docker events
func NewCronNotifier(client DockerClientInterface, logger *command.ZerologUi) *CronNotifier {
	return &CronNotifier{
		client: client,
		logger: logger,
	}
}

// Run starts the notifier event loop. It subscribes to Docker container
// die events filtered by the cron label and processes notifications.
// This blocks until the context is cancelled.
func (n *CronNotifier) Run(ctx context.Context) error {
	filterArgs := filters.NewArgs()
	filterArgs.Add("type", string(events.ContainerEventType))
	filterArgs.Add("event", "die")
	filterArgs.Add("label", "com.dokku.orchestrate/cron=true")

	eventsCh, errCh := n.client.Events(ctx, events.ListOptions{
		Filters: filterArgs,
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("docker events error: %v", err)
			}
			return nil
		case event := <-eventsCh:
			n.handleContainerDie(ctx, event)
		}
	}
}

// RecoverOrphanedContainers finds cron containers that exited while the
// notifier was not running and processes their notifications.
func (n *CronNotifier) RecoverOrphanedContainers(ctx context.Context) error {
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "com.dokku.orchestrate/cron=true")
	filterArgs.Add("status", "exited")

	containers, err := n.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return fmt.Errorf("error listing orphaned cron containers: %v", err)
	}

	for _, c := range containers {
		n.logger.Info(fmt.Sprintf("Processing orphaned cron container: %s", containerShortName(c.Names)))
		n.processContainer(ctx, c.ID)
	}

	return nil
}

// RecoverOrphans is an alias for RecoverOrphanedContainers
func (n *CronNotifier) RecoverOrphans(ctx context.Context) error {
	return n.RecoverOrphanedContainers(ctx)
}

// handleContainerDie processes a container die event
func (n *CronNotifier) handleContainerDie(ctx context.Context, event events.Message) {
	containerID := event.Actor.ID
	n.processContainer(ctx, containerID)
}

// processContainer inspects a container, sends notifications if configured,
// and removes the container
func (n *CronNotifier) processContainer(ctx context.Context, containerID string) {
	// Inspect the container to get labels and state
	info, err := n.client.ContainerInspect(ctx, containerID)
	if err != nil {
		n.logger.Error(fmt.Sprintf("Error inspecting cron container %s: %v", shortID(containerID), err))
		return
	}

	labels := info.Config.Labels

	// Extract metadata from labels
	project := labels["com.dokku.orchestrate/cron-project"]
	service := labels["com.dokku.orchestrate/cron-service"]
	schedule := labels["com.dokku.orchestrate/cron-schedule"]
	triggeredAt := labels["com.dokku.orchestrate/cron-triggered-at"]
	notifyURL := labels["com.dokku.orchestrate/cron-notify-url"]
	notifyOn := labels["com.dokku.orchestrate/cron-notify-on"]
	includeOutputStr := labels["com.dokku.orchestrate/cron-notify-include-output"]

	// Determine exit code and status
	exitCode := 0
	if info.State != nil {
		exitCode = info.State.ExitCode
	}
	status := "success"
	if exitCode != 0 {
		status = "failure"
	}

	containerName := strings.TrimPrefix(info.Name, "/")
	n.logger.Info(fmt.Sprintf("Cron task completed: %s/%s container=%s status=%s exit_code=%d",
		project, service, containerName, status, exitCode))

	// Send webhook notification if configured
	if notifyURL != "" {
		shouldNotify := false
		switch notifyOn {
		case "always":
			shouldNotify = true
		case "success":
			shouldNotify = status == "success"
		case "failure":
			shouldNotify = status == "failure"
		default:
			shouldNotify = status == "failure" // default to failure-only
		}

		if shouldNotify {
			includeOutput, _ := strconv.ParseBool(includeOutputStr)

			// Calculate duration
			completedAt := time.Now().UTC()
			var durationSeconds float64
			if triggeredAt != "" {
				if triggered, err := time.Parse(time.RFC3339, triggeredAt); err == nil {
					durationSeconds = completedAt.Sub(triggered).Seconds()
				}
			}

			payload := CronWebhookPayload{
				Project:         project,
				Service:         service,
				Schedule:        schedule,
				Status:          status,
				ExitCode:        exitCode,
				DurationSeconds: durationSeconds,
				TriggeredAt:     triggeredAt,
				CompletedAt:     completedAt.Format(time.RFC3339),
				ContainerName:   containerName,
			}

			// Fetch container logs if include-output is enabled
			if includeOutput {
				stdout, stderr := n.getContainerLogs(ctx, containerID)
				payload.Stdout = stdout
				payload.Stderr = stderr
			}

			if err := n.sendWebhook(notifyURL, payload); err != nil {
				n.logger.Error(fmt.Sprintf("Error sending webhook for %s/%s: %v", project, service, err))
			} else {
				n.logger.Info(fmt.Sprintf("Webhook sent for %s/%s to %s", project, service, notifyURL))
			}
		}
	}

	// Remove the container (it was spawned without --rm when notifications are configured)
	if err := n.client.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		n.logger.Error(fmt.Sprintf("Error removing cron container %s: %v", shortID(containerID), err))
	}
}

// getContainerLogs fetches stdout and stderr logs from a container
func (n *CronNotifier) getContainerLogs(ctx context.Context, containerID string) (string, string) {
	// Get stdout
	stdoutReader, err := n.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: false,
	})
	if err != nil {
		return "", ""
	}
	defer stdoutReader.Close()

	stdoutBytes, _ := io.ReadAll(stdoutReader)

	// Get stderr
	stderrReader, err := n.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: false,
		ShowStderr: true,
	})
	if err != nil {
		return string(stdoutBytes), ""
	}
	defer stderrReader.Close()

	stderrBytes, _ := io.ReadAll(stderrReader)

	return strings.TrimSpace(string(stdoutBytes)), strings.TrimSpace(string(stderrBytes))
}

// sendWebhook sends a JSON POST request to the webhook URL
func (n *CronNotifier) sendWebhook(url string, payload CronWebhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling webhook payload: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("error creating webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "docker-orchestrate-cron")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// shortID returns the first 12 characters of a container ID
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// containerShortName extracts a readable name from container name list
func containerShortName(names []string) string {
	if len(names) > 0 {
		return strings.TrimPrefix(names[0], "/")
	}
	return "unknown"
}
