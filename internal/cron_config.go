package internal

import (
	"fmt"
	"strings"
	"time"

	types "github.com/compose-spec/compose-go/v2/types"
)

// CronConfig represents the parsed x-cron configuration for a service
type CronConfig struct {
	// Schedule is the cron expression or @every interval
	Schedule string
	// Timezone is the IANA timezone name
	Timezone string
	// Timeout is the maximum duration for the cron task
	Timeout time.Duration
	// NoOverlap prevents concurrent execution of the same cron task
	NoOverlap bool
	// Notify is the notification configuration
	Notify *CronNotifyConfig
}

// CronNotifyConfig represents webhook notification settings
type CronNotifyConfig struct {
	// URL is the webhook endpoint
	URL string
	// On determines when to send notifications: "success", "failure", or "always"
	On string
	// IncludeOutput includes stdout/stderr in the webhook payload
	IncludeOutput bool
}

// CronDefaults represents project-level cron defaults from x-cron-defaults
type CronDefaults struct {
	// Timezone is the default timezone for all cron services in the project
	Timezone string
	// Notify is the default notification configuration
	Notify *CronNotifyConfig
}

// CronProject represents a project with cron-scheduled services
type CronProject struct {
	// Name is the project name
	Name string
	// ComposeFiles is the list of compose file paths
	ComposeFiles []string
	// EnvFiles is the list of env file paths
	EnvFiles []string
	// WorkingDirectory is the project working directory
	WorkingDirectory string
	// Defaults is the project-level cron defaults
	Defaults *CronDefaults
	// Services is the list of cron-scheduled services
	Services []CronService
}

// CronService represents a service with cron scheduling
type CronService struct {
	// Name is the service name
	Name string
	// Config is the parsed cron configuration
	Config *CronConfig
	// Service is the compose service configuration
	Service *types.ServiceConfig
}

// ParseCronDefaults parses x-cron-defaults from project-level extensions
func ParseCronDefaults(extensions map[string]interface{}) (*CronDefaults, error) {
	raw, ok := extensions["x-cron-defaults"]
	if !ok {
		return nil, nil
	}

	defaultsMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("x-cron-defaults must be a mapping")
	}

	defaults := &CronDefaults{}

	if tz, ok := defaultsMap["timezone"].(string); ok {
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("invalid timezone in x-cron-defaults: %q: %v", tz, err)
		}
		defaults.Timezone = tz
	}

	if notifyRaw, ok := defaultsMap["notify"]; ok {
		notify, err := parseCronNotifyConfig(notifyRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid notify in x-cron-defaults: %v", err)
		}
		defaults.Notify = notify
	}

	return defaults, nil
}

// ParseCronConfig parses x-cron from a service's extensions
func ParseCronConfig(service *types.ServiceConfig, defaults *CronDefaults, cliTimezone string) (*CronConfig, error) {
	raw, ok := service.Extensions["x-cron"]
	if !ok {
		return nil, nil
	}

	cronMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("x-cron must be a mapping")
	}

	config := &CronConfig{}

	// Parse schedule (required)
	schedule, ok := cronMap["schedule"].(string)
	if !ok || schedule == "" {
		return nil, fmt.Errorf("x-cron.schedule is required and must be a string")
	}
	config.Schedule = schedule

	// Parse timezone (optional, with resolution order)
	if tz, ok := cronMap["timezone"].(string); ok {
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("invalid x-cron.timezone: %q: %v", tz, err)
		}
		config.Timezone = tz
	} else if defaults != nil && defaults.Timezone != "" {
		config.Timezone = defaults.Timezone
	} else if cliTimezone != "" {
		config.Timezone = cliTimezone
	}

	// Parse timeout (optional)
	if timeoutStr, ok := cronMap["timeout"].(string); ok {
		parsed, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid x-cron.timeout: %q: %v", timeoutStr, err)
		}
		config.Timeout = parsed
	}

	// Parse no-overlap (optional)
	if noOverlap, ok := cronMap["no-overlap"].(bool); ok {
		config.NoOverlap = noOverlap
	}

	// Parse notify (optional, with defaults merge)
	if notifyRaw, ok := cronMap["notify"]; ok {
		notify, err := parseCronNotifyConfig(notifyRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid x-cron.notify: %v", err)
		}
		config.Notify = notify
	} else if defaults != nil && defaults.Notify != nil {
		config.Notify = defaults.Notify
	}

	return config, nil
}

// parseCronNotifyConfig parses a notify configuration from a map
func parseCronNotifyConfig(raw interface{}) (*CronNotifyConfig, error) {
	notifyMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("notify must be a mapping")
	}

	config := &CronNotifyConfig{}

	url, ok := notifyMap["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("notify.url is required and must be a string")
	}
	config.URL = url

	if on, ok := notifyMap["on"].(string); ok {
		on = strings.ToLower(on)
		switch on {
		case "success", "failure", "always":
			config.On = on
		default:
			return nil, fmt.Errorf("notify.on must be one of: success, failure, always (got: %q)", on)
		}
	} else {
		config.On = "failure"
	}

	if includeOutput, ok := notifyMap["include-output"].(bool); ok {
		config.IncludeOutput = includeOutput
	}

	return config, nil
}
