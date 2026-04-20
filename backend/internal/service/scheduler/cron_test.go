package scheduler

import "testing"

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
