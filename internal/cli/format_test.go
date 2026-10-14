package cli_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/cli"
	"github.com/vsem-azamat/agora/internal/rooms"
)

// pb gives m as the hub hands it to a client, field by field as the hub's conversion does.
func pb(m rooms.Message) *agorav1.Message {
	states := map[rooms.Delivery]agorav1.DeliveryState{
		rooms.Scheduled: agorav1.DeliveryState_DELIVERY_STATE_SCHEDULED, rooms.Sending: agorav1.DeliveryState_DELIVERY_STATE_SENDING,
		rooms.Sent: agorav1.DeliveryState_DELIVERY_STATE_SENT, rooms.Cancelled: agorav1.DeliveryState_DELIVERY_STATE_CANCELLED,
		rooms.Failed: agorav1.DeliveryState_DELIVERY_STATE_FAILED, rooms.Unconfirmed: agorav1.DeliveryState_DELIVERY_STATE_UNCONFIRMED,
	}
	out := &agorav1.Message{
		Id: m.ID, Room: m.Room, Author: m.Author, Body: m.Body, ReplyTo: m.ReplyTo, At: timestamppb.New(m.At), Addressed: m.Addressed,
		DeliveryState: states[m.Delivery], DeliveryError: m.DeliveryError, Note: m.Note, Incoming: m.Incoming,
		Thread: m.Thread, Also: m.Also, ThreadTitle: m.ThreadTitle, SentOut: m.SentOut,
	}
	if sum := m.Summary; sum.Replies > 0 {
		out.ThreadSummary = &agorav1.ThreadSummary{Replies: int32(sum.Replies), Repliers: sum.Repliers, Unread: int32(sum.Unread), LastAt: timestamppb.New(sum.LastAt)}
	}
	if m.External() {
		out.ExternalAuthor = &agorav1.ExternalAuthor{Bridge: m.Room, Id: m.ExtAuthorID, Name: m.ExtAuthorName}
	}
	return out
}

// Both callers, the hub's rooms.Format and the CLI's formatting of the API message, lay a message
// out the same way: every mark, alone and together.
func TestTheHubAndTheCLIFormatEveryMarkAlike(t *testing.T) {
	at := time.Date(2026, 10, 9, 14, 2, 0, 0, time.Local)
	base := rooms.Message{ID: 41, Room: "example-chat", Author: "secretary", Body: "first line\nsecond line", At: at}
	with := func(f func(*rooms.Message)) rooms.Message { m := base; f(&m); return m }
	cases := []struct {
		name string
		m    rooms.Message
		mark string // what the header must carry
	}{
		{"plain", base, ""},
		{"a reply", with(func(m *rooms.Message) { m.ReplyTo = 40 }), " · re 40"},
		{"to you", with(func(m *rooms.Message) { m.Addressed = true }), " · to you"},
		{"from outside", with(func(m *rooms.Message) { m.Author, m.ExtAuthorID, m.ExtAuthorName, m.Incoming = "", "42", "Ada", true }), "Ada@example-chat"},
		{"the operator writing outside", with(func(m *rooms.Message) { m.Author, m.Incoming = "owner", true }), " · from outside"},
		{"a note", with(func(m *rooms.Message) { m.Note = true }), " · note"},
		{"a note sent out", with(func(m *rooms.Message) { m.Note, m.SentOut = true, 42 }), " · note · sent out as 42"},
		{"scheduled", with(func(m *rooms.Message) { m.Delivery = rooms.Scheduled }), " · scheduled"},
		{"sending", with(func(m *rooms.Message) { m.Delivery = rooms.Sending }), " · sending"},
		{"sent", with(func(m *rooms.Message) { m.Delivery = rooms.Sent }), " · sent"},
		{"cancelled", with(func(m *rooms.Message) { m.Delivery = rooms.Cancelled }), " · cancelled"},
		{"failed", with(func(m *rooms.Message) { m.Delivery, m.DeliveryError = rooms.Failed, "chat not found" }), " · not sent: chat not found"},
		{"unconfirmed", with(func(m *rooms.Message) { m.Delivery, m.DeliveryError = rooms.Unconfirmed, "the bridge was removed" }), " · unconfirmed: the bridge was removed"},
		{"a reply in a thread", with(func(m *rooms.Message) { m.Thread = 40 }), " · in thread 40"},
		{"a reply in a titled thread", with(func(m *rooms.Message) { m.Thread, m.ThreadTitle = 40, "Release 2.4" }), " · in thread 40 «Release 2.4»"},
		{"a titled root", with(func(m *rooms.Message) { m.ThreadTitle = "Release 2.4" }), " · thread «Release 2.4»"},
		{"a root with a reply", with(func(m *rooms.Message) {
			m.Summary = rooms.ThreadSummary{Replies: 1, Repliers: []string{"builder"}, LastAt: at}
		}), " · thread: 1 reply (builder), last 14:02"},
		{"a root with replies, titled, unread", with(func(m *rooms.Message) {
			m.ThreadTitle = "Release 2.4"
			m.Summary = rooms.ThreadSummary{Replies: 3, Repliers: []string{"builder", "tester"}, LastAt: at, Unread: 1}
		}), " · thread «Release 2.4»: 3 replies (builder, tester), last 14:02, 1 unread"},
		{"everything at once", with(func(m *rooms.Message) {
			m.ReplyTo, m.Addressed, m.Note, m.SentOut, m.Delivery, m.DeliveryError = 40, true, true, 42, rooms.Failed, "x"
			m.Thread, m.ThreadTitle = 40, "T"
		}), " · re 40 · to you · note · sent out as 42 · not sent: x · in thread 40 «T»"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hub, client := rooms.Format(c.m, 0), cli.FormatMessage(pb(c.m))
			if hub != client {
				t.Fatalf("the hub formats\n%q\nthe CLI\n%q", hub, client)
			}
			if head, _, _ := strings.Cut(client, "\n"); !strings.Contains(head, c.mark) {
				t.Fatalf("the header %q lacks %q", head, c.mark)
			}
			if !strings.HasSuffix(client, "\n  first line\n  second line") {
				t.Fatalf("the body: %q", client)
			}
		})
	}
}
