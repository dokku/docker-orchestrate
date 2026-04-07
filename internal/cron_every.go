package internal

import (
	"fmt"
	"strings"
	"time"
)

// MidnightPinnedSchedule implements cron.Schedule for @every intervals
// pinned to midnight in the configured timezone rather than process start time.
type MidnightPinnedSchedule struct {
	// Interval is the duration between runs
	Interval time.Duration
	// Location is the timezone for midnight calculation
	Location *time.Location
}

// Next returns the next activation time for the midnight-pinned schedule.
// The schedule fires at regular intervals from midnight in the configured timezone.
// At day boundaries, the schedule wraps to the next day's midnight.
func (s *MidnightPinnedSchedule) Next(t time.Time) time.Time {
	t = t.In(s.Location)
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.Location)
	elapsed := t.Sub(midnight)
	intervalsPassed := int(elapsed / s.Interval)
	next := midnight.Add(time.Duration(intervalsPassed+1) * s.Interval)
	// If next is more than 24h from midnight, wrap to next day's midnight
	nextMidnight := midnight.Add(24 * time.Hour)
	if !next.Before(nextMidnight) {
		return nextMidnight
	}
	return next
}

// ParseEveryInterval parses an @every expression and returns the interval duration.
// Returns zero duration and false if the expression is not an @every expression.
func ParseEveryInterval(schedule string) (time.Duration, bool) {
	schedule = strings.TrimSpace(schedule)
	if !strings.HasPrefix(schedule, "@every ") {
		return 0, false
	}
	durationStr := strings.TrimPrefix(schedule, "@every ")
	d, err := time.ParseDuration(strings.TrimSpace(durationStr))
	if err != nil {
		return 0, false
	}
	if d <= 0 {
		return 0, false
	}
	return d, true
}

// NewMidnightPinnedSchedule creates a new MidnightPinnedSchedule.
// Returns an error if the interval is invalid.
func NewMidnightPinnedSchedule(interval time.Duration, loc *time.Location) (*MidnightPinnedSchedule, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("interval must be positive, got %v", interval)
	}
	if loc == nil {
		loc = time.Local
	}
	return &MidnightPinnedSchedule{
		Interval: interval,
		Location: loc,
	}, nil
}
