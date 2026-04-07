package internal

import (
	"testing"
	"time"
)

func TestMidnightPinnedScheduleNext(t *testing.T) {
	utc := time.UTC

	tests := []struct {
		name     string
		interval time.Duration
		location *time.Location
		now      time.Time
		expected time.Time
	}{
		{
			name:     "5m_interval_at_midnight",
			interval: 5 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 0, 0, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 0, 5, 0, 0, utc),
		},
		{
			name:     "5m_interval_at_00:03",
			interval: 5 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 0, 3, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 0, 5, 0, 0, utc),
		},
		{
			name:     "5m_interval_at_00:05_exactly",
			interval: 5 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 0, 5, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 0, 10, 0, 0, utc),
		},
		{
			name:     "5m_interval_at_12:37",
			interval: 5 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 12, 37, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 12, 40, 0, 0, utc),
		},
		{
			name:     "7m_odd_interval_at_00:06",
			interval: 7 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 0, 6, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 0, 7, 0, 0, utc),
		},
		{
			name:     "7m_odd_interval_at_00:14",
			interval: 7 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 0, 14, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 0, 21, 0, 0, utc),
		},
		{
			name:     "1h30m_interval_at_01:00",
			interval: 90 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 1, 0, 0, 0, utc),
			expected: time.Date(2024, 1, 15, 1, 30, 0, 0, utc),
		},
		{
			name:     "1h30m_interval_at_22:30",
			interval: 90 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 22, 30, 0, 0, utc),
			expected: time.Date(2024, 1, 16, 0, 0, 0, 0, utc),
			// 22:30 is exactly 15 intervals of 90m from midnight, so next is 24:00 = next midnight
		},
		{
			name:     "day_boundary_wrapping_5m_near_end_of_day",
			interval: 5 * time.Minute,
			location: utc,
			now:      time.Date(2024, 1, 15, 23, 58, 0, 0, utc),
			expected: time.Date(2024, 1, 16, 0, 0, 0, 0, utc),
		},
		{
			name:     "day_boundary_wrapping_1h_at_23:30",
			interval: 1 * time.Hour,
			location: utc,
			now:      time.Date(2024, 1, 15, 23, 30, 0, 0, utc),
			expected: time.Date(2024, 1, 16, 0, 0, 0, 0, utc),
		},
		{
			name:     "timezone_aware_midnight_new_york",
			interval: 1 * time.Hour,
			location: mustLoadLocation("America/New_York"),
			now:      time.Date(2024, 1, 15, 5, 30, 0, 0, time.UTC), // 00:30 EST
			expected: time.Date(2024, 1, 15, 1, 0, 0, 0, mustLoadLocation("America/New_York")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &MidnightPinnedSchedule{
				Interval: tt.interval,
				Location: tt.location,
			}

			got := s.Next(tt.now)
			if !got.Equal(tt.expected) {
				t.Errorf("Next(%v) = %v, expected %v", tt.now, got, tt.expected)
			}
		})
	}
}

func TestParseEveryInterval(t *testing.T) {
	tests := []struct {
		name       string
		schedule   string
		expectOK   bool
		expectDur  time.Duration
	}{
		{
			name:      "valid_5m",
			schedule:  "@every 5m",
			expectOK:  true,
			expectDur: 5 * time.Minute,
		},
		{
			name:      "valid_1h",
			schedule:  "@every 1h",
			expectOK:  true,
			expectDur: 1 * time.Hour,
		},
		{
			name:      "valid_1h30m",
			schedule:  "@every 1h30m",
			expectOK:  true,
			expectDur: 90 * time.Minute,
		},
		{
			name:      "valid_with_extra_whitespace",
			schedule:  "  @every   10s  ",
			expectOK:  true,
			expectDur: 10 * time.Second,
		},
		{
			name:     "invalid_duration",
			schedule: "@every notaduration",
			expectOK: false,
		},
		{
			name:     "non_every_string_cron_expression",
			schedule: "*/5 * * * *",
			expectOK: false,
		},
		{
			name:     "non_every_string_at_syntax",
			schedule: "@hourly",
			expectOK: false,
		},
		{
			name:     "negative_interval",
			schedule: "@every -5m",
			expectOK: false,
		},
		{
			name:     "zero_interval",
			schedule: "@every 0s",
			expectOK: false,
		},
		{
			name:     "empty_string",
			schedule: "",
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dur, ok := ParseEveryInterval(tt.schedule)
			if ok != tt.expectOK {
				t.Errorf("ParseEveryInterval(%q) ok = %v, want %v", tt.schedule, ok, tt.expectOK)
			}
			if ok && dur != tt.expectDur {
				t.Errorf("ParseEveryInterval(%q) duration = %v, want %v", tt.schedule, dur, tt.expectDur)
			}
		})
	}
}

func TestNewMidnightPinnedSchedule(t *testing.T) {
	tests := []struct {
		name        string
		interval    time.Duration
		loc         *time.Location
		expectError bool
		errContains string
		checkLoc    *time.Location
	}{
		{
			name:     "valid_interval_with_location",
			interval: 5 * time.Minute,
			loc:      time.UTC,
		},
		{
			name:        "zero_interval_returns_error",
			interval:    0,
			loc:         time.UTC,
			expectError: true,
			errContains: "interval must be positive",
		},
		{
			name:        "negative_interval_returns_error",
			interval:    -5 * time.Minute,
			loc:         time.UTC,
			expectError: true,
			errContains: "interval must be positive",
		},
		{
			name:     "nil_location_defaults_to_local",
			interval: 10 * time.Minute,
			loc:      nil,
			checkLoc: time.Local,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewMidnightPinnedSchedule(tt.interval, tt.loc)
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
			if s == nil {
				t.Fatal("expected non-nil schedule")
			}
			if s.Interval != tt.interval {
				t.Errorf("expected interval %v, got %v", tt.interval, s.Interval)
			}
			if tt.checkLoc != nil {
				if s.Location != tt.checkLoc {
					t.Errorf("expected location %v, got %v", tt.checkLoc, s.Location)
				}
			}
		})
	}
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
