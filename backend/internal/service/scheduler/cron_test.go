package scheduler

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func TestValidateScheduleAcceptsStandardExpressions(t *testing.T) {
	cases := []string{
		"* * * * *",
		"*/5 * * * *",
		"0 0 * * *",
		"0 0 1 1 *",
	}
	for _, c := range cases {
		if err := ValidateSchedule(c); err != nil {
			t.Fatalf("expected %q to be valid, got %v", c, err)
		}
	}
}

func TestReloadFromDBReplacesInMemoryJobs(t *testing.T) {
	dsn := fmt.Sprintf("file:scheduler_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CronJob{}); err != nil {
		t.Fatal(err)
	}
	previous := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = previous })

	first := model.CronJob{Name: "first", Schedule: "0 0 * * *", Command: "true", Enable: true}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	s := NewScheduler()
	if err := s.LoadFromDB(); err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Stop)

	if err := db.Model(&first).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	second := model.CronJob{ID: 99, Name: "second", Schedule: "5 0 * * *", Command: "true", Enable: true}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ReloadFromDB(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) != 1 {
		t.Fatalf("jobs=%d, want 1", len(s.jobs))
	}
	if _, exists := s.jobs[first.ID]; exists {
		t.Fatal("removed cron job remained scheduled")
	}
	if _, exists := s.jobs[second.ID]; !exists {
		t.Fatal("restored cron job was not scheduled")
	}
}

func TestValidateScheduleRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"invalid",
		"99 * * * *",
		"* * * *", // missing a field
		"hello",
	}
	for _, c := range cases {
		if err := ValidateSchedule(c); err == nil {
			t.Fatalf("expected %q to be invalid, got nil error", c)
		}
	}
}
