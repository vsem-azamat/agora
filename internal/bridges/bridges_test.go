package bridges_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

type env struct {
	b *bridges.Bridges
	r *rooms.Rooms
	s *sessions.Sessions
}

// newEnv returns a board where secretary, builder and owner joined and the bridge example-chat
// was added by builder with secretary as its agent.
func newEnv(t *testing.T) env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := rooms.New(db, nil)
	e := env{b: bridges.New(db, r, nil), r: r, s: sessions.New(db, queue.New(db, nil), r, nil)}
	for _, n := range []string{"secretary", "builder", "owner"} {
		if _, err := e.s.Join(ctx, n, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.b.Add(ctx, "builder", "example-chat", "example-bridge --chat 42", "", []string{"secretary"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) history(t *testing.T) []rooms.Message {
	t.Helper()
	msgs, err := e.r.History(ctx, "example-chat", 100)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func (e env) receive(t *testing.T, in bridges.In) int64 {
	t.Helper()
	id, err := e.b.Receive(ctx, "example-chat", in, "owner")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e env) subscriptions(t *testing.T, agent string) []rooms.Subscription {
	t.Helper()
	subs, err := e.r.Subscriptions(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	return subs
}

func TestAddingABridge(t *testing.T) {
	e := newEnv(t)
	list, err := e.b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "example-chat" || list[0].Policy != bridges.Delay || list[0].CreatedBy != "builder" ||
		!slices.Equal(list[0].Addressees, []string{"secretary"}) || list[0].Command != "example-bridge --chat 42" {
		t.Fatalf("bridges: %+v", list)
	}
	rs, err := e.r.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rs, func(r rooms.Room) bool { return r.Name == "example-chat" && r.Purpose == bridges.DefaultPurpose }) {
		t.Fatalf("rooms: %+v", rs)
	}
	if !slices.Contains(e.subscriptions(t, "secretary"), rooms.Subscription{Room: "example-chat", Mode: rooms.ModeWake}) {
		t.Fatalf("secretary follows %v", e.subscriptions(t, "secretary"))
	}
}

func TestAttachingAnExistingRoom(t *testing.T) {
	e := newEnv(t)
	if err := e.r.Create(ctx, "example-team", "the team chat", "builder"); err != nil {
		t.Fatal(err)
	}
	before := e.post(t, "builder", "example-team", "hello before the bridge")
	created, err := e.b.Add(ctx, "builder", "example-team", "example-bridge", "ignored", nil)
	if err != nil || created {
		t.Fatalf("attach: %v %v", created, err)
	}
	msgs, err := e.r.History(ctx, "example-team", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != before {
		t.Fatalf("history after attaching: %+v", msgs)
	}
	rs, _ := e.r.List(ctx, false)
	if !slices.ContainsFunc(rs, func(r rooms.Room) bool { return r.Name == "example-team" && r.Purpose == "the team chat" }) {
		t.Fatalf("purpose changed: %+v", rs)
	}
}

func (e env) post(t *testing.T, author, room, body string) int64 {
	t.Helper()
	id, err := e.r.Post(ctx, author, room, body, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddingIsRefused(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, command string
		agents        []string
		want          error
	}{
		{"example-chat", "other-bridge", nil, bridges.ErrExists},
		{"general", "example-bridge", nil, store.ErrInvalid},
		{"example-new", " ", nil, store.ErrInvalid},
		{"example-new", "example-bridge", []string{"nobody"}, agents.ErrUnknown},
		{"Bad_Name", "example-bridge", nil, store.ErrInvalid},
	}
	for _, c := range cases {
		if _, err := e.b.Add(ctx, "builder", c.name, c.command, "", c.agents); !errors.Is(err, c.want) {
			t.Errorf("add %s %q %v: %v, want %v", c.name, c.command, c.agents, err, c.want)
		}
	}
	list, _ := e.b.List(ctx)
	if len(list) != 1 || list[0].Command != "example-bridge --chat 42" {
		t.Fatalf("bridges after refusals: %+v", list)
	}
	rs, _ := e.r.List(ctx, true)
	if slices.ContainsFunc(rs, func(r rooms.Room) bool { return r.Name == "example-new" }) {
		t.Fatal("a refused bridge left its room")
	}
}

func TestArchivingAndBridges(t *testing.T) {
	e := newEnv(t)
	if err := e.r.Archive(ctx, "builder", "example-chat", true); !errors.Is(err, rooms.ErrBridged) || !strings.Contains(err.Error(), "agora bridge remove example-chat") {
		t.Fatalf("archiving a bridged room: %v", err)
	}
	if err := e.r.Create(ctx, "old-chat", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "old-chat", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.b.Add(ctx, "builder", "old-chat", "example-bridge", "", []string{"secretary"}); !errors.Is(err, rooms.ErrArchived) || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("bridging an archived room: %v", err)
	}
	if list, _ := e.b.List(ctx); len(list) != 1 {
		t.Fatalf("bridges %+v", list)
	}
	// once its bridge is removed, a room can be archived, after what still waits to go out
	waiting, err := e.r.Post(ctx, "secretary", "example-chat", "a draft for the chat", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "example-chat", true); !errors.Is(err, rooms.ErrUndelivered) || !strings.Contains(err.Error(), "cancel it first") {
		t.Fatalf("archiving with a message waiting: %v", err)
	}
	if err := e.b.CancelScheduled(ctx, waiting, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "example-chat", true); err != nil {
		t.Fatal(err)
	}
}

// A message the operator posted while the bridge was down stays sending once the bridge is
// removed; nothing can settle it, and it does not hold archiving back.
func TestArchivingWithAMessageLeftSending(t *testing.T) {
	e := newEnv(t)
	if _, err := e.r.PostFromOperator(ctx, "owner", "example-chat", "on my way", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "example-chat", true); err != nil {
		t.Fatal(err)
	}
}

func TestRemovingKeepsTheRoom(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorID: "42", AuthorName: "Ada", Text: "hi"})
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	list, _ := e.b.List(ctx)
	if len(list) != 0 {
		t.Fatalf("bridges after removing: %+v", list)
	}
	h := e.history(t)
	if len(h) != 2 || h[0].Body != "hi" || h[1].Author != rooms.Board || !strings.Contains(h[1].Body, "no longer bridged") {
		t.Fatalf("history after removing: %+v", h)
	}
	if !slices.Contains(e.subscriptions(t, "secretary"), rooms.Subscription{Room: "example-chat", Mode: rooms.ModeWake}) {
		t.Fatal("subscription dropped")
	}
	if err := e.b.Remove(ctx, "builder", "example-chat"); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
	// agents post there again like in any room
	if _, err := e.r.Post(ctx, "builder", "example-chat", "still here", 0); err != nil {
		t.Fatal(err)
	}
}

func TestMessagesFromOutsideAreStoredOnce(t *testing.T) {
	e := newEnv(t)
	in := bridges.In{ID: "5513", AuthorID: "42", AuthorName: "Ada", Text: "can you look?", Cursor: "5513"}
	first := e.receive(t, in)
	if again := e.receive(t, in); first == 0 || again != 0 {
		t.Fatalf("ids %d then %d", first, again)
	}
	h := e.history(t)
	if len(h) != 1 || h[0].Author != "" || h[0].ExtAuthorName != "Ada" || h[0].ExtAuthorID != "42" || h[0].From() != "Ada@example-chat" {
		t.Fatalf("history: %+v", h)
	}
	if got := rooms.Format(h[0], 0); !strings.Contains(got, "] Ada@example-chat ·") {
		t.Fatalf("format: %q", got)
	}
	b, _ := e.b.Get(ctx, "example-chat")
	if b.Cursor != "5513" {
		t.Fatalf("cursor %q", b.Cursor)
	}
	e.receive(t, bridges.In{ID: "5514", AuthorID: "42", AuthorName: "Ada", Text: "no cursor here"})
	if b, _ := e.b.Get(ctx, "example-chat"); b.Cursor != "5513" {
		t.Fatalf("cursor after a line without one: %q", b.Cursor)
	}
}

func TestRepliesTimesAndLongTextFromOutside(t *testing.T) {
	e := newEnv(t)
	first := e.receive(t, bridges.In{ID: "5510", AuthorID: "42", AuthorName: "Ada", Text: "the contract"})
	at := time.Date(2026, 10, 10, 18, 2, 11, 0, time.UTC)
	reply := e.receive(t, bridges.In{ID: "5511", AuthorID: "42", AuthorName: "Ada", Text: strings.Repeat("я", 9000), ReplyTo: "5510", At: at})
	unknown := e.receive(t, bridges.In{ID: "5512", AuthorName: "Ada", Text: "re nothing", ReplyTo: "1"})
	h := e.history(t)
	if h[1].ID != reply || h[1].ReplyTo != first || !h[1].At.Equal(at) {
		t.Fatalf("reply: %+v", h[1])
	}
	if n := utf8.RuneCountInString(h[1].Body); n != rooms.MaxBody || !strings.HasSuffix(h[1].Body, "[truncated]") {
		t.Fatalf("long text: %d characters, ends %q", n, h[1].Body[len(h[1].Body)-20:])
	}
	if h[2].ID != unknown || h[2].ReplyTo != 0 {
		t.Fatalf("reply to an unknown id: %+v", h[2])
	}
	if _, err := e.b.Receive(ctx, "example-chat", bridges.In{AuthorName: "Ada", Text: "no id"}, ""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("no id: %v", err)
	}
}

func TestAMentionFromOutside(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "@builder can you look?"})
	msgs, _, err := e.r.Unread(ctx, "builder", rooms.Addressed, 0)
	if err != nil || len(msgs) != 1 || !msgs[0].Addressed {
		t.Fatalf("builder's addressed messages: %+v %v", msgs, err)
	}
}

func TestAddressedFromOutsideReachesTheBridgesAgents(t *testing.T) {
	e := newEnv(t)
	if _, err := e.r.Subscribe(ctx, "secretary", []string{"example-chat"}, true, rooms.ModeMentions); err != nil {
		t.Fatal(err)
	}
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "chatter"})
	id := e.receive(t, bridges.In{ID: "2", AuthorName: "Ada", Text: "for you", Addressed: true})
	msgs, _, err := e.r.Unread(ctx, "secretary", rooms.Everything, 0)
	if err != nil || len(msgs) != 1 || msgs[0].ID != id || !msgs[0].Addressed || !msgs[0].Wakes {
		t.Fatalf("secretary's unread: %+v %v", msgs, err)
	}
	if msgs, _, _ := e.r.Unread(ctx, "builder", rooms.Addressed, 0); len(msgs) != 0 {
		t.Fatalf("builder is not the bridge's agent: %+v", msgs)
	}
}

func TestTheOperatorsOwnMessagesFromOutside(t *testing.T) {
	e := newEnv(t)
	id := e.receive(t, bridges.In{ID: "1", AuthorID: "7", AuthorName: "Owner Outside", Self: true, Text: "on it"})
	h := e.history(t)
	if h[0].ID != id || h[0].Author != "owner" || h[0].External() || h[0].Delivery != "" {
		t.Fatalf("self with an operator: %+v", h[0])
	}
	if _, err := e.b.Receive(ctx, "example-chat", bridges.In{ID: "2", AuthorID: "7", AuthorName: "Owner Outside", Self: true, Text: "no app"}, ""); err != nil {
		t.Fatal(err)
	}
	if h := e.history(t); h[1].Author != "" || h[1].From() != "Owner Outside@example-chat" {
		t.Fatalf("self without an operator: %+v", h[1])
	}
}

func setPolicy(t *testing.T, e env, p bridges.Policy) {
	t.Helper()
	if err := e.b.SetPolicy(ctx, "example-chat", p); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundPolicy(t *testing.T) {
	e := newEnv(t)
	scheduled := e.post(t, "secretary", "example-chat", "looked, all fine")
	fromOperator, err := e.r.PostFromOperator(ctx, "owner", "example-chat", "thanks", 0)
	if err != nil {
		t.Fatal(err)
	}
	setPolicy(t, e, bridges.Open)
	open := e.post(t, "secretary", "example-chat", "open now")
	setPolicy(t, e, bridges.Read)
	if _, err := e.r.Post(ctx, "secretary", "example-chat", "read only", 0); !errors.Is(err, rooms.ErrReadOnly) {
		t.Fatalf("post under read: %v", err)
	}
	stays, err := e.r.PostFromOperator(ctx, "owner", "example-chat", "a note for the agents", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.b.SetPolicy(ctx, "example-chat", "loud"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown policy: %v", err)
	}
	want := map[int64]rooms.Delivery{scheduled: rooms.Scheduled, fromOperator: rooms.Sending, open: rooms.Sending, stays: ""}
	for _, m := range e.history(t) {
		if d, ok := want[m.ID]; ok && m.Delivery != d {
			t.Errorf("message %d %q: %q, want %q", m.ID, m.Body, m.Delivery, d)
		}
		if m.Body == "read only" {
			t.Error("a refused post was stored")
		}
		// what the operator writes under read stays in Agora, so it is a note
		if m.ID == stays && !m.Note {
			t.Errorf("the operator's message under read is not a note: %+v", m)
		}
	}
	// outside a bridged room nothing goes out, and the board's messages never do
	if id := e.post(t, "secretary", "general", "hello"); deliveryOf(t, e, "general", id) != "" {
		t.Fatal("a message in #general goes out")
	}
}

func deliveryOf(t *testing.T, e env, room string, id int64) rooms.Delivery {
	t.Helper()
	msgs, err := e.r.History(ctx, room, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return m.Delivery
		}
	}
	t.Fatalf("no message %d in #%s", id, room)
	return ""
}

func TestGoingOutAndAnswers(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	in := e.receive(t, bridges.In{ID: "5513", AuthorName: "Ada", Text: "can you look?"})
	first, err := e.r.Post(ctx, "secretary", "example-chat", "looked, all fine", in)
	if err != nil {
		t.Fatal(err)
	}
	second := e.post(t, "builder", "example-chat", "me too")
	outs, err := outgoing(t, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 || outs[0] != (bridges.Out{ID: first, Author: "secretary", Text: "looked, all fine", ReplyTo: "5513"}) || outs[1].ID != second || outs[1].ReplyTo != "" {
		t.Fatalf("outgoing: %+v", outs)
	}
	if ok, _, err := e.b.Sent(ctx, "example-chat", first, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	if ok, _, _ := e.b.Sent(ctx, "example-chat", first, "5515"); ok {
		t.Fatal("sent twice")
	}
	if echo := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Self: true, Text: "looked, all fine"}); echo != 0 {
		t.Fatal("the echo of a sent message was stored")
	}
	if ok, err := e.b.Failed(ctx, "example-chat", second, "chat not found"); !ok || err != nil {
		t.Fatalf("failed: %v %v", ok, err)
	}
	if outs, _ := outgoing(t, e); len(outs) != 0 {
		t.Fatalf("answered messages still go out: %+v", outs)
	}
	h := e.history(t)
	last := h[len(h)-1]
	if last.Author != rooms.Board || last.Body != "@builder your message "+itoa(second)+" was not sent: chat not found." {
		t.Fatalf("board after failed: %+v", last)
	}
	if deliveryOf(t, e, "example-chat", first) != rooms.Sent || deliveryOf(t, e, "example-chat", second) != rooms.Failed {
		t.Fatalf("delivery: %+v", h)
	}
	for _, m := range h {
		if m.ID == second && (m.DeliveryError != "chat not found" || !strings.HasSuffix(strings.Split(rooms.Format(m, 0), "\n")[0], " · not sent: chat not found")) {
			t.Fatalf("failed message: %+v / %q", m, rooms.Format(m, 0))
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTheOperatorSendsOrCancels(t *testing.T) {
	e := newEnv(t)
	send := e.post(t, "secretary", "example-chat", "looked, all fine")
	cancel := e.post(t, "secretary", "example-chat", "something rude")
	if outs, _ := outgoing(t, e); len(outs) != 0 {
		t.Fatalf("scheduled messages go out: %+v", outs)
	}
	if err := e.b.SendScheduled(ctx, send); err != nil {
		t.Fatal(err)
	}
	if err := e.b.CancelScheduled(ctx, cancel, "owner"); err != nil {
		t.Fatal(err)
	}
	if outs, _ := outgoing(t, e); len(outs) != 1 || outs[0].ID != send {
		t.Fatalf("outgoing: %+v", outs)
	}
	if err := e.b.SendScheduled(ctx, cancel); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("sending a cancelled message: %v", err)
	}
	if err := e.b.CancelScheduled(ctx, send, "owner"); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("cancelling a sending message: %v", err)
	}
	msgs, _, _ := e.r.Unread(ctx, "secretary", rooms.Addressed, 0)
	if len(msgs) != 1 || msgs[0].Body != "@secretary your message "+itoa(cancel)+" was not sent: owner cancelled it." {
		t.Fatalf("secretary is told: %+v", msgs)
	}
	if deliveryOf(t, e, "example-chat", cancel) != rooms.Cancelled {
		t.Fatal("not cancelled")
	}
	later := e.post(t, "secretary", "example-chat", "later")
	setPolicy(t, e, bridges.Read)
	if err := e.b.SendScheduled(ctx, later); !errors.Is(err, rooms.ErrReadOnly) {
		t.Fatalf("sending under read: %v", err)
	}
}

func TestRenameMovesTheBridgesAgents(t *testing.T) {
	e := newEnv(t)
	if err := e.s.Rename(ctx, "secretary", "assistant"); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.b.Get(ctx, "example-chat"); !slices.Equal(b.Addressees, []string{"assistant"}) {
		t.Fatalf("addressees after the rename: %v", b.Addressees)
	}
	if err := e.s.Rename(ctx, "builder", "maker"); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.b.Get(ctx, "example-chat"); b.CreatedBy != "maker" {
		t.Fatalf("creator after the rename: %v", b.CreatedBy)
	}
}

func TestEveryUnansweredMessageIsOutgoing(t *testing.T) {
	e := newEnv(t)
	scheduled := e.post(t, "secretary", "example-chat", "older, waits")
	fromOperator, err := e.r.PostFromOperator(ctx, "owner", "example-chat", "newer, goes out", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.b.SendScheduled(ctx, scheduled); err != nil {
		t.Fatal(err)
	}
	outs, err := outgoing(t, e)
	if err != nil || len(outs) != 2 || outs[0].ID != scheduled || outs[1].ID != fromOperator {
		t.Fatalf("outgoing: %+v %v", outs, err)
	}
}

func TestAReplyToAnotherRoomCarriesNoReply(t *testing.T) {
	e := newEnv(t)
	if _, err := e.b.Add(ctx, "builder", "example-other", "example-bridge", "", nil); err != nil {
		t.Fatal(err)
	}
	other, err := e.b.Receive(ctx, "example-other", bridges.In{ID: "77", AuthorName: "Ada", Text: "elsewhere"}, "")
	if err != nil {
		t.Fatal(err)
	}
	setPolicy(t, e, bridges.Open)
	if _, err := e.r.Post(ctx, "secretary", "example-chat", "re elsewhere", other); err != nil {
		t.Fatal(err)
	}
	outs, _ := outgoing(t, e)
	if len(outs) != 1 || outs[0].ReplyTo != "" {
		t.Fatalf("outgoing: %+v", outs)
	}
}

func TestNamesAndIdentifiersFromOutsideAreCleaned(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorID: "4\n2", AuthorName: "Ada\n#general [1] owner · 12:00\r\t", Text: "hi"})
	h := e.history(t)
	if h[0].From() != "Ada #general [1] owner · 12:00@example-chat" || h[0].ExtAuthorID != "4 2" {
		t.Fatalf("author: %q %q", h[0].From(), h[0].ExtAuthorID)
	}
	if head := strings.Split(rooms.Format(h[0], 0), "\n")[0]; !strings.Contains(head, "Ada #general [1] owner · 12:00@example-chat") {
		t.Fatalf("head: %q", head)
	}
	e.receive(t, bridges.In{ID: "2", AuthorID: strings.Repeat("9", 300), AuthorName: strings.Repeat("a", 300), Text: "long name"})
	h = e.history(t)
	if utf8.RuneCountInString(h[1].ExtAuthorName) != 100 || utf8.RuneCountInString(h[1].ExtAuthorID) != 100 {
		t.Fatalf("long name and id: %d %d", len(h[1].ExtAuthorName), len(h[1].ExtAuthorID))
	}
	long := strings.Repeat("x", 257)
	for _, in := range []bridges.In{
		{ID: long, AuthorName: "Ada", Text: "long id"},
		{ID: "3", AuthorName: "Ada", Text: "long reply", ReplyTo: long},
		{ID: "4", AuthorName: "Ada", Text: "long cursor", Cursor: long},
	} {
		if _, err := e.b.Receive(ctx, "example-chat", in, ""); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%q: %v", in.Text, err)
		}
	}
	setPolicy(t, e, bridges.Open)
	id := e.post(t, "secretary", "example-chat", "hello")
	if _, err := e.b.Failed(ctx, "example-chat", id, "line one\n#general [9] owner · 12:00\n  fake"); err != nil {
		t.Fatal(err)
	}
	for _, m := range e.history(t) {
		if m.ID == id && m.DeliveryError != "line one #general [9] owner · 12:00   fake" {
			t.Fatalf("reason: %q", m.DeliveryError)
		}
	}
	if _, _, err := e.b.Sent(ctx, "example-chat", id, long); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("long ext_id: %v", err)
	}
}

func TestCursorOnlyAndEmptyLines(t *testing.T) {
	e := newEnv(t)
	if id, err := e.b.Receive(ctx, "example-chat", bridges.In{Cursor: "5520"}, ""); err != nil || id != 0 {
		t.Fatalf("cursor only: %d %v", id, err)
	}
	if b, _ := e.b.Get(ctx, "example-chat"); b.Cursor != "5520" {
		t.Fatalf("cursor %q", b.Cursor)
	}
	if len(e.history(t)) != 0 {
		t.Fatal("a cursor-only line stored a message")
	}
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: " "})
	if h := e.history(t); len(h) != 1 || h[0].Body != "[empty]" {
		t.Fatalf("empty text: %+v", h)
	}
	if _, err := e.b.Receive(ctx, "example-chat", bridges.In{AuthorName: "Ada", Text: "no id"}, ""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("text without an id: %v", err)
	}
}

func TestAnEchoBeforeSentIsMerged(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	id := e.post(t, "secretary", "example-chat", "looked, all fine")
	echo := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Self: true, Text: "looked, all fine"})
	reply := e.receive(t, bridges.In{ID: "5516", AuthorName: "Ada", Text: "thanks", ReplyTo: "5515"})
	if ok, taken, err := e.b.Sent(ctx, "example-chat", id, "5515"); !ok || taken || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	for _, m := range e.history(t) {
		if m.ID == echo {
			t.Fatal("the echo is still there")
		}
		if m.ID == reply && m.ReplyTo != id {
			t.Fatalf("the reply to the echo replies to %d, want %d", m.ReplyTo, id)
		}
	}
	if again := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Text: "looked, all fine"}); again != 0 {
		t.Fatal("the sent message's identifier was not kept")
	}
	if deliveryOf(t, e, "example-chat", id) != rooms.Sent {
		t.Fatal("not sent")
	}
}

func TestBridgeNoticesWakeNoOne(t *testing.T) {
	e := newEnv(t) // secretary follows #example-chat with the mode wake
	if err := e.b.Notice(ctx, "example-chat", "The bridge stopped (exit status 1)."); err != nil {
		t.Fatal(err)
	}
	if msgs, _, _ := e.r.Unread(ctx, "secretary", rooms.Waking, 0); len(msgs) != 0 {
		t.Fatalf("waking: %+v", msgs)
	}
	if msgs, _, _ := e.r.Unread(ctx, "secretary", rooms.Everything, 0); len(msgs) != 1 || msgs[0].Wakes {
		t.Fatalf("unread: %+v", msgs)
	}
	if n, _ := e.r.UnreadCount(ctx, "secretary", rooms.Waking); n != 0 {
		t.Fatalf("waking count %d", n)
	}
	e.post(t, "builder", "example-chat", "hello")
	if msgs, _, _ := e.r.Unread(ctx, "secretary", rooms.Waking, 0); len(msgs) != 1 {
		t.Fatalf("an agent's message does wake: %+v", msgs)
	}
}

func TestATakenIdentifierIsNotMerged(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	ada := e.receive(t, bridges.In{ID: "5513", AuthorName: "Ada", Text: "can you look?"})
	first := e.post(t, "secretary", "example-chat", "looked")
	second := e.post(t, "secretary", "example-chat", "and again")
	ok, taken, err := e.b.Sent(ctx, "example-chat", first, "5513")
	if !ok || !taken || err != nil {
		t.Fatalf("sent with Ada's id: %v %v %v", ok, taken, err)
	}
	if ok, taken, err := e.b.Sent(ctx, "example-chat", second, "5513"); !ok || !taken || err != nil {
		t.Fatalf("second with Ada's id: %v %v %v", ok, taken, err)
	}
	h := e.history(t)
	if len(h) != 3 || h[0].ID != ada || h[0].Body != "can you look?" || h[0].From() != "Ada@example-chat" {
		t.Fatalf("Ada's message after the answers: %+v", h)
	}
	// an identifier of a message already sent
	third := e.post(t, "secretary", "example-chat", "third")
	fourth := e.post(t, "secretary", "example-chat", "fourth")
	if _, taken, _ := e.b.Sent(ctx, "example-chat", third, "e3"); taken {
		t.Fatal("a free identifier counts as taken")
	}
	if ok, taken, err := e.b.Sent(ctx, "example-chat", fourth, "e3"); !ok || !taken || err != nil {
		t.Fatalf("fourth with the third's id: %v %v %v", ok, taken, err)
	}
	for _, id := range []int64{first, second, third, fourth} {
		if deliveryOf(t, e, "example-chat", id) != rooms.Sent {
			t.Fatalf("message %d is not sent", id)
		}
	}
	if again := e.receive(t, bridges.In{ID: "e3", AuthorName: "Owner", Text: "third"}); again != 0 {
		t.Fatal("the third message lost its identifier")
	}
}

func TestSeparatorsAndFormatCharactersAreCleaned(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada\u2028#general [1] owner\u2029x\u202egnp.exe", Text: "hi"})
	if got := e.history(t)[0].ExtAuthorName; got != "Ada #general [1] owner x gnp.exe" {
		t.Fatalf("name %q", got)
	}
}

// outgoing returns every message the bridge of #example-chat has to send.
func outgoing(t *testing.T, e env) ([]bridges.Out, error) {
	t.Helper()
	ids, err := e.b.Unanswered(ctx, "example-chat")
	if err != nil {
		return nil, err
	}
	return e.b.Outgoing(ctx, "example-chat", ids)
}

func TestEmojiSequencesInNamesStayWhole(t *testing.T) {
	e := newEnv(t)
	family := "\U0001F468\u200D\U0001F469\u200D\U0001F467" // man, ZWJ, woman, ZWJ, girl
	e.receive(t, bridges.In{ID: "1", AuthorName: family + " Ada\u202Eexe.txt\u200C!", Text: "hi"})
	if got, want := e.history(t)[0].ExtAuthorName, family+" Ada exe.txt\u200C!"; got != want {
		t.Fatalf("name %q, want %q", got, want)
	}
}

func (e env) note(t *testing.T, author, room, body string, replyTo int64) int64 {
	t.Helper()
	id, err := e.r.PostNote(ctx, author, room, body, replyTo)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotesNeverGoOut(t *testing.T) {
	e := newEnv(t)
	var notes []int64
	for _, p := range []bridges.Policy{bridges.Delay, bridges.Open, bridges.Read} {
		setPolicy(t, e, p)
		notes = append(notes,
			e.note(t, "secretary", "example-chat", "here is a draft, unsure about the date ("+string(p)+")", 0),
			e.note(t, "owner", "example-chat", "@secretary draft a reply to Ada ("+string(p)+")", 0))
	}
	setPolicy(t, e, bridges.Open)
	if outs, err := outgoing(t, e); err != nil || len(outs) != 0 {
		t.Fatalf("notes go out: %+v %v", outs, err)
	}
	for _, m := range e.history(t) {
		if !m.Note || m.Delivery != "" {
			t.Errorf("note %d: note %v, delivery %q", m.ID, m.Note, m.Delivery)
		}
	}
	// a note has nothing to send at once or cancel
	if err := e.b.SendScheduled(ctx, notes[0]); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("sending a note: %v", err)
	}
	if err := e.b.CancelScheduled(ctx, notes[1], "owner"); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("cancelling a note: %v", err)
	}
	// still a note once the bridge is gone
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	if h := e.history(t); !h[0].Note {
		t.Fatalf("after the bridge was removed: %+v", h[0])
	}
}

func TestANoteInARoomWithoutABridgeIsAMessage(t *testing.T) {
	e := newEnv(t)
	id := e.note(t, "secretary", "general", "nothing goes out of here anyway", 0)
	h, err := e.r.History(ctx, "general", 1)
	if err != nil {
		t.Fatal(err)
	}
	if h[0].ID != id || h[0].Note || h[0].Delivery != "" || strings.Contains(rooms.Format(h[0], 0), "note") {
		t.Fatalf("a note in #general: %+v", h[0])
	}
}

func TestAReplyToANoteCarriesNoReply(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	note := e.note(t, "owner", "example-chat", "@secretary say the release is on Friday", 0)
	reply, err := e.r.Post(ctx, "secretary", "example-chat", "The release is on Friday.", note)
	if err != nil {
		t.Fatal(err)
	}
	outs, err := outgoing(t, e)
	if err != nil || len(outs) != 1 || outs[0] != (bridges.Out{ID: reply, Author: "secretary", Text: "The release is on Friday."}) {
		t.Fatalf("outgoing: %+v %v", outs, err)
	}
	if ok, _, err := e.b.Sent(ctx, "example-chat", reply, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	if h := e.history(t); h[0].ID != note || !h[0].Note || h[1].ReplyTo != note {
		t.Fatalf("history: %+v", h)
	}
}

func TestNotesAreMessagesOfTheRoom(t *testing.T) {
	e := newEnv(t)
	note := e.note(t, "owner", "example-chat", "@secretary draft a reply to Ada about the release date", 0)
	msgs, _, err := e.r.Unread(ctx, "secretary", rooms.Waking, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != note || !msgs[0].Addressed || !msgs[0].Note {
		t.Fatalf("secretary's waking messages: %+v", msgs)
	}
	if head := strings.Split(rooms.Format(msgs[0], 0), "\n")[0]; !strings.HasSuffix(head, "owner · "+msgs[0].At.Local().Format("15:04")+" · to you · note") {
		t.Fatalf("head: %q", head)
	}
	reply := e.note(t, "secretary", "example-chat", "here is a draft, unsure about the date", note)
	if h := e.history(t); h[1].ID != reply || h[1].ReplyTo != note || !h[1].Note {
		t.Fatalf("a note replying to a note: %+v", h)
	}
}

func TestHistoryTellsWhatTheChatSaw(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	e.receive(t, bridges.In{ID: "5513", AuthorID: "42", AuthorName: "Ada", Text: "when is the release?"})
	e.receive(t, bridges.In{ID: "5514", AuthorID: "7", AuthorName: "Owner Outside", Self: true, Text: "checking"})
	e.note(t, "owner", "example-chat", "@secretary draft a reply", 0)
	sent := e.post(t, "secretary", "example-chat", "Friday.")
	if ok, _, err := e.b.Sent(ctx, "example-chat", sent, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	var heads []string
	for _, m := range e.history(t) {
		head := strings.Split(rooms.Format(m, 0), "\n")[0]
		heads = append(heads, head[strings.Index(head, "] ")+2:])
	}
	at := time.Now().Local().Format("15:04")
	want := []string{
		"Ada@example-chat · " + at + " · from outside",
		"owner · " + at + " · from outside",
		"owner · " + at + " · note",
		"secretary · " + at + " · sent",
	}
	if !slices.Equal(heads, want) {
		t.Fatalf("heads:\n%q\nwant\n%q", heads, want)
	}
}

func TestAgentsPostOnlyNotesUnderRead(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Read)
	if _, err := e.r.Post(ctx, "secretary", "example-chat", "to the chat", 0); !errors.Is(err, rooms.ErrReadOnly) || !strings.Contains(err.Error(), "--note") {
		t.Fatalf("post under read: %v", err)
	}
	if id := e.note(t, "secretary", "example-chat", "for Agora only", 0); deliveryOf(t, e, "example-chat", id) != "" {
		t.Fatal("a note under read has a delivery state")
	}
}

func TestARepliesToAnAgentsMessageAddressesIt(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	out := e.post(t, "builder", "example-chat", "the fix ships tonight")
	if ok, _, err := e.b.Sent(ctx, "example-chat", out, "900"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	ada := e.receive(t, bridges.In{ID: "901", AuthorName: "Ada", Text: "great, thanks", ReplyTo: "900"})
	msgs, _, err := e.r.Unread(ctx, "builder", rooms.Waking, 0)
	if err != nil || len(msgs) != 1 || msgs[0].ID != ada || !msgs[0].Addressed {
		t.Fatalf("builder's waking messages: %+v %v", msgs, err)
	}
	// a reply to a message from outside addresses no one this way
	e.receive(t, bridges.In{ID: "902", AuthorName: "Bob", Text: "me too", ReplyTo: "901"})
	if msgs, _, _ := e.r.Unread(ctx, "builder", rooms.Addressed, 0); len(msgs) != 1 {
		t.Fatalf("builder addressed by a reply to Ada: %+v", msgs)
	}
}

func TestChangingABridgesAgents(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "hi", Cursor: "1"})
	// secretary chose to follow the room with the mode mentions; staying an agent keeps it
	if _, err := e.r.Subscribe(ctx, "secretary", []string{"example-chat"}, true, rooms.ModeMentions); err != nil {
		t.Fatal(err)
	}
	got, err := e.b.Set(ctx, "builder", "example-chat", bridges.Change{SetAgents: true, Agents: []string{"secretary", "builder", "builder"}})
	if err != nil || got.Restart || len(got.Dropped) != 0 || !slices.Equal(got.Agents, []string{"builder", "secretary"}) {
		t.Fatalf("set: %+v %v", got, err)
	}
	if !slices.Contains(e.subscriptions(t, "builder"), rooms.Subscription{Room: "example-chat", Mode: rooms.ModeWake}) {
		t.Fatal("builder does not follow the room with the mode wake")
	}
	if !slices.Contains(e.subscriptions(t, "secretary"), rooms.Subscription{Room: "example-chat", Mode: rooms.ModeMentions}) {
		t.Fatalf("secretary's mode changed: %+v", e.subscriptions(t, "secretary"))
	}
	id := e.receive(t, bridges.In{ID: "2", AuthorName: "Ada", Text: "for you", Addressed: true})
	for _, a := range []string{"secretary", "builder"} {
		if msgs, _, _ := e.r.Unread(ctx, a, rooms.Addressed, 0); !slices.ContainsFunc(msgs, func(m rooms.Message) bool { return m.ID == id }) {
			t.Fatalf("%s is not addressed: %+v", a, msgs)
		}
	}
	// secretary is dropped: it keeps following the room but is no longer addressed
	got, err = e.b.Set(ctx, "builder", "example-chat", bridges.Change{SetAgents: true, Agents: []string{"builder"}})
	if err != nil || !slices.Equal(got.Dropped, []string{"secretary"}) {
		t.Fatalf("set: %+v %v", got, err)
	}
	if !slices.Contains(e.subscriptions(t, "secretary"), rooms.Subscription{Room: "example-chat", Mode: rooms.ModeMentions}) {
		t.Fatal("secretary's subscription dropped")
	}
	id = e.receive(t, bridges.In{ID: "3", AuthorName: "Ada", Text: "again", Addressed: true})
	if msgs, _, _ := e.r.Unread(ctx, "secretary", rooms.Addressed, 0); slices.ContainsFunc(msgs, func(m rooms.Message) bool { return m.ID == id }) {
		t.Fatalf("secretary is still addressed: %+v", msgs)
	}
	if msgs, _, _ := e.r.Unread(ctx, "builder", rooms.Addressed, 0); !slices.ContainsFunc(msgs, func(m rooms.Message) bool { return m.ID == id }) {
		t.Fatalf("builder is not addressed: %+v", msgs)
	}
	list, _ := e.b.List(ctx)
	if len(list) != 1 || !slices.Equal(list[0].Addressees, []string{"builder"}) || list[0].Cursor != "1" ||
		list[0].Command != "example-bridge --chat 42" || list[0].CreatedBy != "builder" || list[0].Policy != bridges.Delay {
		t.Fatalf("bridge: %+v", list)
	}
	h := e.history(t)
	said := 0
	for _, m := range h {
		if m.Author == rooms.Board && strings.HasPrefix(m.Body, "builder set the bridge's agents: ") {
			said++
		}
	}
	if said != 2 || h[len(h)-2].Body != "builder set the bridge's agents: builder." {
		t.Fatalf("history: %+v", h)
	}
	// the notices wake no one: only the addressed messages from outside do
	if msgs, _, _ := e.r.Unread(ctx, "builder", rooms.Waking, 0); slices.ContainsFunc(msgs, func(m rooms.Message) bool { return m.Author == rooms.Board }) {
		t.Fatalf("a notice wakes: %+v", msgs)
	}
	// none
	got, err = e.b.Set(ctx, "builder", "example-chat", bridges.Change{SetAgents: true})
	if err != nil || len(got.Agents) != 0 || !slices.Equal(got.Dropped, []string{"builder"}) {
		t.Fatalf("no agents: %+v %v", got, err)
	}
}

func TestChangingABridgesCommand(t *testing.T) {
	e := newEnv(t)
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "hi", Cursor: "1"})
	got, err := e.b.Set(ctx, "builder", "example-chat", bridges.Change{SetCommand: true, Command: "  example-bridge --chat 43 "})
	if err != nil || !got.Restart || !slices.Equal(got.Agents, []string{"secretary"}) {
		t.Fatalf("set: %+v %v", got, err)
	}
	b, err := e.b.Get(ctx, "example-chat")
	if err != nil || b.Command != "example-bridge --chat 43" || b.Cursor != "1" || !slices.Equal(b.Addressees, []string{"secretary"}) {
		t.Fatalf("bridge: %+v %v", b, err)
	}
	h := e.history(t)
	if last := h[len(h)-1]; last.Author != rooms.Board || !strings.Contains(last.Body, "builder changed the bridge's command") || strings.Contains(last.Body, "--chat") {
		t.Fatalf("notice: %+v", last)
	}
	// the same command again restarts nothing
	if got, err := e.b.Set(ctx, "builder", "example-chat", bridges.Change{SetCommand: true, Command: "example-bridge --chat 43"}); err != nil || got.Restart {
		t.Fatalf("same command: %+v %v", got, err)
	}
}

func TestChangingABridgeIsRefused(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, agent, bridge string
		change              bridges.Change
		want                error
	}{
		{"nothing to change", "builder", "example-chat", bridges.Change{}, store.ErrInvalid},
		{"no such bridge", "builder", "other-chat", bridges.Change{SetCommand: true, Command: "x"}, bridges.ErrNotFound},
		{"an agent that has not joined", "builder", "example-chat", bridges.Change{SetAgents: true, Agents: []string{"ghost"}, SetCommand: true, Command: "y"}, agents.ErrUnknown},
		{"an acting agent that has not joined", "ghost", "example-chat", bridges.Change{SetCommand: true, Command: "y"}, agents.ErrUnknown},
		{"an empty command", "builder", "example-chat", bridges.Change{SetAgents: true, Agents: []string{"builder"}, SetCommand: true, Command: "  "}, store.ErrInvalid},
	}
	for _, c := range cases {
		if _, err := e.b.Set(ctx, c.agent, c.bridge, c.change); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	b, _ := e.b.Get(ctx, "example-chat")
	if b.Command != "example-bridge --chat 42" || !slices.Equal(b.Addressees, []string{"secretary"}) {
		t.Fatalf("bridge changed: %+v", b)
	}
}

func TestSendingAtOnceAsTheDelayPasses(t *testing.T) {
	e := newEnv(t)
	id := e.post(t, "secretary", "example-chat", "looked, all fine")
	if err := e.b.SendScheduled(ctx, id); err != nil {
		t.Fatal(err)
	}
	// clicked again, or after the delay let it go out: it is going out, so that is no refusal
	if err := e.b.SendScheduled(ctx, id); err != nil {
		t.Fatalf("sending a message that is sending: %v", err)
	}
	if _, _, err := e.b.Sent(ctx, "example-chat", id, "5515"); err != nil {
		t.Fatal(err)
	}
	if err := e.b.SendScheduled(ctx, id); err != nil {
		t.Fatalf("sending a sent message: %v", err)
	}
	if deliveryOf(t, e, "example-chat", id) != rooms.Sent {
		t.Fatal("not sent")
	}
	if err := e.b.SendScheduled(ctx, id+100); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("sending a message that does not exist: %v", err)
	}
}

func TestRemovingSettlesMessagesLeftSending(t *testing.T) {
	e := newEnv(t)
	sending, err := e.r.PostFromOperator(ctx, "owner", "example-chat", "on my way", 0)
	if err != nil {
		t.Fatal(err)
	}
	scheduled := e.post(t, "secretary", "example-chat", "looked, all fine")
	if err := e.b.Remove(ctx, "builder", "example-chat"); err != nil {
		t.Fatal(err)
	}
	if deliveryOf(t, e, "example-chat", sending) != rooms.Failed || deliveryOf(t, e, "example-chat", scheduled) != rooms.Scheduled {
		t.Fatalf("history: %+v", e.history(t))
	}
	h := e.history(t)
	told := slices.ContainsFunc(h, func(m rooms.Message) bool {
		return m.Author == rooms.Board && m.Body == "@owner your message "+itoa(sending)+" may not have gone out: the bridge was removed before it answered. Check the chat before you send it again." && m.ReplyTo == sending
	})
	if !told {
		t.Fatalf("the operator is not told: %+v", h)
	}
	for _, m := range h {
		if m.ID == sending && m.DeliveryError != "the bridge was removed before it answered; it may have gone out" {
			t.Fatalf("reason: %+v", m)
		}
	}
}

func TestAnEchoOfAThreadReplyKeepsThreadsFlat(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	root := e.post(t, "builder", "example-chat", "the export")
	sent, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "looked, all fine", Thread: root})
	if err != nil {
		t.Fatal(err)
	}
	echo := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Self: true, Text: "looked, all fine"})
	// someone replies in the echo's thread before the bridge answers sent
	under, err := e.r.Send(ctx, rooms.Posting{Author: "builder", Room: "example-chat", Body: "thanks", Thread: echo})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.SetThreadTitle(ctx, "builder", echo, "Export"); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.b.Sent(ctx, "example-chat", sent, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	thread, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, m := range thread {
		got = append(got, m.ID)
		if m.ID != root && m.Thread != root {
			t.Fatalf("message %d is in thread %d, want %d", m.ID, m.Thread, root)
		}
	}
	if !slices.Equal(got, []int64{root, sent, under}) || thread[0].ThreadTitle != "Export" {
		t.Fatalf("thread %v titled %q", got, thread[0].ThreadTitle)
	}
}

func TestMergingAnEchoKeepsUnreadRepliesUnread(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	root := e.post(t, "builder", "example-chat", "the export")
	sent, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "looked, all fine", Thread: root})
	if err != nil {
		t.Fatal(err)
	}
	echo := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Self: true, Text: "looked, all fine"})
	under, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "more", Thread: echo, ReplyTo: echo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-chat", under); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.b.Sent(ctx, "example-chat", sent, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	got, _, err := e.r.Unread(ctx, "builder", rooms.Everything, 0)
	if err != nil || !slices.Contains(msgIDs(got), sent) || slices.Contains(msgIDs(got), under) {
		t.Fatalf("builder unread %v, want %d unread and %d read", msgIDs(got), sent, under)
	}
}

func msgIDs(msgs []rooms.Message) []int64 {
	var out []int64
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

func TestATopicRenamedOutsideRetitlesItsThread(t *testing.T) {
	e := newEnv(t)
	title := func(root int64) string {
		t.Helper()
		msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		return msgs[0].ThreadTitle
	}
	root := e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "release today", ThreadKey: "77", ThreadTitle: "Release"})
	e.receive(t, bridges.In{ID: "2", AuthorName: "Bob", Text: "when?", ThreadKey: "77", ThreadTitle: "Release 2.4"})
	if got := title(root); got != "Release 2.4" {
		t.Fatalf("renamed outside: %q", got)
	}
	e.receive(t, bridges.In{ID: "3", AuthorName: "Bob", Text: "five", ThreadKey: "77"})
	if got := title(root); got != "Release 2.4" {
		t.Fatalf("a line without a title keeps it: %q", got)
	}
	// a title an agent or the operator gave stays
	for _, who := range []string{"builder", "owner"} {
		if err := e.r.SetThreadTitle(ctx, who, root, "By "+who); err != nil {
			t.Fatal(err)
		}
		e.receive(t, bridges.In{ID: "4" + who, AuthorName: "Bob", Text: "again", ThreadKey: "77", ThreadTitle: "Release 2.5"})
		if got := title(root); got != "By "+who {
			t.Fatalf("a title of %s was replaced from outside: %q", who, got)
		}
	}
}

func TestARepeatedTitleFromOutsideChangesNothing(t *testing.T) {
	e, c := newDelayEnv(t)
	t0 := c.now()
	lastAt := func() time.Time {
		t.Helper()
		th, err := e.r.Threads(ctx, "example-chat", "", false)
		if err != nil || len(th) != 1 {
			t.Fatalf("threads %+v %v", th, err)
		}
		return th[0].LastAt
	}
	e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "release today", ThreadKey: "77", ThreadTitle: "Release"})
	c.add(time.Hour)
	e.receive(t, bridges.In{ID: "2", AuthorName: "Bob", Text: "when?", ThreadKey: "77", ThreadTitle: "Release 2.4", At: t0})
	renamed := lastAt()
	if !renamed.Equal(t0.Add(time.Hour)) {
		t.Fatalf("a new title is activity: %v", renamed)
	}
	c.add(time.Hour)
	e.receive(t, bridges.In{ID: "3", AuthorName: "Bob", Text: "five", ThreadKey: "77", ThreadTitle: "Release 2.4", At: t0})
	if got := lastAt(); !got.Equal(renamed) {
		t.Fatalf("the same title again moved the thread's activity: %v", got)
	}
}

func TestAThreadWithoutATitleTakesOneFromALaterLine(t *testing.T) {
	e := newEnv(t)
	root := e.receive(t, bridges.In{ID: "1", AuthorName: "Ada", Text: "release today", ThreadKey: "88"})
	e.receive(t, bridges.In{ID: "2", AuthorName: "Bob", Text: "when?", ThreadKey: "88", ThreadTitle: "Late"})
	msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil || msgs[0].ThreadTitle != "Late" {
		t.Fatalf("%+v %v", msgs, err)
	}
}

func TestMessagesOfAThreadOutside(t *testing.T) {
	e := newEnv(t)
	root := e.receive(t, bridges.In{ID: "1", AuthorID: "42", AuthorName: "Ada", Text: "release today", ThreadKey: "77", ThreadTitle: "Release\n2.4"})
	if _, err := e.r.Send(ctx, rooms.Posting{Author: "builder", Room: "example-chat", Body: "at five", Thread: root, Note: true}); err != nil {
		t.Fatal(err)
	}
	reply := e.receive(t, bridges.In{ID: "2", AuthorID: "43", AuthorName: "Bob", Text: "which time?", ThreadKey: "77"})
	msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[0].ThreadTitle != "Release 2.4" || msgs[2].ID != reply || msgs[2].Thread != root {
		t.Fatalf("thread: %+v", msgs)
	}
	got, _, err := e.r.Unread(ctx, "builder", rooms.Everything, 0)
	if err != nil || len(got) != 2 || got[1].ID != reply || !got[1].Addressed { // the root, then the reply
		t.Fatalf("builder took part, so the reply from outside addresses it: %+v %v", got, err)
	}
	if _, err := e.b.Receive(ctx, "example-chat", bridges.In{ID: "3", Text: "x", ThreadKey: strings.Repeat("k", bridges.MaxIdentifier+1)}, "owner"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a long thread key: %v", err)
	}
}

// threadIn is a line from topic 77 outside.
func threadIn(id, author, text string) bridges.In {
	return bridges.In{ID: id, AuthorID: author, AuthorName: author, Text: text, ThreadKey: "77", ThreadTitle: "Release"}
}

func TestTheBridgesAgentsTakePartInItsThreads(t *testing.T) {
	e := newEnv(t) // secretary is the bridge's agent and follows the room with the mode wake
	root := e.receive(t, threadIn("1", "Ada", "release today"))
	reply := e.receive(t, threadIn("2", "Bob", "which time?"))
	got, _, err := e.r.Unread(ctx, "secretary", rooms.Waking, 0)
	if err != nil || !slices.Equal(msgIDs(got), []int64{root, reply}) {
		t.Fatalf("waking for secretary: %v %v", msgIDs(got), err)
	}
}

func TestAKeyThatNamesTheRootJoinsItsThread(t *testing.T) {
	e := newEnv(t)
	root := e.receive(t, bridges.In{ID: "100", AuthorID: "42", AuthorName: "Ada", Text: "release today"})
	reply, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "at five", Thread: root})
	if err != nil {
		t.Fatal(err)
	}
	// the platform keys the thread by its root: the next line names the key 100 for the first time
	bob := e.receive(t, bridges.In{ID: "101", AuthorID: "43", AuthorName: "Bob", Text: "five is fine", ReplyTo: "100", ThreadKey: "100", ThreadTitle: "Release"})
	later := e.receive(t, bridges.In{ID: "102", AuthorID: "42", AuthorName: "Ada", Text: "good", ThreadKey: "100"})
	msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(msgIDs(msgs), []int64{root, reply, bob, later}) || msgs[0].ThreadTitle != "Release" {
		t.Fatalf("thread %v titled %q", msgIDs(msgs), msgs[0].ThreadTitle)
	}
	// a new key replying to a message it does not name starts a thread of its own
	quote := e.receive(t, bridges.In{ID: "103", AuthorID: "43", AuthorName: "Bob", Text: "as Ada said", ReplyTo: "100", ThreadKey: "77", ThreadTitle: "Other"})
	if msgs, _ := e.r.ThreadHistory(ctx, quote, 0, ""); len(msgs) != 1 || msgs[0].Thread != 0 || msgs[0].ThreadTitle != "Other" {
		t.Fatalf("a quote from another topic: %+v", msgs)
	}
}

func TestAnEchoHoldingAKeyGivesItToTheSentMessagesThread(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	root := e.receive(t, bridges.In{ID: "100", AuthorID: "42", AuthorName: "Ada", Text: "release today"})
	sent, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "at five", Thread: root})
	if err != nil {
		t.Fatal(err)
	}
	// the copy comes back from topic 77 before sent; it names no message, so it roots the key
	echo := e.receive(t, bridges.In{ID: "5515", AuthorName: "Owner", Self: true, Text: "at five", ThreadKey: "77"})
	if ok, _, err := e.b.Sent(ctx, "example-chat", sent, "5515"); !ok || err != nil {
		t.Fatalf("sent: %v %v", ok, err)
	}
	bob := e.receive(t, threadIn("5516", "Bob", "which time?"))
	msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(msgIDs(msgs), []int64{root, sent, bob}) {
		t.Fatalf("thread %v (echo was %d)", msgIDs(msgs), echo)
	}
}

func TestTheOperatorWritingInAThreadOutsideTakesPartAgain(t *testing.T) {
	e := newEnv(t)
	root := e.receive(t, threadIn("1", "Ada", "release today"))
	if _, err := e.r.Send(ctx, rooms.Posting{Author: "owner", Room: "example-chat", Body: "noted", Thread: root, Note: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.r.LeaveThread(ctx, "owner", root); err != nil {
		t.Fatal(err)
	}
	e.receive(t, bridges.In{ID: "2", AuthorName: "Owner", Self: true, Text: "back", ThreadKey: "77"})
	reply := e.receive(t, threadIn("3", "Bob", "welcome back"))
	got, _, err := e.r.Unread(ctx, "owner", rooms.Everything, 0)
	if err != nil || !slices.Contains(msgIDs(got), reply) {
		t.Fatalf("owner after writing outside: %v %v", msgIDs(got), err)
	}
}

func TestAReplyToANoteInAThreadGoesOutUnderTheRoot(t *testing.T) {
	e := newEnv(t)
	setPolicy(t, e, bridges.Open)
	root := e.receive(t, bridges.In{ID: "100", AuthorID: "42", AuthorName: "Ada", Text: "release today"})
	note, err := e.r.Send(ctx, rooms.Posting{Author: "owner", Room: "example-chat", Body: "ask about times", Thread: root, Note: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.r.Send(ctx, rooms.Posting{Author: "secretary", Room: "example-chat", Body: "at five?", Thread: root, ReplyTo: note})
	if err != nil {
		t.Fatal(err)
	}
	outs, err := e.b.Outgoing(ctx, "example-chat", []int64{id})
	if err != nil || len(outs) != 1 || outs[0].ReplyTo != "100" {
		t.Fatalf("outs %+v %v", outs, err)
	}
}
