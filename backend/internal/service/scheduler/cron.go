package scheduler

import (
	"log"
	"os/exec"
	"sync"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/robfig/cron/v3"
)

// Scheduler manages cron jobs backed by the database
type Scheduler struct {
	cron *cron.Cron
	jobs map[uint]cron.EntryID
	mu   sync.Mutex
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
	s.cron.Start()
	log.Println("Cron scheduler started")
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop() {
	s.cron.Stop()
	log.Println("Cron scheduler stopped")
}

// LoadFromDB loads all enabled jobs from the database
func (s *Scheduler) LoadFromDB() error {
	var jobs []model.CronJob
	if err := config.DB.Where("enable = ?", true).Find(&jobs).Error; err != nil {
		return err
	}
	for _, job := range jobs {
		cmd := job.Command
		entryID, err := s.cron.AddFunc(job.Schedule, func() {
			out, err := exec.Command("bash", "-c", cmd).CombinedOutput()
			if err != nil {
				log.Printf("Cron job error: %v, output: %s", err, string(out))
			}
		})
		if err != nil {
			log.Printf("Failed to load cron job %d (%s): %v", job.ID, job.Name, err)
			continue
		}
		s.mu.Lock()
		s.jobs[job.ID] = entryID
		s.mu.Unlock()
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
	if err := config.DB.Create(&job).Error; err != nil {
		return 0, err
	}

	if job.Enable {
		cmd := job.Command
		entryID, err := s.cron.AddFunc(job.Schedule, func() {
			out, err := exec.Command("bash", "-c", cmd).CombinedOutput()
			if err != nil {
				log.Printf("Cron job error: %v, output: %s", err, string(out))
			}
		})
		if err != nil {
			// Validate already ran; this would be an unexpected scheduler failure.
			return job.ID, err
		}
		s.mu.Lock()
		s.jobs[job.ID] = entryID
		s.mu.Unlock()
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
