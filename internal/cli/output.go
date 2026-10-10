package cli

import (
	"fmt"
	"io"
	"time"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

func clock(t time.Time) string { return t.Local().Format("15:04:05") }

func shortClock(t time.Time) string { return t.Local().Format("15:04") }

func dateTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return ": " + note
}

func printEntry(w io.Writer, e *agorav1.Entry) {
	switch e.GetState() {
	case agorav1.EntryState_ENTRY_STATE_HELD:
		fmt.Fprintf(w, "holding %s until %s\n", e.GetKey(), clock(e.GetExpiresAt().AsTime()))
	case agorav1.EntryState_ENTRY_STATE_OFFERED:
		fmt.Fprintf(w, "your turn on %s: claim it by %s with `agora queue renew %s`\n", e.GetKey(), clock(e.GetExpiresAt().AsTime()), e.GetKey())
	case agorav1.EntryState_ENTRY_STATE_WAITING:
		fmt.Fprintf(w, "waiting for %s at position %d\n", e.GetKey(), e.GetPosition())
	}
}

func printResource(w io.Writer, r *agorav1.Resource) {
	fmt.Fprintf(w, "%s (%d slot%s)\n", r.GetKey(), r.GetSlots(), plural(r.GetSlots()))
	for _, e := range r.GetEntries() {
		switch e.GetState() {
		case agorav1.EntryState_ENTRY_STATE_HELD:
			fmt.Fprintf(w, "  held     %-16s until %s%s\n", e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote()))
		case agorav1.EntryState_ENTRY_STATE_OFFERED:
			fmt.Fprintf(w, "  offered  %-16s claim by %s%s\n", e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote()))
		default:
			fmt.Fprintf(w, "  #%-7d %-16s since %s%s\n", e.GetPosition(), e.GetAgent(), clock(e.GetJoinedAt().AsTime()), noteSuffix(e.GetNote()))
		}
	}
}

func plural(n int32) string {
	if n == 1 {
		return ""
	}
	return "s"
}
