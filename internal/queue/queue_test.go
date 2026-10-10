package queue_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newQueue(t *testing.T) (*queue.Queue, *clock) {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	return queue.New(db, c.now), c
}

var ctx = context.Background()

func join(t *testing.T, q *queue.Queue, key, agent string) *queue.Entry {
	t.Helper()
	e, _, err := q.Join(ctx, key, agent, "", 10*time.Minute, false)
	if err != nil {
		t.Fatalf("join %s %s: %v", key, agent, err)
	}
	return e
}

func state(t *testing.T, q *queue.Queue, key, agent string) (queue.State, int) {
	t.Helper()
	rs, _, err := q.List(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		for _, e := range r.Entries {
			if e.Agent == agent {
				return e.State, e.Position
			}
		}
	}
	return "", 0
}

func expect(t *testing.T, q *queue.Queue, key, agent string, want queue.State, wantPos int) {
	t.Helper()
	got, pos := state(t, q, key, agent)
	if got != want || pos != wantPos {
		t.Fatalf("%s in %s: got %q at %d, want %q at %d", agent, key, got, pos, want, wantPos)
	}
}

// Requirement: Resources Have Slots

func TestFirstUseOfAKeyHasOneSlot(t *testing.T) {
	q, _ := newQueue(t)
	_, res, err := q.Join(ctx, "example-app/merge", "a", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Slots != 1 {
		t.Fatalf("slots = %d, want 1", res.Slots)
	}
}

func TestMoreSlotsLetMoreAgentsHold(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.SetSlots(ctx, "heavy/typecheck", 3); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a", "b", "c"} {
		if e := join(t, q, "heavy/typecheck", a); e.State != queue.Held {
			t.Fatalf("%s: %s, want held", a, e.State)
		}
	}
	if e := join(t, q, "heavy/typecheck", "d"); e.State != queue.Waiting || e.Position != 1 {
		t.Fatalf("d: %s at %d, want waiting at 1", e.State, e.Position)
	}
}

func TestFewerSlotsKeepCurrentHolders(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.SetSlots(ctx, "heavy/build", 3); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a", "b", "c", "d"} {
		join(t, q, "heavy/build", a)
	}
	if _, err := q.SetSlots(ctx, "heavy/build", 1); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a", "b", "c"} {
		expect(t, q, "heavy/build", a, queue.Held, 0)
	}
	if _, err := q.Release(ctx, "heavy/build", "a", "a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Release(ctx, "heavy/build", "b", "b", false); err != nil {
		t.Fatal(err)
	}
	expect(t, q, "heavy/build", "d", queue.Waiting, 1)
	if _, err := q.Release(ctx, "heavy/build", "c", "c", false); err != nil {
		t.Fatal(err)
	}
	expect(t, q, "heavy/build", "d", queue.Offered, 0)
}

func TestInvalidKeysAreRefused(t *testing.T) {
	q, _ := newQueue(t)
	for _, key := range []string{"../etc", "Example", "", "a..b", "/root"} {
		if _, _, err := q.Join(ctx, key, "a", "", 0, false); !errors.Is(err, queue.ErrInvalid) {
			t.Errorf("key %q: err = %v, want ErrInvalid", key, err)
		}
	}
}

// Requirement: Joining A Queue

func TestJoiningAFreeResourceHoldsUntilLeaseEnds(t *testing.T) {
	q, c := newQueue(t)
	e, _, err := q.Join(ctx, "db/shared", "a", "migrating", 10*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != queue.Held || !e.Expires.Equal(c.now().Add(10*time.Minute)) {
		t.Fatalf("got %s until %v", e.State, e.Expires)
	}
}

func TestDefaultLeaseIsThirtyMinutes(t *testing.T) {
	q, c := newQueue(t)
	e, _, _ := q.Join(ctx, "db/shared", "a", "", 0, false)
	if !e.Expires.Equal(c.now().Add(30 * time.Minute)) {
		t.Fatalf("expires %v, want 30 minutes from now", e.Expires)
	}
}

func TestJoiningABusyResourceWaitsInLine(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "first")
	if e := join(t, q, "r", "second"); e.State != queue.Waiting || e.Position != 2 {
		t.Fatalf("got %s at %d, want waiting at 2", e.State, e.Position)
	}
}

func TestJoiningTwiceKeepsPlaceAndUpdatesNote(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "first")
	join(t, q, "r", "second")
	e, _, err := q.Join(ctx, "r", "second", "new note", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.Position != 2 || e.Note != "new note" {
		t.Fatalf("got position %d note %q", e.Position, e.Note)
	}
}

func TestJoiningAgainWithANewLeaseRestartsAHeldSlot(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "a") // 10-minute lease
	c.add(5 * time.Minute)
	e, _, err := q.Join(ctx, "r", "a", "", 2*time.Hour, false)
	if err != nil || e.Lease != 2*time.Hour || !e.Expires.Equal(c.now().Add(2*time.Hour)) {
		t.Fatalf("entry %+v, err %v", e, err)
	}
	c.add(time.Hour)
	if e, err = q.Renew(ctx, "r", "a"); err != nil || !e.Expires.Equal(c.now().Add(2*time.Hour)) {
		t.Fatalf("renewed %+v, err %v", e, err)
	}
}

func TestWaitingAgentJoiningAgainWithANewLeaseHoldsForIt(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	if _, _, err := q.Join(ctx, "r", "a", "", 2*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	e, err := q.Claim(ctx, "r", "a")
	if err != nil || e.State != queue.Held || !e.Expires.Equal(c.now().Add(2*time.Hour)) {
		t.Fatalf("entry %+v, err %v", e, err)
	}
}

func TestOfferedAgentJoiningAgainWithANewLeaseKeepsTheClaimDeadline(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	deadline := c.now().Add(queue.ClaimWindow)
	c.add(time.Minute)
	e, _, err := q.Join(ctx, "r", "a", "", 2*time.Hour, false)
	if err != nil || e.State != queue.Offered || !e.Expires.Equal(deadline) || e.Lease != 2*time.Hour {
		t.Fatalf("entry %+v, err %v", e, err)
	}
	if e, err = q.Renew(ctx, "r", "a"); err != nil || !e.Expires.Equal(c.now().Add(2*time.Hour)) {
		t.Fatalf("claimed %+v, err %v", e, err)
	}
}

func TestJoiningAgainWithoutALeaseKeepsIt(t *testing.T) {
	q, c := newQueue(t)
	if _, _, err := q.Join(ctx, "r", "a", "", 2*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	end := c.now().Add(2 * time.Hour)
	c.add(time.Minute)
	e, _, err := q.Join(ctx, "r", "a", "note", 0, false)
	if err != nil || e.Lease != 2*time.Hour || !e.Expires.Equal(end) || e.Note != "note" {
		t.Fatalf("entry %+v, err %v", e, err)
	}
}

func TestSimultaneousJoinsGrantExactlyOne(t *testing.T) {
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := queue.New(db, nil)
	var wg sync.WaitGroup
	agents := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, a := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := q.Join(ctx, "r", a, "", 0, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rs, _, _ := q.List(ctx, "r")
	held, positions := 0, map[int]bool{}
	for _, e := range rs[0].Entries {
		if e.State == queue.Held {
			held++
		} else {
			positions[e.Position] = true
		}
	}
	if held != 1 || len(positions) != len(agents)-1 {
		t.Fatalf("held %d, distinct positions %d", held, len(positions))
	}
}

// Requirement: Slots Are Granted In Order

func TestFreedSlotGoesToFirstWaiter(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	expect(t, q, "r", "a", queue.Offered, 0)
	expect(t, q, "r", "b", queue.Waiting, 1)
}

// Requirement: An Offered Slot Must Be Claimed

func TestClaimingAnOfferHoldsForTheLease(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	c.add(time.Minute)
	e, err := q.Renew(ctx, "r", "a")
	if err != nil {
		t.Fatal(err)
	}
	if e.State != queue.Held || !e.Expires.Equal(c.now().Add(10*time.Minute)) {
		t.Fatalf("got %s until %v", e.State, e.Expires)
	}
}

func TestMissedTurnMovesToTheEnd(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	c.add(queue.ClaimWindow)
	expect(t, q, "r", "b", queue.Offered, 0)
	expect(t, q, "r", "a", queue.Waiting, 1)
}

func TestMissingTwiceLeavesTheQueue(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	// the hub sweeps every second; sweep after each step as it would
	c.add(queue.ClaimWindow) // a misses, b is offered
	if _, err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(queue.ClaimWindow) // b misses, a is offered again
	if _, err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, q, "r", "a", queue.Offered, 0)
	c.add(queue.ClaimWindow) // a misses a second time
	if _, err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := state(t, q, "r", "a"); s != "" {
		t.Fatalf("a is still %s", s)
	}
}

func TestWaitingAgentClaimsAtOnce(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "holder")
	join(t, q, "r", "a")
	if _, err := q.Release(ctx, "r", "holder", "holder", false); err != nil {
		t.Fatal(err)
	}
	e, err := q.Claim(ctx, "r", "a")
	if err != nil {
		t.Fatal(err)
	}
	if e.State != queue.Held {
		t.Fatalf("got %s, want held", e.State)
	}
}

// Requirement: Held Slots Are Leases

func TestRenewingExtendsFromNow(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "a")
	c.add(8 * time.Minute)
	e, err := q.Renew(ctx, "r", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Expires.Equal(c.now().Add(10 * time.Minute)) {
		t.Fatalf("expires %v", e.Expires)
	}
}

func TestExpiredLeaseFreesTheSlot(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	c.add(10 * time.Minute)
	changed, err := q.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != "r" {
		t.Fatalf("changed %v", changed)
	}
	expect(t, q, "r", "b", queue.Offered, 0)
	if s, _ := state(t, q, "r", "a"); s != "" {
		t.Fatalf("a is still %s", s)
	}
}

func TestRenewWhileWaitingIsRefused(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	if _, err := q.Renew(ctx, "r", "b"); !errors.Is(err, queue.ErrNotYourTurn) {
		t.Fatalf("err = %v", err)
	}
}

// Requirement: Leaving A Queue

func TestReleaseOffersTheNextAgent(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	ok, err := q.Release(ctx, "r", "a", "a", false)
	if err != nil || !ok {
		t.Fatalf("released %v, err %v", ok, err)
	}
	expect(t, q, "r", "b", queue.Offered, 0)
}

func TestReleasingSomeoneElsesSlotNeedsForce(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "a")
	_, err := q.Release(ctx, "r", "a", "b", false)
	var forbidden *queue.ForbiddenError
	if !errors.As(err, &forbidden) || forbidden.Agent != "a" {
		t.Fatalf("err = %v", err)
	}
	if ok, err := q.Release(ctx, "r", "a", "b", true); err != nil || !ok {
		t.Fatalf("forced release: %v %v", ok, err)
	}
}

func TestReleasingWhenNotQueued(t *testing.T) {
	q, _ := newQueue(t)
	ok, err := q.Release(ctx, "r", "a", "a", false)
	if err != nil || ok {
		t.Fatalf("released %v, err %v", ok, err)
	}
}

// Requirement: Locks Are Queues That Do Not Wait

func TestLockOnAFreeResource(t *testing.T) {
	q, c := newQueue(t)
	e, _, err := q.Join(ctx, "example-app/merge", "a", "merging #57", 10*time.Minute, true)
	if err != nil || e == nil || e.State != queue.Held || !e.Expires.Equal(c.now().Add(10*time.Minute)) {
		t.Fatalf("entry %+v, err %v", e, err)
	}
}

func TestLockOnATakenResourceDoesNotQueue(t *testing.T) {
	q, _ := newQueue(t)
	if _, _, err := q.Join(ctx, "example-app/merge", "a", "merging #57", 0, true); err != nil {
		t.Fatal(err)
	}
	e, res, err := q.Join(ctx, "example-app/merge", "b", "", 0, true)
	if err != nil || e != nil {
		t.Fatalf("entry %+v, err %v", e, err)
	}
	if h := res.Holders(); len(h) != 1 || h[0].Agent != "a" || h[0].Note != "merging #57" {
		t.Fatalf("holders %+v", h)
	}
	if s, _ := state(t, q, "example-app/merge", "b"); s != "" {
		t.Fatalf("b is %s", s)
	}
}

// Requirement: Listing Resources

func TestListingShowsHoldersAndWaitersAndHidesIdleResources(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.SetSlots(ctx, "heavy/typecheck", 3); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a", "b", "c", "d", "e"} {
		join(t, q, "heavy/typecheck", a)
	}
	join(t, q, "gone", "x")
	if _, err := q.Release(ctx, "gone", "x", "x", false); err != nil {
		t.Fatal(err)
	}
	rs, _, err := q.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Key != "heavy/typecheck" {
		t.Fatalf("resources %+v", rs)
	}
	if len(rs[0].Holders()) != 3 {
		t.Fatalf("holders %d", len(rs[0].Holders()))
	}
	expect(t, q, "heavy/typecheck", "d", queue.Waiting, 1)
	expect(t, q, "heavy/typecheck", "e", queue.Waiting, 2)
}

// Requirement: Queues Survive A Restart

func TestQueuesSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(db, nil)
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	db.Close()

	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q = queue.New(db, nil)
	expect(t, q, "r", "a", queue.Held, 0)
	expect(t, q, "r", "b", queue.Waiting, 1)
}

func TestLockWhileWaitingIsRefused(t *testing.T) {
	q, _ := newQueue(t)
	join(t, q, "r", "a")
	join(t, q, "r", "b")
	e, _, err := q.Join(ctx, "r", "b", "", 0, true)
	if err != nil || e != nil {
		t.Fatalf("entry %+v, err %v", e, err)
	}
	expect(t, q, "r", "b", queue.Waiting, 1)
}

func TestLockingAgainRenewsTheLease(t *testing.T) {
	q, c := newQueue(t)
	if _, _, err := q.Join(ctx, "r", "a", "", 10*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	c.add(5 * time.Minute)
	e, _, err := q.Join(ctx, "r", "a", "longer", 2*time.Hour, true)
	if err != nil || e == nil || !e.Expires.Equal(c.now().Add(2*time.Hour)) || e.Note != "longer" {
		t.Fatalf("entry %+v, err %v", e, err)
	}
}

func TestInvalidLeasesAndSlotsAreRefused(t *testing.T) {
	q, _ := newQueue(t)
	for _, lease := range []time.Duration{-time.Minute, time.Millisecond, time.Second - time.Millisecond, queue.MaxLease + time.Second} {
		if _, _, err := q.Join(ctx, "r", "a", "", lease, false); !errors.Is(err, queue.ErrInvalid) || !strings.Contains(err.Error(), "between 1s and") {
			t.Errorf("lease %v: err = %v", lease, err)
		}
		if _, _, err := q.Join(ctx, "r", "a", "", lease, true); !errors.Is(err, queue.ErrInvalid) || !strings.Contains(err.Error(), "between 1s and") {
			t.Errorf("lock for %v: err = %v", lease, err)
		}
	}
	for _, n := range []int{0, queue.MaxSlots + 1} {
		if _, err := q.SetSlots(ctx, "r", n); !errors.Is(err, queue.ErrInvalid) {
			t.Errorf("slots %d: err = %v", n, err)
		}
	}
}

func TestListingReportsWhetherItSettledAQueue(t *testing.T) {
	q, c := newQueue(t)
	join(t, q, "example-app/merge", "builder")
	join(t, q, "example-app/merge", "reviewer")
	if _, settled, err := q.List(ctx, ""); err != nil || settled {
		t.Fatalf("nothing to settle: settled %v, err %v", settled, err)
	}
	c.add(11 * time.Minute) // builder's lease ends: reviewer is offered the lock
	if _, settled, err := q.List(ctx, ""); err != nil || !settled {
		t.Fatalf("expired lease: settled %v, err %v", settled, err)
	}
	if _, settled, _ := q.List(ctx, ""); settled {
		t.Fatal("listing again settled something")
	}
}
