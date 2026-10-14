package cli

import (
	"fmt"
	"io"
	"time"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/rooms"
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

// deliveryStates maps the API's delivery state back to the hub's, for rooms.Format.
var deliveryStates = map[agorav1.DeliveryState]rooms.Delivery{
	agorav1.DeliveryState_DELIVERY_STATE_SCHEDULED:   rooms.Scheduled,
	agorav1.DeliveryState_DELIVERY_STATE_SENDING:     rooms.Sending,
	agorav1.DeliveryState_DELIVERY_STATE_SENT:        rooms.Sent,
	agorav1.DeliveryState_DELIVERY_STATE_CANCELLED:   rooms.Cancelled,
	agorav1.DeliveryState_DELIVERY_STATE_FAILED:      rooms.Failed,
	agorav1.DeliveryState_DELIVERY_STATE_UNCONFIRMED: rooms.Unconfirmed,
}

// formatMessage renders a message as the hub does the messages it delivers into an agent's
// context: it gives the fields of the API message back to rooms.Format, which is the one place
// that lays out a message's header (room, identifier, author, time, reply, to you, from outside or
// note, delivery state, thread) and body.
func formatMessage(m *agorav1.Message) string {
	msg := rooms.Message{
		ID: m.GetId(), Room: m.GetRoom(), Author: m.GetAuthor(), Body: m.GetBody(), ReplyTo: m.GetReplyTo(),
		At: m.GetAt().AsTime(), Addressed: m.GetAddressed(), Delivery: deliveryStates[m.GetDeliveryState()],
		DeliveryError: m.GetDeliveryError(), Note: m.GetNote(), Incoming: m.GetIncoming(), SentOut: m.GetSentOut(),
		Thread: m.GetThread(), ThreadTitle: m.GetThreadTitle(),
	}
	if ext := m.GetExternalAuthor(); ext != nil {
		msg.ExtAuthorID, msg.ExtAuthorName = ext.GetId(), ext.GetName()
	}
	if sum := m.GetThreadSummary(); sum != nil {
		msg.Summary = rooms.ThreadSummary{Replies: int(sum.GetReplies()), Repliers: sum.GetRepliers(), LastAt: sum.GetLastAt().AsTime(), Unread: int(sum.GetUnread())}
	}
	return rooms.Format(msg, 0)
}
