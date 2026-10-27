package scheduler

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/robfig/cron/v3"
)

// Scheduler manages cron jobs backed by the database
type Scheduler struct {
	cron *cron.Cron
	jobs map[uint]cron.EntryID
	mu   sync.Mutex
	run  bool
}

// NewScheduler creates a new Scheduler instance
func NewScheduler() *Scheduler {
	return &Scheduler{
		cron: cron.New(),
		jobs: make(map[uint]cron.EntryID),
	}
}

// Start begins executing scheduled jobs
func (s *Scheduler) Start() {
	s.mu.Lock()
	s.cron.Start()
	s.run = true
	s.mu.Unlock()
	log.Println("Cron scheduler started")
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	old := s.cron
	s.run = false
	s.mu.Unlock()
	old.Stop()
	log.Println("Cron scheduler stopped")
}

// LoadFromDB loads all enabled jobs from the database
func (s *Scheduler) LoadFromDB() error {
	return s.reloadFromDB()
}

// ReloadFromDB atomically replaces the in-memory schedule with the current DB
// rows. It is used after backup restore so removed jobs stop immediately and
// restored jobs do not wait for a panel restart.
func (s *Scheduler) ReloadFromDB() error {
	return s.reloadFromDB()
}

func (s *Scheduler) reloadFromDB() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var jobs []model.CronJob
	if err := config.DB.Where("enable = ?", true).Find(&jobs).Error; err != nil {
		return err
	}

	nextCron := cron.New()
	nextJobs := make(map[uint]cron.EntryID, len(jobs))
	for _, job := range jobs {
		cmd := job.Command
		timeout := job.Timeout
		entryID, err := nextCron.AddFunc(job.Schedule, func() {
			runCronCommand(cmd, timeout)
		})
		if err != nil {
			return fmt.Errorf("load cron job %d (%s): %w", job.ID, job.Name, err)
		}
		nextJobs[job.ID] = entryID
	}

	oldCron := s.cron
	wasRunning := s.run
	s.cron = nextCron
	s.jobs = nextJobs
	if wasRunning {
		nextCron.Start()
	}
	if oldCron != nil {
		oldCron.Stop()
	}
	log.Printf("Loaded %d cron jobs from database", len(jobs))
	return nil
}

// ValidateSchedule returns an error if the schedule expression can't be parsed
// by the standard cron parser. Callers should invoke this before persisting a
// job so the row only ever lands in the DB when it will actually run.
func ValidateSchedule(schedule string) error {
	_, err := cron.ParseStandard(schedule)
	return err
}

// AddJob creates a new cron job in DB and schedules it.
// The schedule is validated before any DB write so a bad expression can't
// leave the job persisted-but-never-running (previous behaviour returned
// success to the HTTP layer and silently skipped scheduling).
func (s *Scheduler) AddJob(job model.CronJob) (uint, error) {
	if err := ValidateSchedule(job.Schedule); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.DB.Create(&job).Error; err != nil {
		return 0, err
	}

	if job.Enable {
		cmd := job.Command
		timeout := job.Timeout
		entryID, err := s.cron.AddFunc(job.Schedule, func() {
			runCronCommand(cmd, timeout)
		})
		if err != nil {
			// Validate already ran; this would be an unexpected scheduler failure.
			return job.ID, err
		}
		s.jobs[job.ID] = entryID
	}

	return job.ID, nil
}

// RemoveJob deletes a cron job from the scheduler and DB
func (s *Scheduler) RemoveJob(id uint) error {
	s.mu.Lock()
	if entryID, ok := s.jobs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, id)
	}
	s.mu.Unlock()

	return config.DB.Unscoped().Delete(&model.CronJob{}, id).Error
}

// ListJobs returns all cron jobs from the database
func (s *Scheduler) ListJobs() ([]model.CronJob, error) {
	var jobs []model.CronJob
	if err := config.DB.Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// runCronCommand executes a bash command with an optional timeout (seconds).
// A timeout of 0 means no limit.
func runCronCommand(cmd string, timeoutSec int) {
	var execCmd *exec.Cmd
	if timeoutSec > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
		defer cancel()
		execCmd = exec.CommandContext(ctx, "bash", "-c", cmd)
	} else {
		execCmd = exec.Command("bash", "-c", cmd)
	}
	out, err := execCmd.CombinedOutput()
	if err != nil {
		log.Printf("Cron job error: %v, output: %s", err, string(out))
	}
}
