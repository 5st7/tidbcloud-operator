package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	schedulerv1 "github.com/5st7/tidbcloud-operator/api/v1"
	"github.com/robfig/cron/v3"
)

// SchedulerService interface defines cron scheduling operations
type SchedulerService interface {
	// AddSchedule adds a unified schedule and returns job ID
	AddSchedule(ctx context.Context, namespace, schedulerName string, schedule schedulerv1.Schedule, callback func()) (int, error)

	// RemoveSchedule removes a schedule by job ID
	RemoveSchedule(namespace, schedulerName string, jobID int) error

	// RemoveAllSchedules removes all schedules for a scheduler
	RemoveAllSchedules(namespace, schedulerName string) error

	// GetNextRun gets the next run time for a job ID
	GetNextRun(namespace, schedulerName string, jobID int) (*time.Time, error)

	// GetActiveSchedules returns all active schedules for a namespace
	GetActiveSchedules(namespace string) map[string][]ScheduleInfo

	// Shutdown gracefully shuts down all schedulers
	Shutdown(ctx context.Context) error
}

// ScheduleInfo contains information about an active schedule
type ScheduleInfo struct {
	JobID         int
	Schedule      string
	NextRun       time.Time
	SchedulerName string
	ScheduleType  string // "scale" or "suspend" or "resume"
}

// TimezoneAwareScheduler implements SchedulerService with timezone support
type TimezoneAwareScheduler struct {
	// Map of namespace -> timezone -> cron instance
	schedulers map[string]map[string]*cron.Cron

	// Map of namespace -> scheduler name -> job IDs
	scheduleMap map[string]map[string][]int

	// Mutex for thread-safe operations
	mu sync.RWMutex

	// Default timezone
	defaultTimezone string

	// Job execution tracking
	executionMap map[string]map[int]*JobExecution
}

// JobExecution tracks execution information for a job
type JobExecution struct {
	LastRun        *time.Time
	ExecutionCount int
	LastResult     string
	ScheduleType   string
}

// NewTimezoneAwareScheduler creates a new timezone-aware scheduler service
func NewTimezoneAwareScheduler(defaultTimezone string) *TimezoneAwareScheduler {
	if defaultTimezone == "" {
		defaultTimezone = "UTC"
	}

	return &TimezoneAwareScheduler{
		schedulers:      make(map[string]map[string]*cron.Cron),
		scheduleMap:     make(map[string]map[string][]int),
		executionMap:    make(map[string]map[int]*JobExecution),
		defaultTimezone: defaultTimezone,
	}
}

// getOrCreateCronScheduler gets or creates a cron scheduler for the given namespace and timezone
func (s *TimezoneAwareScheduler) getOrCreateCronScheduler(namespace, timezone string) (*cron.Cron, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize namespace maps if needed
	if s.schedulers[namespace] == nil {
		s.schedulers[namespace] = make(map[string]*cron.Cron)
		s.scheduleMap[namespace] = make(map[string][]int)
		s.executionMap[namespace] = make(map[int]*JobExecution)
	}

	// Check if scheduler already exists for this timezone
	if cronScheduler, exists := s.schedulers[namespace][timezone]; exists {
		return cronScheduler, nil
	}

	// Parse timezone
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", timezone, err)
	}

	// Create new cron scheduler with timezone support
	cronScheduler := cron.New(
		cron.WithLocation(location),
		cron.WithChain(
			cron.Recover(cron.DefaultLogger),             // Panic recovery
			cron.DelayIfStillRunning(cron.DefaultLogger), // Prevent overlapping runs
		),
	)

	// Start the scheduler
	cronScheduler.Start()

	// Store the scheduler
	s.schedulers[namespace][timezone] = cronScheduler

	return cronScheduler, nil
}

// AddSchedule adds a unified schedule
func (s *TimezoneAwareScheduler) AddSchedule(ctx context.Context, namespace, schedulerName string, schedule schedulerv1.Schedule, callback func()) (int, error) {
	timezone := s.defaultTimezone
	if ctx.Value("timezone") != nil {
		timezone = ctx.Value("timezone").(string)
	}

	cronScheduler, err := s.getOrCreateCronScheduler(namespace, timezone)
	if err != nil {
		return 0, err
	}

	// Validate schedule expression
	if err := s.validateScheduleExpression(schedule.Schedule); err != nil {
		return 0, fmt.Errorf("invalid schedule expression: %w", err)
	}

	// Determine operation type for logging
	operationType := "unknown"
	if schedule.Scale != nil {
		operationType = "scale"
	} else if schedule.Suspend != nil && *schedule.Suspend {
		operationType = "suspend"
	} else if schedule.Resume != nil && *schedule.Resume {
		operationType = "resume"
	}

	// Add the job with callback
	var entryID cron.EntryID
	entryID, err = cronScheduler.AddFunc(schedule.Schedule, func() {
		s.executeWithTracking(namespace, int(entryID), operationType, func() {
			callback()
		})
	})

	if err != nil {
		return 0, fmt.Errorf("failed to add schedule: %w", err)
	}

	jobID := int(entryID)

	// Track the schedule
	s.mu.Lock()
	if s.scheduleMap[namespace][schedulerName] == nil {
		s.scheduleMap[namespace][schedulerName] = []int{}
	}
	s.scheduleMap[namespace][schedulerName] = append(s.scheduleMap[namespace][schedulerName], jobID)

	// Initialize execution tracking
	s.executionMap[namespace][jobID] = &JobExecution{
		ScheduleType: operationType,
	}
	s.mu.Unlock()

	return jobID, nil
}

// executeWithTracking executes a job and tracks execution information
func (s *TimezoneAwareScheduler) executeWithTracking(namespace string, jobID int, scheduleType string, fn func()) {
	now := time.Now()

	s.mu.Lock()
	execution := s.executionMap[namespace][jobID]
	if execution == nil {
		execution = &JobExecution{ScheduleType: scheduleType}
		s.executionMap[namespace][jobID] = execution
	}
	execution.LastRun = &now
	execution.ExecutionCount++
	s.mu.Unlock()

	// Execute the function and track result
	defer func() {
		if r := recover(); r != nil {
			s.mu.Lock()
			execution.LastResult = fmt.Sprintf("panic: %v", r)
			s.mu.Unlock()
			// Re-panic to let cron's recovery middleware handle it
			panic(r)
		}
	}()

	fn()

	s.mu.Lock()
	execution.LastResult = "success"
	s.mu.Unlock()
}

// RemoveSchedule removes a schedule by job ID
func (s *TimezoneAwareScheduler) RemoveSchedule(namespace, schedulerName string, jobID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find and remove from schedule map
	if schedules, exists := s.scheduleMap[namespace][schedulerName]; exists {
		for i, id := range schedules {
			if id == jobID {
				s.scheduleMap[namespace][schedulerName] = append(schedules[:i], schedules[i+1:]...)
				break
			}
		}
	}

	// Remove from all cron schedulers in the namespace
	if schedulers, exists := s.schedulers[namespace]; exists {
		for _, cronScheduler := range schedulers {
			cronScheduler.Remove(cron.EntryID(jobID))
		}
	}

	// Remove execution tracking
	if executions, exists := s.executionMap[namespace]; exists {
		delete(executions, jobID)
	}

	return nil
}

// RemoveAllSchedules removes all schedules for a scheduler
func (s *TimezoneAwareScheduler) RemoveAllSchedules(namespace, schedulerName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	schedules, exists := s.scheduleMap[namespace][schedulerName]
	if !exists {
		return nil
	}

	// Remove all jobs from cron schedulers
	if schedulers, exists := s.schedulers[namespace]; exists {
		for _, cronScheduler := range schedulers {
			for _, jobID := range schedules {
				cronScheduler.Remove(cron.EntryID(jobID))
			}
		}
	}

	// Remove execution tracking
	if executions, exists := s.executionMap[namespace]; exists {
		for _, jobID := range schedules {
			delete(executions, jobID)
		}
	}

	// Clear the schedule map for this scheduler
	delete(s.scheduleMap[namespace], schedulerName)

	return nil
}

// GetNextRun gets the next run time for a job ID
func (s *TimezoneAwareScheduler) GetNextRun(namespace, schedulerName string, jobID int) (*time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	schedulers, exists := s.schedulers[namespace]
	if !exists {
		return nil, fmt.Errorf("no schedulers found for namespace %s", namespace)
	}

	// Search through all timezone schedulers
	for _, cronScheduler := range schedulers {
		entry := cronScheduler.Entry(cron.EntryID(jobID))
		if entry.ID != 0 {
			nextRun := entry.Next
			return &nextRun, nil
		}
	}

	return nil, fmt.Errorf("job %d not found", jobID)
}

// GetActiveSchedules returns all active schedules for a namespace
func (s *TimezoneAwareScheduler) GetActiveSchedules(namespace string) map[string][]ScheduleInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]ScheduleInfo)

	schedulers, exists := s.schedulers[namespace]
	if !exists {
		return result
	}

	for schedulerName, jobIDs := range s.scheduleMap[namespace] {
		var scheduleInfos []ScheduleInfo

		for _, jobID := range jobIDs {
			// Find the job across all timezone schedulers
			for timezone, cronScheduler := range schedulers {
				entry := cronScheduler.Entry(cron.EntryID(jobID))
				if entry.ID != 0 {
					scheduleType := "unknown"
					if exec, exists := s.executionMap[namespace][jobID]; exists {
						scheduleType = exec.ScheduleType
					}

					scheduleInfos = append(scheduleInfos, ScheduleInfo{
						JobID:         jobID,
						Schedule:      fmt.Sprintf("%s (%s)", entry.Schedule, timezone),
						NextRun:       entry.Next,
						SchedulerName: schedulerName,
						ScheduleType:  scheduleType,
					})
					break
				}
			}
		}

		if len(scheduleInfos) > 0 {
			result[schedulerName] = scheduleInfos
		}
	}

	return result
}

// Shutdown gracefully shuts down all schedulers
func (s *TimezoneAwareScheduler) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop all cron schedulers
	for namespace, schedulers := range s.schedulers {
		for timezone, cronScheduler := range schedulers {
			cronScheduler.Stop()
			delete(schedulers, timezone)
		}
		delete(s.schedulers, namespace)
	}

	// Clear all tracking data
	s.scheduleMap = make(map[string]map[string][]int)
	s.executionMap = make(map[string]map[int]*JobExecution)

	return nil
}

// validateScheduleExpression validates a cron schedule expression
func (s *TimezoneAwareScheduler) validateScheduleExpression(schedule string) error {
	// Use the standard 5-field parser (minute, hour, day, month, day-of-week)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(schedule)
	return err
}
