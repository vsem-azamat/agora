package hub

import (
	"context"
	"time"

	"github.com/vsem-azamat/agora/internal/pullrequests"
)

// watchLoop looks up the pull requests of active agents WatchFirst after start and then every
// WatchEvery, and fires the change signal after each round.
func (h *Hub) watchLoop(ctx context.Context) {
	w := pullrequests.New(h.db, h.agents, h.rooms, h.Forges, h.log)
	t := time.NewTimer(h.WatchFirst)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Round(ctx)
			h.changes.fire() // found pull requests and CI states may have changed even without a message
			t.Reset(h.WatchEvery)
		}
	}
}
