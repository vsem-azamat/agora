package cli_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/cli"
)

func TestMessageLayout(t *testing.T) {
	at := time.Date(2026, 10, 9, 22, 50, 0, 0, time.Local)
	m := &agorav1.Message{Id: 12, Room: "example-app", Author: "builder", Body: "merged #57\nthanks", ReplyTo: 9, At: timestamppb.New(at), Addressed: true}
	want := "#example-app [12] builder · 22:50 · re 9 · to you\n  merged #57\n  thanks"
	if got := cli.FormatMessage(m); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	ext := &agorav1.Message{
		Id: 14, Room: "example-chat", Body: "can you look?", At: timestamppb.New(at),
		ExternalAuthor: &agorav1.ExternalAuthor{Bridge: "example-chat", Id: "42", Name: "Ada"},
	}
	if got := cli.FormatMessage(ext); got != "#example-chat [14] Ada@example-chat · 22:50\n  can you look?" {
		t.Fatalf("got %q", got)
	}
	failed := &agorav1.Message{
		Id: 15, Room: "example-chat", Author: "secretary", Body: "hi", At: timestamppb.New(at),
		DeliveryState: agorav1.DeliveryState_DELIVERY_STATE_FAILED, DeliveryError: "chat not found",
	}
	if got := cli.FormatMessage(failed); got != "#example-chat [15] secretary · 22:50 · not sent: chat not found\n  hi" {
		t.Fatalf("got %q", got)
	}
	unconfirmed := &agorav1.Message{
		Id: 18, Room: "example-chat", Author: "secretary", Body: "hi", At: timestamppb.New(at),
		DeliveryState: agorav1.DeliveryState_DELIVERY_STATE_UNCONFIRMED, DeliveryError: "the bridge was removed before it answered",
	}
	if got := cli.FormatMessage(unconfirmed); got != "#example-chat [18] secretary · 22:50 · unconfirmed: the bridge was removed before it answered\n  hi" {
		t.Fatalf("got %q", got)
	}
	scheduled := &agorav1.Message{Id: 16, Room: "example-chat", Author: "secretary", Body: "hi", At: timestamppb.New(at), DeliveryState: agorav1.DeliveryState_DELIVERY_STATE_SCHEDULED}
	if got := cli.FormatMessage(scheduled); got != "#example-chat [16] secretary · 22:50 · scheduled\n  hi" {
		t.Fatalf("got %q", got)
	}
	cancelled := &agorav1.Message{Id: 19, Room: "example-chat", Author: "secretary", Body: "hi", At: timestamppb.New(at), DeliveryState: agorav1.DeliveryState_DELIVERY_STATE_CANCELLED}
	if got := cli.FormatMessage(cancelled); got != "#example-chat [19] secretary · 22:50 · cancelled\n  hi" {
		t.Fatalf("got %q", got)
	}
	note := &agorav1.Message{Id: 17, Room: "example-chat", Author: "secretary", Body: "a draft", At: timestamppb.New(at), Note: true}
	if got := cli.FormatMessage(note); got != "#example-chat [17] secretary · 22:50 · note\n  a draft" {
		t.Fatalf("got %q", got)
	}
	note.SentOut = 18
	if got := cli.FormatMessage(note); got != "#example-chat [17] secretary · 22:50 · note · sent out as 18\n  a draft" {
		t.Fatalf("a note sent out: %q", got)
	}
	note.SentOut = 0
	in := &agorav1.Message{Id: 18, Room: "example-chat", Author: "owner", Body: "checking", At: timestamppb.New(at), Incoming: true}
	if got := cli.FormatMessage(in); got != "#example-chat [18] owner · 22:50 · from outside\n  checking" {
		t.Fatalf("got %q", got)
	}
	plain := &agorav1.Message{Id: 13, Room: "general", Author: "agora", Body: "hi", At: timestamppb.New(at)}
	if got := cli.FormatMessage(plain); got != "#general [13] agora · 22:50\n  hi" {
		t.Fatalf("got %q", got)
	}
}
