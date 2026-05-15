package cli

import "time"

// timeFormat renders a Unix-seconds timestamp in a humans-can-eyeball way:
//
//	"2026-05-15 12:47"
//
// Kept in its own file so the table renderer doesn't pull in the whole time
// package at the top, and so callers can swap in a test clock if needed.
func timeFormat(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02 15:04")
}
