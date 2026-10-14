package cli

import (
	"fmt"
	"io"
	"strings"
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

// deliveryLabels follow the head of a message of a bridged room, as in rooms.Format.
var deliveryLabels = map[agorav1.DeliveryState]string{
	agorav1.DeliveryState_DELIVERY_STATE_SCHEDULED: " · scheduled",
	agorav1.DeliveryState_DELIVERY_STATE_SENDING:   " · sending",
	agorav1.DeliveryState_DELIVERY_STATE_SENT:      " · sent",
	agorav1.DeliveryState_DELIVERY_STATE_CANCELLED: " · cancelled",
	agorav1.DeliveryState_DELIVERY_STATE_FAILED:    " · not sent: ",
}

// formatMessage renders a message: room, identifier, author (name@bridge for a message from
// outside), time, reply reference, whether it came from outside or is a note, delivery state and thread, then the body indented. It is the same
// layout as rooms.Format, which the hub uses for the messages it delivers into an agent's
// context; keep the two alike.
func formatMessage(m *agorav1.Message) string {
	author := m.GetAuthor()
	if ext := m.GetExternalAuthor(); ext != nil {
		author = ext.GetName() + "@" + ext.GetBridge()
	}
	head := fmt.Sprintf("#%s [%d] %s · %s", m.GetRoom(), m.GetId(), author, shortClock(m.GetAt().AsTime()))
	if m.GetReplyTo() != 0 {
		head += fmt.Sprintf(" · re %d", m.GetReplyTo())
	}
	if m.GetAddressed() {
		head += " · to you"
	}
	switch {
	case m.GetIncoming():
		head += " · from outside"
	case m.GetNote():
		head += " · note"
	}
	head += deliveryLabels[m.GetDeliveryState()]
	if m.GetDeliveryState() == agorav1.DeliveryState_DELIVERY_STATE_FAILED {
		head += m.GetDeliveryError()
	}
	head += threadLabel(m)
	return head + "\n  " + strings.ReplaceAll(m.GetBody(), "\n", "\n  ")
}

// threadLabel follows a message's head as rooms.Format's thread label does: the thread a reply is
// in, or the thread of a root.
func threadLabel(m *agorav1.Message) string {
	title := m.GetThreadTitle()
	if title != "" {
		title = " «" + title + "»"
	}
	if m.GetThread() != 0 {
		return fmt.Sprintf(" · in thread %d%s", m.GetThread(), title)
	}
	sum := m.GetThreadSummary()
	if sum.GetReplies() == 0 {
		if title == "" {
			return ""
		}
		return " · thread" + title
	}
	label := fmt.Sprintf(" · thread%s: %d repl%s (%s), last %s", title, sum.GetReplies(), map[bool]string{true: "y", false: "ies"}[sum.GetReplies() == 1],
		strings.Join(sum.GetRepliers(), ", "), shortClock(sum.GetLastAt().AsTime()))
	if sum.GetUnread() > 0 {
		label += fmt.Sprintf(", %d unread", sum.GetUnread())
	}
	return label
}
