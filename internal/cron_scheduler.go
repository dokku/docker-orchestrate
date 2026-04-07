package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cron "github.com/robfig/cron/v3"

	"github.com/josegonzalez/cli-skeleton/command"
)

// SpawnerFunc is the function signature for spawning cron tasks
type SpawnerFunc func(ctx context.Context, project CronProject, service CronService) error

// CronScheduler manages cron job scheduling for one or more projects
type CronScheduler struct {
	cron     *cron.Cron
	spawner  SpawnerFunc
	logger   *command.ZerologUi
	entries  map[string]cron.EntryID // key: "project/service"
	mu       sync.Mutex
	projects []CronProject
}

// NewCronScheduler creates a new cron scheduler with the given projects and spawner function
func NewCronScheduler(projects []CronProject, spawner SpawnerFunc, logger *command.ZerologUi) (*CronScheduler, error) {
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	c := cron.New(cron.WithParser(parser))

	scheduler := &CronScheduler{
		cron:     c,
		spawner:  spawner,
		logger:   logger,
		entries:  make(map[string]cron.EntryID),
		projects: projects,
	}

	if err := scheduler.registerJobs(projects); err != nil {
		return nil, err
	}

	return scheduler, nil
}

// Start begins the cron scheduler
func (s *CronScheduler) Start() {
	s.cron.Start()
}

// Stop halts the cron scheduler
func (s *CronScheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Reload updates the scheduler with new project configurations.
// It removes jobs that are no longer present, adds new jobs,
// and updates jobs whose configuration has changed.
func (s *CronScheduler) Reload(projects []CronProject) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build new job map
	newJobs := make(map[string]CronService)
	newProjects := make(map[string]CronProject)
	for _, project := range projects {
		for _, service := range project.Services {
			key := project.Name + "/" + service.Name
			newJobs[key] = service
			newProjects[key] = project
		}
	}

	// Remove jobs no longer present
	for key, entryID := range s.entries {
		if _, ok := newJobs[key]; !ok {
			s.cron.Remove(entryID)
			delete(s.entries, key)
			s.logger.Info(fmt.Sprintf("Removed cron job: %s", key))
		}
	}

	// Add or update jobs
	for key, service := range newJobs {
		if _, exists := s.entries[key]; exists {
			// Remove old entry before re-adding with potentially new config
			s.cron.Remove(s.entries[key])
			delete(s.entries, key)
		}

		project := newProjects[key]
		entryID, err := s.addJob(project, service)
		if err != nil {
			s.logger.Error(fmt.Sprintf("Error adding cron job %s: %v", key, err))
			continue
		}
		s.entries[key] = entryID
	}

	s.projects = projects
	return nil
}

// registerJobs adds all jobs from the given projects to the cron engine
func (s *CronScheduler) registerJobs(projects []CronProject) error {
	for _, project := range projects {
		for _, service := range project.Services {
			key := project.Name + "/" + service.Name
			entryID, err := s.addJob(project, service)
			if err != nil {
				return fmt.Errorf("error registering cron job %s: %v", key, err)
			}
			s.entries[key] = entryID
		}
	}
	return nil
}

// addJob adds a single cron job to the engine
func (s *CronScheduler) addJob(project CronProject, service CronService) (cron.EntryID, error) {
	config := service.Config
	key := project.Name + "/" + service.Name

	// Resolve timezone
	loc := time.Local
	if config.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(config.Timezone)
		if err != nil {
			return 0, fmt.Errorf("invalid timezone %q for %s: %v", config.Timezone, key, err)
		}
	}

	// Create the job function
	p := project
	svc := service
	jobFunc := func() {
		s.logger.Info(fmt.Sprintf("Triggering cron job: %s (schedule: %s)", key, config.Schedule))
		if err := s.spawner(context.Background(), p, svc); err != nil {
			s.logger.Error(fmt.Sprintf("Cron job %s failed to spawn: %v", key, err))
		}
	}

	// Handle @every with midnight pinning
	if interval, ok := ParseEveryInterval(config.Schedule); ok {
		schedule, err := NewMidnightPinnedSchedule(interval, loc)
		if err != nil {
			return 0, fmt.Errorf("invalid @every schedule for %s: %v", key, err)
		}
		return s.cron.Schedule(schedule, cron.FuncJob(jobFunc)), nil
	}

	// Standard cron expression — prepend CRON_TZ if timezone is configured
	schedule := config.Schedule
	if config.Timezone != "" {
		schedule = fmt.Sprintf("CRON_TZ=%s %s", config.Timezone, schedule)
	}

	return s.cron.AddFunc(schedule, jobFunc)
}

// RunConfigReloader starts a goroutine that periodically re-reads project
// configurations and reloads the scheduler. It also listens for SIGHUP
// to trigger an immediate reload.
func (s *CronScheduler) RunConfigReloader(ctx context.Context, reloadInterval time.Duration, loadProjects func() ([]CronProject, error)) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	go func() {
		ticker := time.NewTicker(reloadInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				signal.Stop(sighup)
				return
			case <-sighup:
				s.logger.Info("Received SIGHUP, reloading configuration")
				s.doReload(loadProjects)
			case <-ticker.C:
				s.doReload(loadProjects)
			}
		}
	}()
}

// doReload performs the actual config reload
func (s *CronScheduler) doReload(loadProjects func() ([]CronProject, error)) {
	projects, err := loadProjects()
	if err != nil {
		s.logger.Error(fmt.Sprintf("Error reloading configuration: %v", err))
		return
	}
	if err := s.Reload(projects); err != nil {
		s.logger.Error(fmt.Sprintf("Error applying reloaded configuration: %v", err))
	}
}

// Entries returns the number of scheduled entries
func (s *CronScheduler) Entries() int {
	return len(s.cron.Entries())
}
