package scheduler

import (
	"log"

	"github.com/robfig/cron/v3"
)

var cronRunner *cron.Cron

// InitCron starts the cron scheduler
func InitCron() {
	cronRunner = cron.New(cron.WithSeconds())
	cronRunner.Start()
	log.Println("Cron scheduler started")
}

// AddJob adds a shell command to be executed periodically
func AddJob(spec string, tag string, cmd func()) (cron.EntryID, error) {
	if cronRunner == nil {
		InitCron()
	}
	id, err := cronRunner.AddFunc(spec, cmd)
	return id, err
}

// RemoveJob removes a scheduled job
func RemoveJob(id cron.EntryID) {
	if cronRunner != nil {
		cronRunner.Remove(id)
	}
}
