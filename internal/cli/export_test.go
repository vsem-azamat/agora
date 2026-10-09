package cli

import "time"

// SetHubGiveUp changes how long a wait tolerates an unreachable hub, for tests; the returned
// function restores it.
func SetHubGiveUp(d time.Duration) func() {
	old := hubGiveUp
	hubGiveUp = d
	return func() { hubGiveUp = old }
}
