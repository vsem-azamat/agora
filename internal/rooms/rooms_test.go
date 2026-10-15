package rooms_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

type env struct {
	r  *rooms.Rooms
	s  *sessions.Sessions
	db *sql.DB
}

func newEnv(t *testing.T) env { return newEnvAt(t, nil) }

// newEnvAt is newEnv with the clock now (nil for the real one).
func newEnvAt(t *testing.T, now func() time.Time) env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return env{r: rooms.New(db, now), s: sessions.New(db, queue.New(db, nil), rooms.New(db, now), nil), db: db}
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
	msgs, _, err := e.r.Unread(ctx, agent, rooms.Everything, 0)
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
	list, _ := e.r.List(ctx, false)
	if len(list) != 1 || list[0].Name != "general" {
		t.Fatalf("rooms %+v", list)
	}
	got, err := e.r.Subscribe(ctx, "builder", []string{"general"}, false, "")
	if err != nil || !slices.Equal(got, []rooms.Subscription{{Room: "general", Mode: rooms.ModeAll}}) {
		t.Fatalf("followed %v err %v", got, err)
	}
}

func TestCreatingRooms(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "example-app", "work on example-app", "builder"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.r.Subscriptions(ctx, "builder"); !slices.Equal(got, []rooms.Subscription{{Room: "general", Mode: rooms.ModeAll}, {Room: "example-app", Mode: rooms.ModeAll}}) {
		t.Fatalf("followed %v", got)
	}
	if err := e.r.Create(ctx, "example-app", "again", "builder"); !errors.Is(err, rooms.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := e.r.Create(ctx, "no-purpose", "  ", "builder"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("no purpose: %v", err)
	}
	if err := e.r.Create(ctx, "Bad_Name", "x", "builder"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	list, _ := e.r.List(ctx, false)
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
	if _, err := e.r.Subscribe(ctx, "reviewer", []string{"example-app"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "reviewer"); len(got) != 0 {
		t.Fatalf("%d unread", len(got))
	}
	e.post(t, "builder", "example-app", "new")
	if got := e.unread(t, "reviewer"); len(got) != 1 || got[0].Body != "new" {
		t.Fatalf("unread %+v", got)
	}
	if _, err := e.r.Subscribe(ctx, "reviewer", []string{"nowhere"}, true, ""); !errors.Is(err, rooms.ErrNotFound) {
		t.Fatalf("unknown room: %v", err)
	}
	if _, err := e.r.Subscribe(ctx, "reviewer", nil, true, ""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("no rooms: %v", err)
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
	if _, err := e.r.Post(ctx, "builder", "general", "x", 9999); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown reply: %v", err)
	}
	if _, err := e.r.Post(ctx, "builder", "general", "   ", 0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := e.r.Post(ctx, "ghost", "general", "hi", 0); !errors.Is(err, agents.ErrUnknown) {
		t.Fatalf("unknown author: %v", err)
	}
	if _, err := e.r.Post(ctx, rooms.Board, "general", "hi", 0); !errors.Is(err, rooms.ErrBoardOnly) {
		t.Fatalf("posting as the board: %v", err)
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
	if err := rooms.MarkRead(ctx, e.r, "builder", got[1:]); err != nil {
		t.Fatal(err)
	}
	// older: the position does not move back
	if err := rooms.MarkRead(ctx, e.r, "builder", got[:1]); err != nil {
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
	if _, err := e.r.Subscribe(ctx, "follower", []string{"example-app"}, true, ""); err != nil {
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
	if got, _, _ := e.r.Unread(ctx, "outsider", rooms.Addressed, 0); len(got) != 1 {
		t.Fatalf("outsider in general %+v", got)
	}
}

func TestUnreadLimitAndTotal(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	for range 8 {
		e.post(t, "reviewer", "general", "x")
	}
	got, total, _ := e.r.Unread(ctx, "builder", rooms.Everything, 5)
	if len(got) != 5 || total != 8 {
		t.Fatalf("%d of %d", len(got), total)
	}
}

// Rows past the limit are never read: one that cannot be scanned lies beyond it.
func TestUnreadLimitIsAppliedInTheQuery(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	var last int64
	for range 4 {
		last = e.post(t, "reviewer", "general", "x")
	}
	if _, err := e.db.Exec(`UPDATE messages SET at = 'not a time' WHERE id = ?`, last); err != nil {
		t.Fatal(err)
	}
	got, total, err := e.r.Unread(ctx, "builder", rooms.Everything, 2)
	if err != nil || len(got) != 2 || total != 4 {
		t.Fatalf("%d of %d: %v", len(got), total, err)
	}
	// the filter counts too
	if got, total, err = e.r.Unread(ctx, "builder", rooms.Addressed, 2); err != nil || len(got) != 0 || total != 0 {
		t.Fatalf("addressed: %d of %d: %v", len(got), total, err)
	}
}

// Leaving mentions looks at the room's messages only: unreadable messages of other rooms, older
// than the room's own, do not stand in its way, whether they are followed with mentions or all.
func TestLeavingMentionsReadsOnlyItsRoom(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	for _, room := range []string{"example-app", "other-app", "all-app"} {
		if err := e.r.Create(ctx, room, "work", "reviewer"); err != nil {
			t.Fatal(err)
		}
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeMentions)
	e.subscribe(t, "builder", "other-app", rooms.ModeMentions)
	e.subscribe(t, "builder", "all-app", rooms.ModeAll)
	older := []int64{e.post(t, "reviewer", "other-app", "@builder look too"), e.post(t, "reviewer", "other-app", "@all heads up"), e.post(t, "reviewer", "all-app", "@builder and here")}
	e.post(t, "reviewer", "example-app", "chatter")
	addressed := e.post(t, "reviewer", "example-app", "@builder look")
	for _, id := range older {
		if _, err := e.db.Exec(`UPDATE messages SET at = 'not a time' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeAll)
	var pos int64
	if err := e.db.QueryRow(`SELECT last_id FROM read_positions WHERE agent = 'builder' AND room = 'example-app'`).Scan(&pos); err != nil || pos != addressed-1 {
		t.Fatalf("position %d (%v), want %d", pos, err, addressed-1)
	}
}

// The total counts the rows that pass the filter, not those the limit cut.
func TestUnreadTotalCountsTheFilteredRows(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	var want []int64
	for _, body := range []string{"@builder one", "chatter", "@builder two", "more chatter", "@builder three"} {
		if id := e.post(t, "reviewer", "general", body); strings.HasPrefix(body, "@") {
			want = append(want, id)
		}
	}
	got, total, err := e.r.Unread(ctx, "builder", rooms.Addressed, 2)
	if err != nil || total != 3 || !slices.Equal(ids(got), want[:2]) {
		t.Fatalf("%v of %d: %v", ids(got), total, err)
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
	mentions, _, _ := e.r.Unread(ctx, "builder", rooms.Addressed, 0)
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
	if err := rooms.MarkRead(ctx, e.r, "builder", left); err != nil {
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
	if got, _, _ := e.r.Take(ctx, "builder", rooms.Everything, 0); len(got) != 1 {
		t.Fatalf("take %+v", got)
	}
	for range 50 {
		e.post(t, "reviewer", "side", "chatter")
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"side"}, true, ""); err != nil {
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
	if got, _, _ := e.r.Take(ctx, "builder", rooms.Addressed, 0); len(got) != 1 || got[0].Body != "two @builder" {
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
	if n, err := e.r.UnreadCount(ctx, "builder", rooms.Everything); err != nil || n != 3 {
		t.Fatalf("all: %d %v", n, err)
	}
	if n, err := e.r.UnreadCount(ctx, "builder", rooms.Addressed); err != nil || n != 2 {
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
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", other); !errors.Is(err, store.ErrInvalid) {
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

// --- subscription modes ---------------------------------------------------------------

func (e env) subscribe(t *testing.T, agent, room string, mode rooms.Mode) []rooms.Subscription {
	t.Helper()
	subs, err := e.r.Subscribe(ctx, agent, []string{room}, true, mode)
	if err != nil {
		t.Fatalf("subscribe %s to %s: %v", agent, room, err)
	}
	return subs
}

func (e env) modeOf(t *testing.T, agent, room string) rooms.Mode {
	t.Helper()
	subs, err := e.r.Subscriptions(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		if s.Room == room {
			return s.Mode
		}
	}
	return ""
}

func TestSubscribingWithAMode(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	for _, room := range []string{"example-app", "ops"} {
		if err := e.r.Create(ctx, room, "work", "reviewer"); err != nil {
			t.Fatal(err)
		}
	}
	got := e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	want := []rooms.Subscription{{Room: "general", Mode: rooms.ModeAll}, {Room: "example-app", Mode: rooms.ModeWake}}
	if !slices.Equal(got, want) {
		t.Fatalf("subscriptions %+v, want %+v", got, want)
	}
	e.subscribe(t, "builder", "ops", "")
	if m := e.modeOf(t, "builder", "ops"); m != rooms.ModeAll {
		t.Fatalf("default mode %q", m)
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"ops"}, true, "loud"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestChangingTheModeKeepsTheReadingPosition(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	e.post(t, "reviewer", "example-app", "one")
	e.post(t, "reviewer", "example-app", "two")
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	if m := e.modeOf(t, "builder", "example-app"); m != rooms.ModeWake {
		t.Fatalf("mode %q", m)
	}
	if got := e.unread(t, "builder"); len(got) != 2 {
		t.Fatalf("%d unread after changing the mode, want 2", len(got))
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeMentions)
	e.subscribe(t, "builder", "example-app", "")
	if m := e.modeOf(t, "builder", "example-app"); m != rooms.ModeMentions {
		t.Fatalf("subscribing again without a mode changed it to %q", m)
	}
}

func TestGeneralTakesAMode(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	e.post(t, "reviewer", "general", "before")
	e.subscribe(t, "builder", "general", rooms.ModeMentions)
	if _, err := e.r.Subscribe(ctx, "builder", []string{"general"}, false, ""); err != nil {
		t.Fatal(err)
	}
	if m := e.modeOf(t, "builder", "general"); m != rooms.ModeMentions {
		t.Fatalf("mode of general %q", m)
	}
	e.post(t, "reviewer", "general", "chatter")
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("chatter unread in general followed for mentions: %+v", got)
	}
	e.subscribe(t, "builder", "general", rooms.ModeAll)
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("%d unread after following general with all again: leaving mentions skips the chatter", len(got))
	}
	e.post(t, "reviewer", "general", "after")
	if got := e.unread(t, "builder"); len(got) != 1 {
		t.Fatalf("%d unread in general followed with all again, want 1", len(got))
	}
}

func TestMentionsModeCountsOnlyAddressedMessages(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeMentions)
	e.post(t, "reviewer", "example-app", "chatter")
	all := e.post(t, "reviewer", "example-app", "@all main is red")
	name := e.post(t, "reviewer", "example-app", "@builder look")
	got := e.unread(t, "builder")
	if !slices.Equal(ids(got), []int64{all, name}) || !got[0].Addressed || !got[1].Addressed {
		t.Fatalf("unread %+v", got)
	}
	counts, _ := e.r.UnreadByRoom(ctx, "builder")
	if want := []rooms.RoomUnread{{Room: "example-app", Unread: 2, Addressed: 2}}; !slices.Equal(counts, want) {
		t.Fatalf("counts %+v", counts)
	}
}

func TestWakeModeMessagesWake(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	e.post(t, "reviewer", "general", "chatter in general")
	wake := e.post(t, "reviewer", "example-app", "chatter in example-app")
	mention := e.post(t, "reviewer", "general", "@builder look")
	got, total, err := e.r.Unread(ctx, "builder", rooms.Waking, 0)
	if err != nil || total != 2 || !slices.Equal(ids(got), []int64{wake, mention}) {
		t.Fatalf("waking %+v total %d err %v", got, total, err)
	}
	if got[0].Addressed || !got[0].Wakes || !got[1].Addressed || !got[1].Wakes {
		t.Fatalf("marks %+v", got)
	}
	if n, _ := e.r.UnreadCount(ctx, "builder", rooms.Waking); n != 2 {
		t.Fatalf("waking count %d", n)
	}
	if n, _ := e.r.UnreadCount(ctx, "builder", rooms.Addressed); n != 1 {
		t.Fatalf("addressed count %d", n)
	}
	if got, total, _ := e.r.Unread(ctx, "builder", rooms.Quiet, 0); total != 1 || got[0].Body != "chatter in general" {
		t.Fatalf("quiet %+v total %d", got, total)
	}
	if n, _ := e.r.UnreadCount(ctx, "builder", rooms.Quiet); n != 1 {
		t.Fatalf("quiet count %d", n)
	}
	if got, _, _ := e.r.Take(ctx, "builder", rooms.Waking, 0); len(got) != 2 {
		t.Fatalf("take %+v", got)
	}
	if got := e.unread(t, "builder"); len(got) != 1 || got[0].Body != "chatter in general" {
		t.Fatalf("unread after taking what wakes %+v", got)
	}
}

func TestLeavingMentionsKeepsOldChatterRead(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeMentions)
	e.post(t, "reviewer", "example-app", "old chatter")
	all := e.post(t, "reviewer", "example-app", "@all main is red")
	after := e.post(t, "reviewer", "example-app", "chatter after")
	e.subscribe(t, "builder", "example-app", rooms.ModeAll)
	if got := ids(e.unread(t, "builder")); !slices.Equal(got, []int64{all, after}) {
		t.Fatalf("unread %v, want %v", got, []int64{all, after})
	}
	// nothing addressed: the position moves to the newest message
	e.subscribe(t, "builder", "example-app", rooms.ModeMentions)
	if _, _, err := e.r.Take(ctx, "builder", rooms.Everything, 0); err != nil {
		t.Fatal(err)
	}
	e.post(t, "reviewer", "example-app", "more chatter")
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("chatter from the mentions time became unread: %+v", got)
	}
}

func TestFollowingAgainStartsWithAll(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	// a mode is ignored when unsubscribing, even one that does not exist
	if _, err := e.r.Subscribe(ctx, "builder", []string{"example-app"}, false, "loud"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", "")
	if m := e.modeOf(t, "builder", "example-app"); m != rooms.ModeAll {
		t.Fatalf("mode %q after following again", m)
	}
}

func TestBacklogDoesNotWakeAfterChangingToWake(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "example-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeAll)
	for range 200 {
		e.post(t, "reviewer", "example-app", "backlog")
	}
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	if got, total, _ := e.r.Unread(ctx, "builder", rooms.Waking, 0); total != 0 {
		t.Fatalf("the backlog wakes: %d, first %+v", total, got[0])
	}
	if n := len(e.unread(t, "builder")); n != 200 {
		t.Fatalf("%d unread, want 200", n)
	}
	fresh := e.post(t, "reviewer", "example-app", "new")
	if got, _, _ := e.r.Unread(ctx, "builder", rooms.Waking, 0); !slices.Equal(ids(got), []int64{fresh}) {
		t.Fatalf("waking %v, want %v", ids(got), []int64{fresh})
	}
	// subscribing again in the same mode keeps where waking starts
	e.subscribe(t, "builder", "example-app", rooms.ModeWake)
	if _, total, _ := e.r.Unread(ctx, "builder", rooms.Waking, 0); total != 1 {
		t.Fatalf("%d waking after subscribing again with wake", total)
	}
}

// --- archiving ---------------------------------------------------------------------

func TestArchivingARoom(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "reviewer")
	if err := e.r.Create(ctx, "old-app", "work", "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.subscribe(t, "builder", "old-app", rooms.ModeWake)
	first := e.post(t, "reviewer", "old-app", "one")
	second := e.post(t, "reviewer", "old-app", "two")
	if err := e.r.Archive(ctx, "builder", "old-app", true); err != nil {
		t.Fatal(err)
	}

	list, _ := e.r.List(ctx, false)
	if len(list) != 1 || list[0].Name != "general" {
		t.Fatalf("rooms %+v", list)
	}
	all, _ := e.r.List(ctx, true)
	if len(all) != 2 || all[1].Name != "old-app" || all[1].ArchivedBy != "builder" || all[1].ArchivedAt.IsZero() || all[0].ArchivedBy != "" {
		t.Fatalf("all rooms %+v", all)
	}
	hist, _ := e.r.History(ctx, "general", 1)
	if len(hist) != 1 || hist[0].Author != rooms.Board || hist[0].Body != "#old-app archived by builder" {
		t.Fatalf("announcement %+v", hist)
	}
	if got, _ := e.r.History(ctx, "old-app", 0); !slices.Equal(ids(got), []int64{first, second}) {
		t.Fatalf("history of an archived room %+v", got)
	}

	// nothing unread there, nothing wakes, and the room is not listed as followed
	if got := e.unread(t, "builder"); len(got) != 1 || got[0].Room != "general" {
		t.Fatalf("unread while archived %+v", got)
	}
	if n, _ := e.r.UnreadCount(ctx, "builder", rooms.Waking); n != 0 {
		t.Fatalf("waking while archived %d", n)
	}
	if got, _ := e.r.UnreadByRoom(ctx, "builder"); len(got) != 1 || got[0].Room != "general" {
		t.Fatalf("unread by room %+v", got)
	}
	if got, _ := e.r.Subscriptions(ctx, "builder"); !slices.Equal(got, []rooms.Subscription{{Room: "general", Mode: rooms.ModeAll}}) {
		t.Fatalf("followed while archived %v", got)
	}

	// posts and new followers are refused, with how to unarchive
	for _, post := range []func() error{
		func() error { _, err := e.r.Post(ctx, "reviewer", "old-app", "three", 0); return err },
		func() error { _, err := e.r.PostNote(ctx, "reviewer", "old-app", "three", 0); return err },
		func() error { _, err := e.r.PostFromOperator(ctx, "reviewer", "old-app", "three", 0); return err },
	} {
		if err := post(); !errors.Is(err, rooms.ErrArchived) || !strings.Contains(err.Error(), "agora room-unarchive old-app") {
			t.Fatalf("post in an archived room: %v", err)
		}
	}
	if _, err := e.r.Subscribe(ctx, "reviewer", []string{"old-app"}, true, rooms.ModeWake); !errors.Is(err, rooms.ErrArchived) {
		t.Fatalf("mode change in an archived room: %v", err)
	}
	e.join(t, "newcomer")
	if _, err := e.r.Subscribe(ctx, "newcomer", []string{"old-app"}, true, ""); !errors.Is(err, rooms.ErrArchived) {
		t.Fatalf("a new follower of an archived room: %v", err)
	}

	// unarchiving brings everything back as it was
	if err := e.r.Archive(ctx, "reviewer", "old-app", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.r.Subscriptions(ctx, "builder"); !slices.Equal(got, []rooms.Subscription{{Room: "general", Mode: rooms.ModeAll}, {Room: "old-app", Mode: rooms.ModeWake}}) {
		t.Fatalf("followed after unarchiving %v", got)
	}
	got, _, _ := e.r.Unread(ctx, "builder", rooms.Everything, 0)
	var inRoom []int64
	for _, m := range got {
		if m.Room == "old-app" {
			inRoom = append(inRoom, m.ID)
		}
	}
	if !slices.Equal(inRoom, []int64{first, second}) {
		t.Fatalf("unread after unarchiving %+v", got)
	}
	if n, _ := e.r.UnreadCount(ctx, "builder", rooms.Waking); n != 2 {
		t.Fatalf("waking after unarchiving %d", n)
	}
	if hist, _ := e.r.History(ctx, "general", 1); hist[0].Body != "#old-app unarchived by reviewer" {
		t.Fatalf("announcement %+v", hist)
	}
	if list, _ := e.r.List(ctx, false); len(list) != 2 || list[1].ArchivedBy != "" || !list[1].ArchivedAt.IsZero() {
		t.Fatalf("rooms after unarchiving %+v", list)
	}
	e.post(t, "reviewer", "old-app", "back")
}

func TestUnfollowingAnArchivedRoom(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "old-app", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "old-app", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"old-app"}, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "old-app", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.r.Subscriptions(ctx, "builder"); len(got) != 1 {
		t.Fatalf("followed %v", got)
	}
}

func TestArchivingIsRefused(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "old-app", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	announced := func() int {
		h, _ := e.r.History(ctx, "general", 100)
		return len(h)
	}
	before := announced()
	if err := e.r.Archive(ctx, "builder", "general", true); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("archiving general: %v", err)
	}
	if err := e.r.Archive(ctx, "builder", "nowhere", true); !errors.Is(err, rooms.ErrNotFound) {
		t.Fatalf("archiving an unknown room: %v", err)
	}
	if err := e.r.Archive(ctx, "builder", "old-app", false); !errors.Is(err, rooms.ErrNotArchived) {
		t.Fatalf("unarchiving a room that is not archived: %v", err)
	}
	if err := e.r.Archive(ctx, "nobody", "old-app", true); !errors.Is(err, agents.ErrUnknown) {
		t.Fatalf("archiving as an unknown agent: %v", err)
	}
	if err := e.r.Archive(ctx, "builder", "#old-app", true); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "old-app", true); !errors.Is(err, rooms.ErrArchived) {
		t.Fatalf("archiving twice: %v", err)
	}
	if got := announced(); got != before+1 {
		t.Fatalf("announcements %d, want %d", got, before+1)
	}
	if err := e.r.Create(ctx, "old-app", "again", "builder"); !errors.Is(err, rooms.ErrExists) || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("creating an archived room's name: %v", err)
	}
}

func TestNoticesSkipArchivedRooms(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	for _, r := range []string{"aa-old", "bb-app"} {
		if err := e.r.Create(ctx, r, "work", "builder"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.r.Archive(ctx, "builder", "aa-old", true); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Rename(ctx, "builder", "builder-2"); err != nil {
		t.Fatal(err)
	}
	if h, _ := e.r.History(ctx, "bb-app", 1); len(h) != 1 || h[0].Body != "builder is now called builder-2" {
		t.Fatalf("notice %+v", h)
	}
}

// --- threads ----------------------------------------------------------------------

// threadEnv has #example-app, followed by builder, reviewer and tester, and message 40-like
// root, posted by builder.
func threadEnv(t *testing.T) (env, int64) { return threadEnvAt(t, nil) }

func threadEnvAt(t *testing.T, now func() time.Time) (env, int64) {
	t.Helper()
	e := newEnvAt(t, now)
	e.join(t, "builder", "reviewer", "tester")
	if err := e.r.Create(ctx, "example-app", "the app", "builder"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"reviewer", "tester"} {
		if _, err := e.r.Subscribe(ctx, a, []string{"example-app"}, true, rooms.ModeAll); err != nil {
			t.Fatal(err)
		}
	}
	root := e.post(t, "builder", "example-app", "the export is broken")
	e.readAll(t, "builder", "reviewer", "tester")
	return e, root
}

func (e env) readAll(t *testing.T, agents ...string) {
	t.Helper()
	for _, a := range agents {
		if _, _, err := e.r.Take(ctx, a, rooms.Everything, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func (e env) reply(t *testing.T, author string, thread int64, body string, also bool) int64 {
	t.Helper()
	id, err := e.r.Send(ctx, rooms.Posting{Author: author, Room: "example-app", Body: body, Thread: thread, Also: also})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	return id
}

func TestThreadReplyIsInTheThreadOfItsRoot(t *testing.T) {
	e, root := threadEnv(t)
	first := e.reply(t, "reviewer", root, "which export broke?", false)
	second := e.reply(t, "tester", first, "the CSV one", false) // naming a reply names its thread
	msgs, err := e.r.ThreadHistory(ctx, first, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(msgs), []int64{root, first, second}) {
		t.Fatalf("thread %v", ids(msgs))
	}
	for _, m := range msgs[1:] {
		if m.Thread != root {
			t.Fatalf("message %d in thread %d", m.ID, m.Thread)
		}
	}
	if err := e.r.Create(ctx, "other", "another room", "builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Send(ctx, rooms.Posting{Author: "reviewer", Room: "other", Body: "x", Thread: root}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a reply in another room: %v", err)
	}
	if _, err := e.r.Send(ctx, rooms.Posting{Author: "reviewer", Room: "example-app", Body: "x", Thread: 999}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a reply in the thread of no message: %v", err)
	}
}

func TestThreadRepliesStayOutOfTheRoom(t *testing.T) {
	e, root := threadEnv(t)
	if _, err := e.r.Subscribe(ctx, "tester", []string{"example-app"}, true, rooms.ModeWake); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "which export broke?", false)
	if got := e.unread(t, "tester"); len(got) != 0 {
		t.Fatalf("a thread reply is unread for a follower outside the thread: %v", ids(got))
	}
	waking, _, err := e.r.Unread(ctx, "tester", rooms.Waking, 0)
	if err != nil || len(waking) != 0 {
		t.Fatalf("waking %v, %v", ids(waking), err)
	}
	hist, err := e.r.History(ctx, "example-app", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(hist), []int64{root}) {
		t.Fatalf("history %v", ids(hist))
	}
}

func TestTheRootShowsItsThread(t *testing.T) {
	e, root := threadEnv(t)
	e.reply(t, "reviewer", root, "one", false)
	e.reply(t, "tester", root, "two", false)
	last := e.reply(t, "reviewer", root, "three", false)
	hist, err := e.r.HistoryFor(ctx, "example-app", 0, "builder")
	if err != nil {
		t.Fatal(err)
	}
	th := hist[0].Summary
	if th.Replies != 3 || !slices.Equal(th.Repliers, []string{"reviewer", "tester"}) || th.Unread != 3 {
		t.Fatalf("summary %+v", th)
	}
	msgs, _ := e.r.ThreadHistory(ctx, root, 0, "")
	if !th.LastAt.Equal(msgs[len(msgs)-1].At) || msgs[len(msgs)-1].ID != last {
		t.Fatalf("last reply at %v, want %v", th.LastAt, msgs[len(msgs)-1].At)
	}
}

func TestAlsoToTheRoom(t *testing.T) {
	e, root := threadEnv(t)
	id := e.reply(t, "builder", root, "fixed in #57", true)
	hist, err := e.r.History(ctx, "example-app", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(hist), []int64{root, id}) || hist[1].Thread != root || !hist[1].Also {
		t.Fatalf("history %+v", hist)
	}
	for _, a := range []string{"reviewer", "tester"} {
		if got := e.unread(t, a); !slices.Equal(ids(got), []int64{id}) {
			t.Fatalf("%s unread %v", a, ids(got))
		}
	}
	if !strings.Contains(rooms.Format(hist[1], 0), fmt.Sprintf("in thread %d", root)) {
		t.Fatalf("format %q", rooms.Format(hist[1], 0))
	}
}

func TestAReplyAddressesTheThreadsParticipants(t *testing.T) {
	e, root := threadEnv(t)
	if _, err := e.r.Subscribe(ctx, "builder", []string{"example-app"}, true, rooms.ModeMentions); err != nil {
		t.Fatal(err)
	}
	first := e.reply(t, "reviewer", root, "@tester can you check?", false)
	got := e.unread(t, "builder")
	if !slices.Equal(ids(got), []int64{first}) || !got[0].Addressed || !got[0].Wakes {
		t.Fatalf("root author: %+v", got)
	}
	e.readAll(t, "tester")
	second := e.reply(t, "builder", root, "thanks", false)
	for _, a := range []string{"reviewer", "tester"} {
		got := e.unread(t, a)
		if !slices.Equal(ids(got), []int64{second}) || !got[0].Addressed {
			t.Fatalf("%s: %+v", a, got)
		}
	}
	e.readAll(t, "builder")
	e.join(t, "outsider")
	if _, err := e.r.Subscribe(ctx, "outsider", []string{"example-app"}, true, rooms.ModeAll); err != nil {
		t.Fatal(err)
	}
	third := e.reply(t, "reviewer", root, "@all please check", false)
	if got := e.unread(t, "outsider"); len(got) != 0 {
		t.Fatalf("@all in a thread reached a follower outside it: %v", ids(got))
	}
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{third}) { // not its own second
		t.Fatalf("builder %v", ids(got))
	}
}

func TestLeavingAThread(t *testing.T) {
	e, root := threadEnv(t)
	if err := e.r.LeaveThread(ctx, "builder", root); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "one", false)
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("a left thread is unread: %v", ids(got))
	}
	named := e.reply(t, "reviewer", root, "@builder still there?", false)
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{named}) {
		t.Fatalf("a mention by name after leaving: %v", ids(got))
	}
	e.readAll(t, "builder")
	e.reply(t, "builder", root, "back", false)
	again := e.reply(t, "reviewer", root, "welcome back", false)
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{again}) {
		t.Fatalf("after coming back: %v", ids(got))
	}
}

func TestThreadTitles(t *testing.T) {
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e, root := threadEnvAt(t, func() time.Time { clock = clock.Add(time.Second); return clock })
	if err := e.r.SetThreadTitle(ctx, "builder", root, strings.Repeat("x", 101)); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a 101-character title: %v", err)
	}
	if err := e.r.SetThreadTitle(ctx, "builder", root, "Release 2.4"); err != nil {
		t.Fatal(err)
	}
	other := e.post(t, "tester", "example-app", "another topic")
	if err := e.r.SetThreadTitle(ctx, "tester", other, "Docs"); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "newest activity", false) // the clock makes it the newest activity
	list, err := e.r.Threads(ctx, "example-app", "builder", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Root != root || list[0].Title != "Release 2.4" || list[0].Replies != 1 || list[0].Unread != 1 || list[1].Root != other {
		t.Fatalf("threads %+v", list)
	}
	hist, _ := e.r.History(ctx, "example-app", 0)
	if hist[0].ThreadTitle != "Release 2.4" {
		t.Fatalf("root title %q", hist[0].ThreadTitle)
	}
	got := e.unread(t, "builder") // tester's new message, then the reply
	if len(got) != 2 || !strings.Contains(rooms.Format(got[1], 0), fmt.Sprintf("in thread %d «Release 2.4»", root)) {
		t.Fatalf("unread %+v", got)
	}
}

func TestMarkingTheRoomReadLeavesThreadRepliesUnread(t *testing.T) {
	e, root := threadEnv(t)
	reply := e.reply(t, "reviewer", root, "in the thread", false)
	later := e.post(t, "tester", "example-app", "in the room")
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", later); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{reply}) {
		t.Fatalf("unread %v, want the reply only", ids(got))
	}
}

func TestMarkingAThreadRead(t *testing.T) {
	e, root := threadEnv(t)
	first := e.reply(t, "reviewer", root, "one", false)
	second := e.reply(t, "reviewer", root, "two", false)
	room := e.post(t, "tester", "example-app", "in the room")
	third := e.reply(t, "reviewer", root, "three", false)
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", second); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{room, third}) {
		t.Fatalf("unread %v, want %v", ids(got), []int64{room, third})
	}
	_ = first
}

func TestThreadsOfEveryRoom(t *testing.T) {
	e, root := threadEnv(t)
	general := e.post(t, "tester", "general", "elsewhere")
	for id, title := range map[int64]string{root: "Release", general: "Docs"} {
		if err := e.r.SetThreadTitle(ctx, "builder", id, title); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.r.Threads(ctx, "", "", false)
	if err != nil || len(list) != 2 {
		t.Fatalf("threads %+v, %v", list, err)
	}
}

func TestMarkingReadUpToAReplyAlsoInTheRoomReadsItsThread(t *testing.T) {
	e, root := threadEnv(t)
	e.reply(t, "reviewer", root, "in the thread", false)
	also := e.reply(t, "tester", root, "also in the room", true)
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", also); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("unread %v, want none", ids(got))
	}
}

func TestMarkingReadUpToAReplyAlsoInTheRoomReadsTheRoomToo(t *testing.T) {
	e, root := threadEnv(t)
	room := e.post(t, "tester", "example-app", "in the room")
	also := e.reply(t, "reviewer", root, "also in the room", true)
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", also); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("unread %v; message %d of the room should be read", ids(got), room)
	}
}

func TestAnUnreadThreadReplyDoesNotHoldTheRoomBack(t *testing.T) {
	e, root := threadEnv(t)
	reply := e.reply(t, "reviewer", root, "in the thread", false)
	var last int64
	for i := range 200 {
		last = e.post(t, "tester", "example-app", fmt.Sprintf("room message %d", i))
		if i%50 == 49 {
			if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", last); err != nil {
				t.Fatal(err)
			}
		}
	}
	var pos int64
	var marks int
	if err := e.db.QueryRowContext(ctx, `SELECT (SELECT last_id FROM read_positions WHERE agent = 'builder' AND room = 'example-app'),
		(SELECT COUNT(*) FROM read_marks WHERE agent = 'builder')`).Scan(&pos, &marks); err != nil {
		t.Fatal(err)
	}
	if pos != last || marks != 0 {
		t.Fatalf("position %d (want %d), %d read marks", pos, last, marks)
	}
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{reply}) {
		t.Fatalf("unread %v, want the reply", ids(got))
	}
	if err := e.r.LeaveThread(ctx, "builder", root); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("after leaving the thread: %v", ids(got))
	}
}

func TestReadingTheRoomPastTwoThreadReplies(t *testing.T) {
	e, root := threadEnv(t)
	r1 := e.reply(t, "reviewer", root, "one", false)
	e.post(t, "tester", "example-app", "between")
	r2 := e.reply(t, "reviewer", root, "two", false)
	later := e.post(t, "tester", "example-app", "later")
	if _, err := e.r.MarkRoomRead(ctx, "builder", "example-app", later); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); !slices.Equal(ids(got), []int64{r1, r2}) {
		t.Fatalf("unread %v, want %v", ids(got), []int64{r1, r2})
	}
}

func TestUnreadByRoomCountsThreadRepliesApart(t *testing.T) {
	e, root := threadEnv(t)
	e.reply(t, "reviewer", root, "in the thread", false)
	e.reply(t, "reviewer", root, "@builder also here", true)
	e.post(t, "tester", "example-app", "in the room")
	counts, err := e.r.UnreadByRoom(ctx, "builder")
	if err != nil {
		t.Fatal(err)
	}
	want := []rooms.RoomUnread{{Room: "example-app", Unread: 2, Addressed: 1, Threads: 1}}
	if !slices.Equal(counts, want) {
		t.Fatalf("counts %+v, want %+v", counts, want)
	}
	if _, _, err := e.r.Take(ctx, "builder", rooms.Everything, 0); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "only in the thread", false)
	if counts, _ := e.r.UnreadByRoom(ctx, "builder"); !slices.Equal(counts, []rooms.RoomUnread{{Room: "example-app", Threads: 1}}) {
		t.Fatalf("a room with only thread replies unread: %+v", counts)
	}
}

func TestReadingAThreadNamesItsParticipants(t *testing.T) {
	e, root := threadEnv(t)
	e.reply(t, "reviewer", root, "@tester can you check?", false)
	if err := e.r.LeaveThread(ctx, "builder", root); err != nil {
		t.Fatal(err)
	}
	msgs, err := e.r.ThreadHistory(ctx, root, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs[0].Summary.Participants; !slices.Equal(got, []string{"reviewer", "tester"}) {
		t.Fatalf("participants %v", got)
	}
}

func TestAllInAThreadSkipsFollowersForMentions(t *testing.T) {
	e, root := threadEnv(t)
	e.join(t, "quiet")
	if _, err := e.r.Subscribe(ctx, "quiet", []string{"example-app"}, true, rooms.ModeMentions); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "@all please check", false)
	if got := e.unread(t, "quiet"); len(got) != 0 {
		t.Fatalf("@all in a thread reached a mentions follower outside it: %v", ids(got))
	}
}

func TestThreadsOfEveryRoomLeaveOutArchivedRooms(t *testing.T) {
	e, root := threadEnv(t)
	if err := e.r.Create(ctx, "old-app", "gone", "builder"); err != nil {
		t.Fatal(err)
	}
	old := e.post(t, "builder", "old-app", "old")
	for id, title := range map[int64]string{root: "Release", old: "Old"} {
		if err := e.r.SetThreadTitle(ctx, "builder", id, title); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.r.Archive(ctx, "builder", "old-app", true); err != nil {
		t.Fatal(err)
	}
	list, err := e.r.Threads(ctx, "", "", false)
	if err != nil || len(list) != 1 || list[0].Root != root {
		t.Fatalf("threads %+v, %v", list, err)
	}
	if err := e.r.SetThreadTitle(ctx, "builder", old, "Renamed"); !errors.Is(err, rooms.ErrArchived) {
		t.Fatalf("titling in an archived room: %v", err)
	}
}

func TestLeavingAThreadReadsItsRepliesAlsoInTheRoom(t *testing.T) {
	e, root := threadEnv(t)
	if _, err := e.r.Subscribe(ctx, "builder", []string{"example-app"}, true, rooms.ModeMentions); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "also in the room", true)
	if err := e.r.LeaveThread(ctx, "builder", root); err != nil {
		t.Fatal(err)
	}
	if got := e.unread(t, "builder"); len(got) != 0 {
		t.Fatalf("after leaving: %v", ids(got))
	}
}

func TestListingUnreadThreadsWithoutTitles(t *testing.T) {
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e, root := threadEnvAt(t, func() time.Time { clock = clock.Add(time.Second); return clock })
	titled := e.post(t, "tester", "example-app", "docs")
	if err := e.r.SetThreadTitle(ctx, "tester", titled, "Docs"); err != nil {
		t.Fatal(err)
	}
	e.reply(t, "reviewer", root, "unread for builder", false)
	list, err := e.r.Threads(ctx, "example-app", "builder", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Root != root || list[0].Title != "" || list[0].Author != "builder" || list[0].Unread != 1 || list[1].Root != titled {
		t.Fatalf("threads %+v", list)
	}
	if list, _ := e.r.Threads(ctx, "example-app", "builder", false); len(list) != 1 {
		t.Fatalf("titled only: %+v", list)
	}
}

func TestProjectRoomIsDerivedFromTheProjectName(t *testing.T) {
	for project, want := range map[string]string{
		"example-app":                  "example-app",
		"Example-App":                  "example-app",
		" example-app ":                "example-app",
		"my.app":                       "my-app",
		"my_app":                       "my-app",
		"my app":                       "my-app",
		"my._ app":                     "my-app", // repeats collapse
		"my--app":                      "my-app",
		"-my-app-":                     "my-app",
		".hidden":                      "hidden",
		"2048-game":                    "p-2048-game",
		"1":                            "p-1",
		"3d.viewer":                    "p-3d-viewer",
		"ab":                           "ab",
		strings.Repeat("a", 40):        strings.Repeat("a", 32),
		strings.Repeat("a", 31) + "-b": strings.Repeat("a", 31), // no trailing dash after the cut
		strings.Repeat("a", 30) + ".b": strings.Repeat("a", 30) + "-b",
		"1" + strings.Repeat("a", 40):  "p-1" + strings.Repeat("a", 29), // the prefix counts toward 32
		"":                             "",
		"a":                            "", // too short for a room name
		"---":                          "",
		"naïve":                        "", // not a room name once mapped
		"my/app":                       "",
	} {
		if got := rooms.ProjectRoom(project); got != want {
			t.Errorf("ProjectRoom(%q) = %q, want %q", project, got, want)
		}
		if want != "" && !rooms.ValidName(want) {
			t.Errorf("%q is no valid room name", want)
		}
	}
}

// follows lists the rooms agent follows besides #general, with their modes, as "room" or "room(mode)".
func (e env) follows(t *testing.T, agent string) []string {
	t.Helper()
	subs, err := e.r.Subscriptions(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range subs {
		if s.Room == rooms.General && s.Mode == rooms.ModeAll {
			continue // followed implicitly
		}
		if s.Mode == rooms.ModeAll {
			out = append(out, s.Room)
		} else {
			out = append(out, fmt.Sprintf("%s(%s)", s.Room, s.Mode))
		}
	}
	slices.Sort(out)
	return out
}

// setProject sets the profile project of agent, as an update of the profile does before the
// project room follows it.
func (e env) setProject(t *testing.T, agent, project string) {
	t.Helper()
	if _, err := e.db.ExecContext(ctx, `UPDATE agents SET project = ? WHERE name = ?`, project, agent); err != nil {
		t.Fatal(err)
	}
}

// followProject sets the project, then follows its room.
func (e env) followProject(t *testing.T, agent, project string) rooms.ProjectFollow {
	t.Helper()
	e.setProject(t, agent, project)
	got, err := e.r.FollowProject(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestChangingTheProjectMovesTheProjectRoom(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "owner")
	for _, room := range []string{"alpha", "beta"} {
		if err := e.r.Create(ctx, room, "work", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	got := e.followProject(t, "builder", "alpha")
	if got != (rooms.ProjectFollow{Room: "alpha", State: rooms.ProjectFollowing}) {
		t.Fatalf("first project: %+v", got)
	}
	got = e.followProject(t, "builder", "alpha")
	if got != (rooms.ProjectFollow{Room: "alpha", State: rooms.ProjectFollowing}) || !slices.Equal(e.follows(t, "builder"), []string{"alpha"}) {
		t.Fatalf("the same project: %+v %v", got, e.follows(t, "builder"))
	}
	got = e.followProject(t, "builder", "Beta")
	if got != (rooms.ProjectFollow{Room: "beta", State: rooms.ProjectFollowing, Unfollowed: "alpha"}) {
		t.Fatalf("another project: %+v", got)
	}
	if f := e.follows(t, "builder"); !slices.Equal(f, []string{"beta"}) {
		t.Fatalf("follows %v", f)
	}
	// a project without a room gives up the old one and says there is none
	got = e.followProject(t, "builder", "gamma")
	if got != (rooms.ProjectFollow{Room: "gamma", State: rooms.ProjectNoRoom, Unfollowed: "beta"}) || len(e.follows(t, "builder")) != 0 {
		t.Fatalf("project without a room: %+v %v", got, e.follows(t, "builder"))
	}
	// a project with no room name only gives up the old one
	e.followProject(t, "builder", "alpha")
	got = e.followProject(t, "builder", "my/app")
	if got != (rooms.ProjectFollow{Unfollowed: "alpha"}) || len(e.follows(t, "builder")) != 0 {
		t.Fatalf("project without a room name: %+v %v", got, e.follows(t, "builder"))
	}
}

func TestTheAgentsOwnSubscriptionsAreNotMovedWithTheProject(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "owner")
	for _, room := range []string{"alpha", "beta"} {
		if err := e.r.Create(ctx, room, "work", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for name, touch := range map[string]func(){
		"followed before": func() {},
		"mode changed": func() {
			if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, true, rooms.ModeWake); err != nil {
				t.Fatal(err)
			}
		},
		"followed again by hand": func() {
			if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, true, ""); err != nil {
				t.Fatal(err)
			}
		},
		"unfollowed and followed by hand": func() {
			for _, follow := range []bool{false, true} {
				if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, follow, ""); err != nil {
					t.Fatal(err)
				}
			}
		},
	} {
		if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha", "beta"}, false, ""); err != nil {
			t.Fatal(err)
		}
		if name == "followed before" {
			if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, true, ""); err != nil {
				t.Fatal(err)
			}
		}
		e.followProject(t, "builder", "alpha")
		touch()
		got := e.followProject(t, "builder", "beta")
		if got.Unfollowed != "" {
			t.Fatalf("%s: %+v", name, got)
		}
		if f := e.follows(t, "builder"); !slices.Contains(f, "beta") || !slices.ContainsFunc(f, func(s string) bool { return strings.HasPrefix(s, "alpha") }) {
			t.Fatalf("%s: follows %v", name, f)
		}
	}
}

func TestAnArchivedProjectRoomIsNotFollowed(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "alpha", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "alpha", true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, false, ""); err != nil {
		t.Fatal(err)
	}
	got := e.followProject(t, "builder", "alpha")
	if got.State != rooms.ProjectFailed || got.Detail == "" || len(e.follows(t, "builder")) != 0 {
		t.Fatalf("%+v %v", got, e.follows(t, "builder"))
	}
}

func TestAnArchivedProjectRoomAlreadyFollowedIsNotReportedAsFollowed(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	if err := e.r.Create(ctx, "alpha", "work", "builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"alpha"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Archive(ctx, "builder", "alpha", true); err != nil {
		t.Fatal(err)
	}
	if got := e.followProject(t, "builder", "alpha"); got.State != rooms.ProjectFailed || got.Detail == "" {
		t.Fatalf("%+v", got)
	}
}

// Two updates of the profile can reach the rooms in the other order than they were stored; the
// follow converges on the project the profile has now.
func TestTheProjectRoomFollowsTheCurrentProjectWhateverTheOrderOfCalls(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "owner")
	for _, room := range []string{"alpha", "beta", "gamma"} {
		if err := e.r.Create(ctx, room, "work", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	e.followProject(t, "builder", "alpha")
	e.setProject(t, "builder", "beta") // the update to beta is stored, its follow has not run
	e.followProject(t, "builder", "gamma")
	if _, err := e.r.FollowProject(ctx, "builder"); err != nil { // the late follow of the update to beta
		t.Fatal(err)
	}
	if f := e.follows(t, "builder"); !slices.Equal(f, []string{"gamma"}) {
		t.Fatalf("follows %v, want only gamma", f)
	}
}

func TestMessageMustBeValidUTF8(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Join(ctx, "builder", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Post(ctx, "builder", "general", "cut in the middle of é: \xc3", 0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid text: %v", err)
	}
	if msgs, _ := e.r.History(ctx, "general", 10); len(msgs) != 0 {
		t.Fatalf("stored %+v", msgs)
	}
	if _, err := e.r.Post(ctx, "builder", "general", "é ok", 0); err != nil {
		t.Fatal(err)
	}
}

// A refusal says only what was refused; errors.Is still finds its kind.
func TestARefusalDoesNotStartWithItsKind(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder")
	err := e.r.Create(ctx, "Bad Name", "work", "builder")
	if !errors.Is(err, store.ErrInvalid) || strings.HasPrefix(err.Error(), store.ErrInvalid.Error()) {
		t.Fatalf("a bad name: %v", err)
	}
	if err := e.r.Create(ctx, "general", "work", "builder"); !errors.Is(err, rooms.ErrExists) || strings.HasPrefix(err.Error(), rooms.ErrExists.Error()) {
		t.Fatalf("an existing room: %v", err)
	}
	if _, err := e.r.Post(ctx, "builder", "no-such-room", "hello", 0); !errors.Is(err, rooms.ErrNotFound) || strings.HasPrefix(err.Error(), rooms.ErrNotFound.Error()) {
		t.Fatalf("a missing room: %v", err)
	}
}
