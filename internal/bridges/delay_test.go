package bridges_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

// fakeClock is a clock tests move by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newDelayEnv is newEnv on a clock the test moves, with the bridge's policy delay and a delay of
// a minute.
func newDelayEnv(t *testing.T) (env, *fakeClock) {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &fakeClock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	r := rooms.New(db, c.now)
	b := bridges.New(db, r, c.now)
	b.Delay = time.Minute
	e := env{b: b, r: r, s: sessions.New(db, queue.New(db, c.now), r, c.now)}
	for _, n := range []string{"secretary", "builder", "owner"} {
		if _, err := e.s.Join(ctx, n, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.b.Add(ctx, "builder", "example-chat", "example-bridge --chat 42", "", []string{"secretary"}); err != nil {
		t.Fatal(err)
	}
	return e, c
}

func TestTheHubsDefaultPolicy(t *testing.T) {
	e := newEnv(t)
	if l, _ := e.b.List(ctx); l[0].Policy != bridges.Delay {
		t.Fatalf("default policy %q", l[0].Policy)
	}
	e.b.Policy = bridges.Open
	if _, err := e.b.Add(ctx, "builder", "example-feed", "example-bridge --feed", "", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.b.Get(ctx, "example-feed"); b.Policy != bridges.Open {
		t.Fatalf("policy of a bridge added under open %q", b.Policy)
	}
	if err := e.b.SetPolicy(ctx, "example-feed", "approve"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("the former policy approve: %v", err)
	}
}

func TestScheduledMessagesGoOutOnceTheDelayPassed(t *testing.T) {
	e, c := newDelayEnv(t)
	first := e.post(t, "secretary", "example-chat", "looked, all fine")
	c.add(20 * time.Second)
	second := e.post(t, "secretary", "example-chat", "and the export too")
	if m := e.history(t)[0]; m.Delivery != rooms.Scheduled || !m.ScheduledAt.Equal(c.now().Add(-20*time.Second)) {
		t.Fatalf("scheduled message: %+v", m)
	}
	if moved, err := e.b.SendDue(ctx, "example-chat"); moved || err != nil {
		t.Fatalf("before the delay: %v %v", moved, err)
	}
	c.add(40 * time.Second)
	if moved, err := e.b.SendDue(ctx, "example-chat"); !moved || err != nil {
		t.Fatalf("after the delay of the first: %v %v", moved, err)
	}
	if deliveryOf(t, e, "example-chat", first) != rooms.Sending || deliveryOf(t, e, "example-chat", second) != rooms.Scheduled {
		t.Fatalf("history %+v", e.history(t))
	}
	// under read nothing goes out, also once the delay passed; it goes when the policy opens again
	setPolicy(t, e, bridges.Read)
	c.add(time.Hour)
	if moved, _ := e.b.SendDue(ctx, "example-chat"); moved || deliveryOf(t, e, "example-chat", second) != rooms.Scheduled {
		t.Fatal("a scheduled message went out under read")
	}
	setPolicy(t, e, bridges.Open)
	if moved, _ := e.b.SendDue(ctx, "example-chat"); !moved || deliveryOf(t, e, "example-chat", second) != rooms.Sending {
		t.Fatal("the scheduled message did not go out once the room opened again")
	}
	if line := rooms.Format(e.history(t)[0], 0); !strings.Contains(line, "· sending") {
		t.Fatalf("format %q", line)
	}
}

func TestScheduledShowsToAgents(t *testing.T) {
	e, _ := newDelayEnv(t)
	e.post(t, "secretary", "example-chat", "looked, all fine")
	cancel := e.post(t, "secretary", "example-chat", "something rude")
	if err := e.b.CancelScheduled(ctx, cancel, "owner"); err != nil {
		t.Fatal(err)
	}
	h := e.history(t)
	if !strings.Contains(rooms.Format(h[0], 0), " · scheduled\n") || !strings.Contains(rooms.Format(h[1], 0), " · cancelled\n") {
		t.Fatalf("formats %q / %q", rooms.Format(h[0], 0), rooms.Format(h[1], 0))
	}
	if h[2].Body != "@secretary your message "+itoa(cancel)+" was not sent: owner cancelled it." {
		t.Fatalf("board %+v", h[2])
	}
	if err := e.b.SendScheduled(ctx, cancel); !errors.Is(err, bridges.ErrNotFound) || err.Error() != "message "+itoa(cancel)+" is not scheduled" {
		t.Fatalf("sending a cancelled message: %v", err)
	}
}

func TestTheOperatorSendsANoteOut(t *testing.T) {
	e, _ := newDelayEnv(t)
	in := e.receive(t, bridges.In{ID: "5513", AuthorName: "Ada", Text: "is it fixed?"})
	note, err := e.r.PostNote(ctx, "secretary", "example-chat", "Draft: the fix ships tonight", in)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.b.SendNoteOut(ctx, note)
	if err != nil {
		t.Fatal(err)
	}
	h := e.history(t)
	last := h[len(h)-1]
	if last.ID != id || last.Author != "secretary" || last.Body != "Draft: the fix ships tonight" || last.ReplyTo != in || last.Note || last.Delivery != rooms.Sending {
		t.Fatalf("sent out: %+v", last)
	}
	if h[1].ID != note || !h[1].Note || h[1].Delivery != "" {
		t.Fatalf("the note changed: %+v", h[1])
	}
	outs, _ := outgoing(t, e)
	if len(outs) != 1 || outs[0].ID != id || outs[0].ReplyTo != "5513" {
		t.Fatalf("outgoing %+v", outs)
	}
	// only notes, and nothing under read or without a bridge
	if _, err := e.b.SendNoteOut(ctx, id); !errors.Is(err, bridges.ErrNotFound) || err.Error() != "message "+itoa(id)+" is not a note" {
		t.Fatalf("sending out a message that is no note: %v", err)
	}
	setPolicy(t, e, bridges.Read)
	if _, err := e.b.SendNoteOut(ctx, note); !errors.Is(err, rooms.ErrReadOnly) {
		t.Fatalf("sending a note out under read: %v", err)
	}
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.b.SendNoteOut(ctx, note); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("sending a note out without a bridge: %v", err)
	}
}

func TestANoteInAThreadGoesOutInTheThread(t *testing.T) {
	e, _ := newDelayEnv(t)
	root := e.receive(t, bridges.In{ID: "5513", AuthorName: "Ada", Text: "is it fixed?"})
	for _, also := range []bool{false, true} {
		note, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "Draft: the fix ships tonight", Thread: root, Also: also, Note: true})
		if err != nil {
			t.Fatal(err)
		}
		id, err := e.b.SendNoteOut(ctx, note)
		if err != nil {
			t.Fatal(err)
		}
		th, err := e.r.ThreadHistory(ctx, root, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if outs, _ := outgoing(t, e); len(outs) == 0 || outs[len(outs)-1].ID != id || outs[len(outs)-1].ReplyTo != "5513" {
			t.Fatalf("also=%v: outgoing %+v", also, outs)
		}
		var sent, kept *rooms.Message
		for i := range th {
			switch th[i].ID {
			case id:
				sent = &th[i]
			case note:
				kept = &th[i]
			}
		}
		if sent == nil || sent.Thread != root || sent.Also != also || sent.Note || sent.Delivery != rooms.Sending {
			t.Fatalf("also=%v: the copy is not in the thread: %+v", also, sent)
		}
		if kept == nil || kept.Thread != root || kept.Also != also || !kept.Note {
			t.Fatalf("also=%v: the note changed: %+v", also, kept)
		}
	}
}

func TestSendingANoteOutKeepsItsAuthorOutOfAThreadItLeft(t *testing.T) {
	e, _ := newDelayEnv(t)
	root := e.receive(t, bridges.In{ID: "5513", AuthorName: "Ada", Text: "is it fixed?"})
	note, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "Draft: yes", Thread: root, Note: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.LeaveThread(ctx, "secretary", root); err != nil {
		t.Fatal(err)
	}
	if _, err := e.b.SendNoteOut(ctx, note); err != nil {
		t.Fatal(err)
	}
	reply, err := e.r.Send(ctx, rooms.Posting{Author: "owner", Room: "example-chat", Body: "thanks", Thread: root})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := e.r.Unread(ctx, "secretary", rooms.Everything, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.ID == reply {
			t.Fatalf("sending the note out brought secretary back into the thread it left: %+v", m)
		}
	}
}

func TestAddingABridgeAgainStartsTheDelayAgain(t *testing.T) {
	e, c := newDelayEnv(t)
	id := e.post(t, "secretary", "example-chat", "something rude")
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	c.add(2 * time.Minute)
	if _, err := e.b.Add(ctx, "builder", "example-chat", "example-bridge --chat 42", "", nil); err != nil {
		t.Fatal(err)
	}
	if moved, _ := e.b.SendDue(ctx, "example-chat"); moved || deliveryOf(t, e, "example-chat", id) != rooms.Scheduled {
		t.Fatal("a message left scheduled went out as soon as the room was bridged again")
	}
	c.add(time.Minute)
	if moved, _ := e.b.SendDue(ctx, "example-chat"); !moved {
		t.Fatal("the message did not go out once its new delay passed")
	}
}

func TestReadingCaps(t *testing.T) {
	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	long := strings.Repeat("x", 100)
	for _, c := range []struct {
		in   string
		want bridges.Caps
	}{
		{`{"personas":"prefix","threads":"topic","other":"x"}`, bridges.Caps{Personas: "prefix", Threads: "topic"}},
		{`{"personas":"` + long + `"}`, bridges.Caps{Personas: strings.Repeat("x", bridges.MaxCap)}},
		{"{\"personas\":\"a\\u202eb\\nc\"}", bridges.Caps{Personas: "a b c"}},
		{`{"personas":7,"threads":null}`, bridges.Caps{}},
		{`{}`, bridges.Caps{}},
	} {
		if got := bridges.ReadCaps(raw(c.in)); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.in, got, c.want)
		}
	}
}
