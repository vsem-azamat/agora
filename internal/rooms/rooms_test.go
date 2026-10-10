package rooms_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

type env struct {
	r *rooms.Rooms
	s *sessions.Sessions
}

func newEnv(t *testing.T) env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return env{r: rooms.New(db, nil), s: sessions.New(db, queue.New(db, nil), rooms.New(db, nil), nil)}
}

func (e env) join(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := e.s.Join(ctx, n, "", false); err != nil {
			t.Fatal(err)
		}
	}
}

func (e env) post(t *testing.T, author, room, body string) int64 {
	t.Helper()
	id, err := e.r.Post(ctx, author, room, body, 0)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return id
}

func (e env) unread(t *testing.T, agent string) []rooms.Message {
	t.Helper()
	msgs, _, err := e.r.Unread(ctx, agent, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func ids(msgs []rooms.Message) []int64 {
	var out []int64
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// --- mentions ---------------------------------------------------------------------

func TestMentionParsing(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{"@builder can you take #57?", []string{"builder"}},
		{"ping @builder-2", []string{"builder-2"}},
		{"mail ops@builder.example", nil},
		{"@ab, @reviewer and @reviewer again", []string{"ab", "reviewer"}},
		{"(@builder)", []string{"builder"}},
		{"é@builder", nil},
		{"@Builder, please look", []string{"builder"}},
		{"thanks @builder-", []string{"builder"}},
		{"**@builder**", []string{"builder"}},
	} {
		if got, _ := rooms.Mentions(tc.body); !slices.Equal(got, tc.want) {
			t.Errorf("%q: %v, want %v", tc.body, got, tc.want)
		}
	}
	if _, all := rooms.Mentions("@all please review"); !all {
		t.Error("@all not found")
	}
}

// --- rooms ------------------------------------------------------------------------

func TestGeneralExistsAndStaysFollowed(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	list, _ := e.r.List(ctx)
	if len(list) != 1 || list[0].Name != "general" {
		t.Fatalf("rooms %+v", list)
	}
	got, err := e.r.Subscribe(ctx, "builder", []string{"general"}, false)
	if err != nil || !slices.Equal(got, []string{"general"}) {
		t.Fatalf("followed %v err %v", got, err)
	}
}

func TestCreatingRooms(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "example-app", "work on example-app", "builder"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.r.Followed(ctx, "builder"); !slices.Equal(got, []string{"general", "example-app"}) {
		t.Fatalf("followed %v", got)
	}
	if err := e.r.Create(ctx, "example-app", "again", "builder"); !errors.Is(err, rooms.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := e.r.Create(ctx, "no-purpose", "  ", "builder"); !errors.Is(err, rooms.ErrInvalid) {
		t.Fatalf("no purpose: %v", err)
	}
	if err := e.r.Create(ctx, "Bad_Name", "x", "builder"); !errors.Is(err, rooms.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	list, _ := e.r.List(ctx)
	if len(list) != 2 {
		t.Fatalf("rooms %+v", list)
	}
}

func TestSubscribingToARoomWithHistoryStartsAtItsNewestMessage(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		e.post(t, "builder", "example-app", "old")
	}
	if _, err := e.r.Subscribe(ctx, "reviewer", []string{"example-app"}, true); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "reviewer"); len(got) != 0 {
		t.Fatalf("%d unread", len(got))
	}
	e.post(t, "builder", "example-app", "new")
	if got := e.unread(t, "reviewer"); len(got) != 1 || got[0].Body != "new" {
		t.Fatalf("unread %+v", got)
	}
	if _, err := e.r.Subscribe(ctx, "reviewer", []string{"nowhere"}, true); !errors.Is(err, rooms.ErrNotFound) {
		t.Fatalf("unknown room: %v", err)
	}
}

// --- messages ----------------------------------------------------------------------

func TestPostingAndReplying(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	first := e.post(t, "builder", "general", "PR #57 is ready for review")
	reply, err := e.r.Post(ctx, "builder", "general", "merged", first)
	if err != nil || reply <= first {
		t.Fatalf("reply %d err %v", reply, err)
	}
	h, _ := e.r.History(ctx, "general", 0)
	if len(h) != 2 || h[1].ReplyTo != first || !strings.Contains(rooms.Format(h[1], 0), "re ") {
		t.Fatalf("history %+v", h)
	}
	if _, err := e.r.Post(ctx, "builder", "general", "x", 9999); !errors.Is(err, rooms.ErrInvalid) {
		t.Fatalf("unknown reply: %v", err)
	}
	if _, err := e.r.Post(ctx, "builder", "general", "   ", 0); !errors.Is(err, rooms.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := e.r.Post(ctx, "ghost", "general", "hi", 0); !errors.Is(err, rooms.ErrNotFound) {
		t.Fatalf("unknown author: %v", err)
	}
}

func TestConcurrentPostsAllLand(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.r.Post(ctx, "builder", "general", "hi", 0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if h, _ := e.r.History(ctx, "general", 100); len(h) != 20 {
		t.Fatalf("%d messages", len(h))
	}
}

func TestPostingDoesNotMarkOthersRead(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "one")
	e.post(t, "reviewer", "general", "two")
	e.post(t, "builder", "general", "mine")
	if got := e.unread(t, "builder"); len(got) != 2 {
		t.Fatalf("unread %+v", got)
	}
}

func TestHistoryDoesNotChangeReadState(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	for range 7 {
		e.post(t, "reviewer", "general", "x")
	}
	h, _ := e.r.History(ctx, "general", 5)
	if len(h) != 5 || h[0].ID >= h[4].ID {
		t.Fatalf("history %v", ids(h))
	}
	if got := e.unread(t, "builder"); len(got) != 7 {
		t.Fatalf("unread %d", len(got))
	}
}

// --- unread ------------------------------------------------------------------------

func TestWhatCountsAsUnread(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "side", "side talk", "reviewer"); err != nil {
		t.Fatal(err)
	}
	followedMsg := e.post(t, "reviewer", "general", "hello")
	mention := e.post(t, "reviewer", "side", "@builder look here")
	e.post(t, "reviewer", "side", "chatter")
	got := e.unread(t, "builder")
	if !slices.Equal(ids(got), []int64{followedMsg, mention}) || got[0].Addressed || !got[1].Addressed {
		t.Fatalf("unread %+v", got)
	}
}

func TestNewAgentsStartFromNow(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	for range 30 {
		e.post(t, "builder", "general", "before")
	}
	e.join(t, "late")
	if got := e.unread(t, "late"); len(got) != 0 {
		t.Fatalf("%d unread", len(got))
	}
	e.post(t, "builder", "general", "after")
	if got := e.unread(t, "late"); len(got) != 1 {
		t.Fatalf("%d unread", len(got))
	}
}

func TestMarkingReadAndPeeking(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "a")
	e.post(t, "reviewer", "general", "b")
	got := e.unread(t, "builder")
	if len(got) != 2 {
		t.Fatal(len(got))
	}
	if err := e.r.MarkRead(ctx, "builder", got[1:]); err != nil {
		t.Fatal(err)
	}
	// older: the position does not move back
	if err := e.r.MarkRead(ctx, "builder", got[:1]); err != nil {
		t.Fatal(err)
	}
	if again := e.unread(t, "builder"); len(again) != 0 {
		t.Fatalf("still unread %+v", again)
	}
}

func TestAllAddressesTheRoomAudience(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "follower", "outsider")
	if err := e.r.Create(ctx, "example-app", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Subscribe(ctx, "follower", []string{"example-app"}, true); err != nil {
		t.Fatal(err)
	}
	e.post(t, "builder", "example-app", "@all heads up")
	if got := e.unread(t, "follower"); len(got) != 1 || !got[0].Addressed {
		t.Fatalf("follower %+v", got)
	}
	if got := e.unread(t, "outsider"); len(got) != 0 {
		t.Fatalf("outsider %+v", got)
	}
	e.post(t, "builder", "general", "@all everyone")
	if got, _, _ := e.r.Unread(ctx, "outsider", true, 0); len(got) != 1 {
		t.Fatalf("outsider in general %+v", got)
	}
}

func TestUnreadLimitAndTotal(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	for range 8 {
		e.post(t, "reviewer", "general", "x")
	}
	got, total, _ := e.r.Unread(ctx, "builder", false, 5)
	if len(got) != 5 || total != 8 {
		t.Fatalf("%d of %d", len(got), total)
	}
}

func TestFormatShortensLongBodies(t *testing.T) {
	m := rooms.Message{ID: 3, Room: "general", Author: "builder", Body: strings.Repeat("x", 800)}
	if out := rooms.Format(m, 700); !strings.HasSuffix(out, "…") || strings.Count(out, "x") != 700 {
		t.Fatalf("format %q", out[len(out)-10:])
	}
}

func TestMarkingSingleMessagesKeepsEarlierOnesUnread(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "chatter")
	e.post(t, "reviewer", "general", "@builder look")
	mentions, _, _ := e.r.Unread(ctx, "builder", true, 0)
	if len(mentions) != 1 {
		t.Fatalf("mentions %+v", mentions)
	}
	if err := e.r.MarkEach(ctx, "builder", mentions); err != nil {
		t.Fatal(err)
	}
	left := e.unread(t, "builder")
	if len(left) != 1 || left[0].Body != "chatter" {
		t.Fatalf("unread %+v", left)
	}
	if err := e.r.MarkRead(ctx, "builder", left); err != nil {
		t.Fatal(err)
	}
	if again := e.unread(t, "builder"); len(again) != 0 {
		t.Fatalf("unread again %+v", again)
	}
}

func TestSubscribingAfterADeliveredMentionStartsAtTheNewestMessage(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "side", "side talk", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.post(t, "reviewer", "side", "@builder one question")
	if got, _, _ := e.r.Take(ctx, "builder", false, 0); len(got) != 1 {
		t.Fatalf("take %+v", got)
	}
	for range 50 {
		e.post(t, "reviewer", "side", "chatter")
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"side"}, true); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("%d unread after subscribing", len(got))
	}
}

func TestTakingMentionsKeepsOtherMessagesUnread(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "one")
	e.post(t, "reviewer", "general", "two @builder")
	if got, _, _ := e.r.Take(ctx, "builder", true, 0); len(got) != 1 || got[0].Body != "two @builder" {
		t.Fatalf("take %+v", got)
	}
	if got := e.unread(t, "builder"); len(got) != 1 || got[0].Body != "one" {
		t.Fatalf("unread %+v", got)
	}
}

func TestCountingUnreadMessages(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "chatter")
	e.post(t, "reviewer", "general", "@builder look")
	e.post(t, "reviewer", "general", "@all heads up")
	if n, err := e.r.UnreadCount(ctx, "builder", false); err != nil || n != 3 {
		t.Fatalf("all: %d %v", n, err)
	}
	if n, err := e.r.UnreadCount(ctx, "builder", true); err != nil || n != 2 {
		t.Fatalf("addressed: %d %v", n, err)
	}
}

// --- unread per room and marking a room read ------------------------------------------

func TestUnreadCountsPerRoom(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "the app", "builder"); err != nil {
		t.Fatal(err)
	}
	e.post(t, "reviewer", "example-app", "first")
	e.post(t, "reviewer", "example-app", "@builder second")
	e.post(t, "reviewer", "example-app", "third")
	e.post(t, "builder", "general", "my own message")
	counts, err := e.r.UnreadByRoom(ctx, "builder")
	if err != nil {
		t.Fatal(err)
	}
	if want := []rooms.RoomUnread{{Room: "example-app", Unread: 3, Addressed: 1}}; !slices.Equal(counts, want) {
		t.Fatalf("counts %+v, want %+v", counts, want)
	}
	if n := len(e.unread(t, "builder")); n != 3 {
		t.Fatalf("counting changed read state: %d unread", n)
	}
}

func TestMarkingARoomRead(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "the app", "builder"); err != nil {
		t.Fatal(err)
	}
	e.post(t, "reviewer", "example-app", "one")
	second := e.post(t, "reviewer", "example-app", "two")
	third := e.post(t, "reviewer", "example-app", "three")
	other := e.post(t, "reviewer", "general", "elsewhere")
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", other); !errors.Is(err, rooms.ErrInvalid) {
		t.Fatalf("message from another room: %v", err)
	}
	if got := ids(e.unread(t, "builder")); len(got) != 4 {
		t.Fatalf("refused mark changed read state: %v", got)
	}
	if moved, err := e.r.MarkRoomRead(ctx, "builder", "#example-app", second); err != nil || !moved {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	if got := ids(e.unread(t, "builder")); !slices.Equal(got, []int64{third, other}) {
		t.Fatalf("unread %v", got)
	}
	if moved, err := e.r.MarkRoomRead(ctx, "builder", "example-app", second-1); err != nil || moved {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	if got := ids(e.unread(t, "builder")); !slices.Equal(got, []int64{third, other}) {
		t.Fatalf("position moved backwards: unread %v", got)
	}
}

func TestMarkingARoomReadNeverReachesBehindTheJoin(t *testing.T) {
	e := newEnv(t)
	e.join(t, "reviewer")
	old := e.post(t, "reviewer", "general", "before builder joined")
	e.post(t, "reviewer", "general", "also before")
	e.join(t, "builder")
	if moved, err := e.r.MarkRoomRead(ctx, "builder", "general", old); err != nil || moved {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("messages from before the join became unread: %v", ids(got))
	}
}
