package internal

import (
	"testing"

	types "github.com/compose-spec/compose-go/v2/types"
)

func TestParseCronConfig(t *testing.T) {
	tests := []struct {
		name         string
		extensions   map[string]interface{}
		defaults     *CronDefaults
		cliTimezone  string
		expectNil    bool
		expectError  bool
		errContains  string
		checkConfig  func(t *testing.T, config *CronConfig)
	}{
		{
			name: "valid_complete_config",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule":   "*/5 * * * *",
					"timezone":   "America/New_York",
					"timeout":    "30m",
					"no-overlap": true,
					"notify": map[string]interface{}{
						"url": "https://example.com/webhook",
						"on":  "always",
					},
				},
			},
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Schedule != "*/5 * * * *" {
					t.Errorf("expected schedule '*/5 * * * *', got %q", config.Schedule)
				}
				if config.Timezone != "America/New_York" {
					t.Errorf("expected timezone 'America/New_York', got %q", config.Timezone)
				}
				if config.Timeout.Minutes() != 30 {
					t.Errorf("expected timeout 30m, got %v", config.Timeout)
				}
				if !config.NoOverlap {
					t.Error("expected no-overlap to be true")
				}
				if config.Notify == nil {
					t.Fatal("expected notify to be non-nil")
				}
				if config.Notify.URL != "https://example.com/webhook" {
					t.Errorf("expected notify URL 'https://example.com/webhook', got %q", config.Notify.URL)
				}
				if config.Notify.On != "always" {
					t.Errorf("expected notify on 'always', got %q", config.Notify.On)
				}
			},
		},
		{
			name:       "no_x_cron_extension_returns_nil",
			extensions: map[string]interface{}{},
			expectNil:  true,
		},
		{
			name: "missing_schedule_returns_error",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"timezone": "UTC",
				},
			},
			expectError: true,
			errContains: "schedule is required",
		},
		{
			name: "empty_schedule_returns_error",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "",
				},
			},
			expectError: true,
			errContains: "schedule is required",
		},
		{
			name: "invalid_timezone_returns_error",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
					"timezone": "Not/A/Timezone",
				},
			},
			expectError: true,
			errContains: "invalid x-cron.timezone",
		},
		{
			name: "timezone_resolution_per_service_wins",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
					"timezone": "America/Chicago",
				},
			},
			defaults:    &CronDefaults{Timezone: "Europe/London"},
			cliTimezone: "Asia/Tokyo",
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Timezone != "America/Chicago" {
					t.Errorf("expected per-service timezone 'America/Chicago', got %q", config.Timezone)
				}
			},
		},
		{
			name: "timezone_resolution_defaults_when_no_per_service",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
				},
			},
			defaults:    &CronDefaults{Timezone: "Europe/London"},
			cliTimezone: "Asia/Tokyo",
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Timezone != "Europe/London" {
					t.Errorf("expected defaults timezone 'Europe/London', got %q", config.Timezone)
				}
			},
		},
		{
			name: "timezone_resolution_cli_when_no_defaults",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
				},
			},
			defaults:    nil,
			cliTimezone: "Asia/Tokyo",
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Timezone != "Asia/Tokyo" {
					t.Errorf("expected CLI timezone 'Asia/Tokyo', got %q", config.Timezone)
				}
			},
		},
		{
			name: "invalid_timeout_returns_error",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
					"timeout":  "not-a-duration",
				},
			},
			expectError: true,
			errContains: "invalid x-cron.timeout",
		},
		{
			name: "default_notify_from_project_defaults",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
				},
			},
			defaults: &CronDefaults{
				Notify: &CronNotifyConfig{
					URL: "https://defaults.example.com/hook",
					On:  "failure",
				},
			},
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Notify == nil {
					t.Fatal("expected notify from defaults, got nil")
				}
				if config.Notify.URL != "https://defaults.example.com/hook" {
					t.Errorf("expected defaults notify URL, got %q", config.Notify.URL)
				}
			},
		},
		{
			name: "per_service_notify_overrides_defaults",
			extensions: map[string]interface{}{
				"x-cron": map[string]interface{}{
					"schedule": "*/5 * * * *",
					"notify": map[string]interface{}{
						"url": "https://service.example.com/hook",
						"on":  "always",
					},
				},
			},
			defaults: &CronDefaults{
				Notify: &CronNotifyConfig{
					URL: "https://defaults.example.com/hook",
					On:  "failure",
				},
			},
			checkConfig: func(t *testing.T, config *CronConfig) {
				if config.Notify == nil {
					t.Fatal("expected per-service notify, got nil")
				}
				if config.Notify.URL != "https://service.example.com/hook" {
					t.Errorf("expected per-service notify URL, got %q", config.Notify.URL)
				}
				if config.Notify.On != "always" {
					t.Errorf("expected per-service notify on 'always', got %q", config.Notify.On)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &types.ServiceConfig{
				Name:       "test-service",
				Extensions: tt.extensions,
			}

			config, err := ParseCronConfig(svc, tt.defaults, tt.cliTimezone)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" {
					if !contains(err.Error(), tt.errContains) {
						t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectNil {
				if config != nil {
					t.Fatalf("expected nil config, got %+v", config)
				}
				return
			}
			if config == nil {
				t.Fatal("expected non-nil config")
			}
			if tt.checkConfig != nil {
				tt.checkConfig(t, config)
			}
		})
	}
}

func TestParseCronDefaults(t *testing.T) {
	tests := []struct {
		name        string
		extensions  map[string]interface{}
		expectNil   bool
		expectError bool
		errContains string
		check       func(t *testing.T, d *CronDefaults)
	}{
		{
			name: "valid_defaults_with_timezone_and_notify",
			extensions: map[string]interface{}{
				"x-cron-defaults": map[string]interface{}{
					"timezone": "Europe/Berlin",
					"notify": map[string]interface{}{
						"url": "https://example.com/hook",
						"on":  "always",
					},
				},
			},
			check: func(t *testing.T, d *CronDefaults) {
				if d.Timezone != "Europe/Berlin" {
					t.Errorf("expected timezone 'Europe/Berlin', got %q", d.Timezone)
				}
				if d.Notify == nil {
					t.Fatal("expected notify to be non-nil")
				}
				if d.Notify.URL != "https://example.com/hook" {
					t.Errorf("expected notify URL 'https://example.com/hook', got %q", d.Notify.URL)
				}
				if d.Notify.On != "always" {
					t.Errorf("expected notify on 'always', got %q", d.Notify.On)
				}
			},
		},
		{
			name: "invalid_timezone_returns_error",
			extensions: map[string]interface{}{
				"x-cron-defaults": map[string]interface{}{
					"timezone": "Invalid/Zone",
				},
			},
			expectError: true,
			errContains: "invalid timezone in x-cron-defaults",
		},
		{
			name:       "nil_extensions_returns_nil",
			extensions: map[string]interface{}{},
			expectNil:  true,
		},
		{
			name:       "no_x_cron_defaults_key_returns_nil",
			extensions: map[string]interface{}{"other-key": "value"},
			expectNil:  true,
		},
		{
			name: "notify_parsing_in_defaults",
			extensions: map[string]interface{}{
				"x-cron-defaults": map[string]interface{}{
					"notify": map[string]interface{}{
						"url":            "https://example.com/hook",
						"on":             "success",
						"include-output": true,
					},
				},
			},
			check: func(t *testing.T, d *CronDefaults) {
				if d.Notify == nil {
					t.Fatal("expected notify")
				}
				if d.Notify.On != "success" {
					t.Errorf("expected on 'success', got %q", d.Notify.On)
				}
				if !d.Notify.IncludeOutput {
					t.Error("expected include-output true")
				}
			},
		},
		{
			name: "invalid_notify_in_defaults_returns_error",
			extensions: map[string]interface{}{
				"x-cron-defaults": map[string]interface{}{
					"notify": map[string]interface{}{
						"on": "always",
						// missing url
					},
				},
			},
			expectError: true,
			errContains: "invalid notify in x-cron-defaults",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaults, err := ParseCronDefaults(tt.extensions)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectNil {
				if defaults != nil {
					t.Fatalf("expected nil, got %+v", defaults)
				}
				return
			}
			if defaults == nil {
				t.Fatal("expected non-nil defaults")
			}
			if tt.check != nil {
				tt.check(t, defaults)
			}
		})
	}
}

func TestParseCronNotifyConfig(t *testing.T) {
	tests := []struct {
		name        string
		raw         interface{}
		expectError bool
		errContains string
		check       func(t *testing.T, c *CronNotifyConfig)
	}{
		{
			name: "valid_config",
			raw: map[string]interface{}{
				"url":            "https://example.com/hook",
				"on":             "always",
				"include-output": true,
			},
			check: func(t *testing.T, c *CronNotifyConfig) {
				if c.URL != "https://example.com/hook" {
					t.Errorf("expected URL, got %q", c.URL)
				}
				if c.On != "always" {
					t.Errorf("expected on 'always', got %q", c.On)
				}
				if !c.IncludeOutput {
					t.Error("expected include-output true")
				}
			},
		},
		{
			name: "missing_url_returns_error",
			raw: map[string]interface{}{
				"on": "always",
			},
			expectError: true,
			errContains: "notify.url is required",
		},
		{
			name: "empty_url_returns_error",
			raw: map[string]interface{}{
				"url": "",
			},
			expectError: true,
			errContains: "notify.url is required",
		},
		{
			name: "invalid_on_value_returns_error",
			raw: map[string]interface{}{
				"url": "https://example.com/hook",
				"on":  "never",
			},
			expectError: true,
			errContains: "notify.on must be one of",
		},
		{
			name: "default_on_value_is_failure",
			raw: map[string]interface{}{
				"url": "https://example.com/hook",
			},
			check: func(t *testing.T, c *CronNotifyConfig) {
				if c.On != "failure" {
					t.Errorf("expected default on 'failure', got %q", c.On)
				}
			},
		},
		{
			name:        "non_map_input_returns_error",
			raw:         "not a map",
			expectError: true,
			errContains: "notify must be a mapping",
		},
		{
			name: "on_value_case_insensitive",
			raw: map[string]interface{}{
				"url": "https://example.com/hook",
				"on":  "SUCCESS",
			},
			check: func(t *testing.T, c *CronNotifyConfig) {
				if c.On != "success" {
					t.Errorf("expected on 'success', got %q", c.On)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := parseCronNotifyConfig(tt.raw)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if config == nil {
				t.Fatal("expected non-nil config")
			}
			if tt.check != nil {
				tt.check(t, config)
			}
		})
	}
}

// contains checks if s contains substr (helper to avoid importing strings just for tests)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
