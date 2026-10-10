package governance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/governance"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

type env struct {
	g *governance.Governance
	r *rooms.Rooms
}

func newEnv(t *testing.T) env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := rooms.New(db, nil)
	s := sessions.New(db, queue.New(db, nil), r, nil)
	for _, n := range []string{"builder", "reviewer", "tester"} {
		if _, err := s.Join(ctx, n, "", false); err != nil {
			t.Fatal(err)
		}
	}
	return env{g: governance.New(db, r, nil), r: r}
}

func (e env) general(t *testing.T) []rooms.Message {
	t.Helper()
	h, err := e.r.History(ctx, "general", 50)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestProposingAnnouncesToEveryone(t *testing.T) {
	e := newEnv(t)
	id, err := e.g.Propose(ctx, "builder", "Merge only under the merge lock", "Take example-app/merge before merging.")
	if err != nil || id != 1 {
		t.Fatalf("id %d err %v", id, err)
	}
	p, _ := e.g.Get(ctx, id)
	if p.State != "open" || p.Author != "builder" {
		t.Fatalf("proposal %+v", p)
	}
	h := e.general(t)
	if len(h) != 1 || h[0].Author != "agora" || !strings.Contains(h[0].Body, "@all new proposal #1") || !strings.Contains(h[0].Body, "agora vote 1") {
		t.Fatalf("announcement %+v", h)
	}
	if got, _, _ := e.r.Unread(ctx, "reviewer", rooms.Addressed, 0); len(got) != 1 {
		t.Fatalf("not addressed to everyone: %+v", got)
	}
}

func TestInvalidProposalsAreRefused(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{"", "text"}, {"title", " "}, {strings.Repeat("t", 121), "text"}} {
		if _, err := e.g.Propose(ctx, "builder", c[0], c[1]); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%q: err = %v", c[0], err)
		}
	}
	if _, err := e.g.Propose(ctx, "ghost", "t", "x"); !errors.Is(err, agents.ErrUnknown) {
		t.Errorf("unknown author: %v", err)
	}
}

func TestLatestVoteCounts(t *testing.T) {
	e := newEnv(t)
	id, _ := e.g.Propose(ctx, "builder", "Rule", "Text")
	if _, err := e.g.Cast(ctx, "reviewer", id, "no", "too strict"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Cast(ctx, "reviewer", id, "yes", "fine after all"); err != nil {
		t.Fatal(err)
	}
	p, _ := e.g.Get(ctx, id)
	if len(p.Votes) != 1 || p.Votes[0].Choice != "yes" || p.Votes[0].Reason != "fine after all" {
		t.Fatalf("votes %+v", p.Votes)
	}
	if _, err := e.g.Cast(ctx, "reviewer", id, "maybe", ""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad choice: %v", err)
	}
}

func TestClosing(t *testing.T) {
	e := newEnv(t)
	id, _ := e.g.Propose(ctx, "builder", "Rule", "Text")
	if _, err := e.g.Cast(ctx, "reviewer", id, "yes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Cast(ctx, "tester", id, "yes", ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.g.Get(ctx, id); p.State != "open" {
		t.Fatal("the board accepted a proposal by itself")
	}
	if err := e.g.Close(ctx, "builder", id, "accepted"); err != nil {
		t.Fatal(err)
	}
	p, _ := e.g.Get(ctx, id)
	if p.State != "accepted" || p.ClosedBy != "builder" || p.ClosedAt.IsZero() {
		t.Fatalf("proposal %+v", p)
	}
	if h := e.general(t); !strings.Contains(h[len(h)-1].Body, "Proposal #1 accepted by builder") {
		t.Fatalf("announcement %q", h[len(h)-1].Body)
	}
	if err := e.g.Close(ctx, "reviewer", id, "rejected"); !errors.Is(err, governance.ErrClosed) {
		t.Fatalf("closing twice: %v", err)
	}
	if _, err := e.g.Cast(ctx, "tester", id, "no", ""); !errors.Is(err, governance.ErrClosed) {
		t.Fatalf("voting on closed: %v", err)
	}
}

func TestListing(t *testing.T) {
	e := newEnv(t)
	a, _ := e.g.Propose(ctx, "builder", "A", "x")
	if _, err := e.g.Propose(ctx, "builder", "B", "x"); err != nil {
		t.Fatal(err)
	}
	if err := e.g.Close(ctx, "builder", a, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	open, _ := e.g.List(ctx, false)
	all, _ := e.g.List(ctx, true)
	if len(open) != 1 || open[0].Title != "B" || len(all) != 2 {
		t.Fatalf("open %d all %d", len(open), len(all))
	}
}

func TestCharter(t *testing.T) {
	e := newEnv(t)
	c, _ := e.g.Charter(ctx)
	if !strings.Contains(c.Body, "# Agora charter") || c.ChangedBy != "agora" {
		t.Fatalf("default charter %+v", c)
	}
	open, _ := e.g.Propose(ctx, "builder", "Rule", "x")
	if err := e.g.SetCharter(ctx, "builder", open, "# New"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("open proposal: %v", err)
	}
	if err := e.g.SetCharter(ctx, "builder", 99, "# New"); !errors.Is(err, governance.ErrNotFound) {
		t.Fatalf("missing proposal: %v", err)
	}
	if err := e.g.Close(ctx, "builder", open, "accepted"); err != nil {
		t.Fatal(err)
	}
	if err := e.g.SetCharter(ctx, "builder", open, "  "); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if err := e.g.SetCharter(ctx, "builder", open, "# Agora charter\n\n1. Be kind."); err != nil {
		t.Fatal(err)
	}
	c, _ = e.g.Charter(ctx)
	if c.Body != "# Agora charter\n\n1. Be kind.\n" || c.ChangedBy != "builder" || c.ProposalID != open {
		t.Fatalf("charter %+v", c)
	}
	if h := e.general(t); !strings.Contains(h[len(h)-1].Body, "Charter updated by builder after proposal #1") {
		t.Fatalf("announcement %q", h[len(h)-1].Body)
	}
}

func TestOneCharterChangePerProposal(t *testing.T) {
	e := newEnv(t)
	id, _ := e.g.Propose(ctx, "builder", "Rule", "x")
	if err := e.g.Close(ctx, "builder", id, "Accepted"); err != nil {
		t.Fatal(err)
	}
	if err := e.g.SetCharter(ctx, "builder", id, "# One"); err != nil {
		t.Fatal(err)
	}
	if err := e.g.SetCharter(ctx, "builder", id, "# Two"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("reused proposal: %v", err)
	}
}

func TestTitlesAreOneLineAndMentionNobody(t *testing.T) {
	e := newEnv(t)
	if _, err := e.g.Propose(ctx, "builder", "two\nlines", "x"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("newline: %v", err)
	}
	if _, err := e.g.Propose(ctx, "builder", "ping @reviewer", "x"); err != nil {
		t.Fatal(err)
	}
	h := e.general(t)
	if got, _ := rooms.Mentions(h[len(h)-1].Body); len(got) != 0 {
		t.Fatalf("announcement mentions %v", got)
	}
}

func TestChoicesIgnoreCase(t *testing.T) {
	e := newEnv(t)
	id, _ := e.g.Propose(ctx, "builder", "Rule", "x")
	if _, err := e.g.Cast(ctx, "reviewer", id, "Yes", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.g.Close(ctx, "builder", id, "REJECTED"); err != nil {
		t.Fatal(err)
	}
}
