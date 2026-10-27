package notify

// reloadRequests lets configuration-changing handlers wake the notification
// supervisor immediately. The channel is deliberately buffered and the send
// is non-blocking: multiple setting changes coalesce into one reload and an
// HTTP request never waits for the background poller.
var reloadRequests = make(chan struct{}, 1)

// TriggerReload asks the notification supervisor to re-read its settings.
func TriggerReload() {
	select {
	case reloadRequests <- struct{}{}:
	default:
	}
}

// ReloadRequests exposes the process-local wake-up stream to main's lifecycle
// loop. Consumers should normally only receive from it.
func ReloadRequests() <-chan struct{} { return reloadRequests }
