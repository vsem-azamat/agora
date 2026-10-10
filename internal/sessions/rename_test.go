package sessions_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/governance"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

func (e env) rename(t *testing.T, agent, name string) {
	t.Helper()
	if err := e.s.Rename(ctx, agent, name); err != nil {
		t.Fatalf("rename %s to %s: %v", agent, name, err)
	}
}

func (e env) post(t *testing.T, author, room, body string) int64 {
	t.Helper()
	id, err := e.r.Post(ctx, author, room, body, 0)
	if err != nil {
		t.Fatalf("post %q: %v", body, err)
	}
	return id
}

func (e env) history(t *testing.T, room string) []rooms.Message {
	t.Helper()
	msgs, err := e.r.History(ctx, room, 100)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestRenamingKeepsWhatIsTheAgents(t *testing.T) {
	e := newEnv(t)
	g := governance.New(e.db, e.r, e.clock.now)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "fixer", "session-1")
	e.join(t, "builder", "")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "fixer", "merging #57", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Create(ctx, "example-app", "the example app", "fixer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Subscribe(ctx, "fixer", []string{"example-app"}, true, rooms.ModeWake); err != nil {
		t.Fatal(err)
	}
	e.post(t, "builder", "example-app", "main is green")
	if _, _, err := e.r.Take(ctx, "fixer", rooms.Everything, 0); err != nil { // fixer has read everything so far
		t.Fatal(err)
	}
	posted := e.post(t, "fixer", "general", "taking #57")
	proposal, err := g.Propose(ctx, "fixer", "Lock before deploys", "Take deploy/<env> before any deploy.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Cast(ctx, "fixer", proposal, "yes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(ctx, `INSERT INTO pull_requests (agent, repo, number, found) VALUES ('fixer', 'github.com/example-org/example-app', 57, 1)`); err != nil {
		t.Fatal(err)
	}

	e.rename(t, "fixer", "docs-writer")

	if got, _ := e.s.Resolve(ctx, "session-1"); got != "docs-writer" {
		t.Errorf("session acts as %q", got)
	}
	if p := e.places(t, "docs-writer"); len(p) != 1 || p[0].Key != "example-app/merge" || p[0].State != queue.Held {
		t.Errorf("places: %+v", p)
	}
	if followed, _ := e.r.Subscriptions(ctx, "docs-writer"); !slices.Contains(followed, rooms.Subscription{Room: "example-app", Mode: rooms.ModeWake}) {
		t.Errorf("follows %v", followed)
	}
	unread, _, err := e.r.Unread(ctx, "docs-writer", rooms.Everything, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range unread {
		if m.Body == "main is green" || m.Author == "docs-writer" {
			t.Errorf("unread after the rename: %+v", m)
		}
	}
	if p, err := e.a.Get(ctx, "docs-writer"); err != nil || !slices.Contains(p.FoundPRs, 57) {
		t.Errorf("pull requests %v, err %v", p.FoundPRs, err)
	}
	if p, err := g.Get(ctx, proposal); err != nil || p.Author != "docs-writer" || len(p.Votes) != 1 || p.Votes[0].Agent != "docs-writer" {
		t.Errorf("proposal %+v, err %v", p, err)
	}
	for _, m := range e.history(t, "general") {
		if m.ID == posted && m.Author != "docs-writer" {
			t.Errorf("posted message by %q", m.Author)
		}
	}
	if _, err := e.a.Get(ctx, "fixer"); err == nil {
		t.Error("fixer is still an agent")
	}
}

func TestRenamingRefusesTakenNames(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "builder", "")
	if err := e.s.Rename(ctx, "fixer", "builder"); !errors.Is(err, agents.ErrTaken) {
		t.Errorf("another agent's name: %v", err)
	}
	for _, bad := range []string{"Docs_Writer", "all", "agora", "fixer"} {
		if err := e.s.Rename(ctx, "fixer", bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, _, err := e.q.Join(ctx, "example-app/deploy", "deployer", "", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Rename(ctx, "fixer", "deployer"); !errors.Is(err, agents.ErrTaken) {
		t.Errorf("a name in a queue: %v", err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/db", "migrator", "", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Release(ctx, "example-app/db", "migrator", "builder", true); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Rename(ctx, "fixer", "migrator"); !errors.Is(err, agents.ErrTaken) {
		t.Errorf("a name in the record of forced removals: %v", err)
	}
	if err := e.s.Rename(ctx, "ghost", "spirit"); !errors.Is(err, agents.ErrUnknown) {
		t.Errorf("unknown agent: %v", err)
	}
	if _, err := e.a.Get(ctx, "fixer"); err != nil {
		t.Fatalf("fixer lost its name after refusals: %v", err)
	}
}

func TestFormerNamesStayReserved(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "builder", "")
	e.rename(t, "fixer", "docs-writer")
	err := e.s.Rename(ctx, "builder", "fixer")
	if !errors.Is(err, agents.ErrTaken) || !strings.Contains(err.Error(), `now called "docs-writer"`) {
		t.Errorf("renaming to a former name: %v", err)
	}
	if _, err := e.s.Join(ctx, "fixer", "", false); !errors.Is(err, agents.ErrTaken) || !strings.Contains(err.Error(), `now called "docs-writer"`) {
		t.Errorf("joining as a former name: %v", err)
	}
}

func TestTakingBackAFormerName(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "builder", "")
	e.rename(t, "fixer", "docs-writer")
	e.clock.add(time.Minute)
	e.rename(t, "docs-writer", "fixer")
	p, err := e.a.Get(ctx, "fixer")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Formerly) != 1 || p.Formerly[0].Name != "docs-writer" {
		t.Errorf("formerly %+v", p.Formerly)
	}
	if err := e.s.Rename(ctx, "builder", "docs-writer"); !errors.Is(err, agents.ErrTaken) {
		t.Errorf("the name given up again is not reserved: %v", err)
	}
}

func TestFormerNamesAreRecordedNewestFirst(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	first := e.clock.now()
	e.rename(t, "fixer", "docs-writer")
	e.clock.add(time.Hour)
	e.rename(t, "docs-writer", "reviewer")
	p, err := e.a.Get(ctx, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	want := []agents.FormerName{{Name: "docs-writer", At: first.Add(time.Hour)}, {Name: "fixer", At: first}}
	if len(p.Formerly) != len(want) {
		t.Fatalf("formerly %+v", p.Formerly)
	}
	for i, f := range p.Formerly {
		if f.Name != want[i].Name || !f.At.Equal(want[i].At) {
			t.Errorf("formerly[%d] = %+v, want %+v", i, f, want[i])
		}
	}
}

func TestActingUnderAFormerNameNamesTheNewOne(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "builder", "")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "fixer", "", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/db", "builder", "", 0, true); err != nil {
		t.Fatal(err)
	}
	e.rename(t, "fixer", "docs-writer")
	refused := func(what string, err error) {
		t.Helper()
		var former *agents.FormerNameError
		if !errors.As(err, &former) || former.Current != "docs-writer" || !strings.Contains(err.Error(), `now called "docs-writer"`) {
			t.Errorf("%s as a former name: %v", what, err)
		}
	}
	_, err := e.r.Post(ctx, "fixer", "general", "hello", 0)
	refused("posting", err)
	_, _, err = e.q.Join(ctx, "example-app/deploy", "fixer", "", 0, true)
	refused("locking", err)
	_, err = e.q.Claim(ctx, "example-app/merge", "fixer")
	refused("claiming", err)
	_, err = e.q.Renew(ctx, "example-app/merge", "fixer")
	refused("renewing", err)
	_, err = e.q.Release(ctx, "example-app/merge", "fixer", "fixer", false)
	refused("releasing", err)
	_, err = e.q.Release(ctx, "example-app/db", "builder", "fixer", true)
	refused("force-releasing", err)
	if p := e.places(t, "docs-writer"); len(p) != 1 || p[0].State != queue.Held {
		t.Errorf("places after the refusals: %+v", p)
	}
	if p := e.places(t, "builder"); len(p) != 1 {
		t.Errorf("builder lost its place: %+v", p)
	}
}

func TestRenamesAreAnnounced(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "solo", "")
	if err := e.r.Create(ctx, "example-app", "the example app", "fixer"); err != nil {
		t.Fatal(err)
	}
	e.rename(t, "fixer", "docs-writer")
	e.rename(t, "solo", "loner")
	count := func(room, body string) int {
		n := 0
		for _, m := range e.history(t, room) {
			if m.Body == body && m.Author == rooms.Board {
				n++
			}
		}
		return n
	}
	for _, room := range []string{"general", "example-app"} {
		if n := count(room, "fixer is now called docs-writer"); n != 1 {
			t.Errorf("#%s: %d announcements", room, n)
		}
	}
	if n := count("general", "solo is now called loner"); n != 1 {
		t.Errorf("announcements without a project room: %d", n)
	}
	if n := count("example-app", "solo is now called loner"); n != 0 {
		t.Errorf("announced in a room the agent does not follow: %d", n)
	}
}

func TestMentioningAFormerName(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	e.join(t, "builder", "")
	before := e.post(t, "builder", "general", "@fixer before the rename")
	e.rename(t, "fixer", "docs-writer")
	after := e.post(t, "builder", "general", "@fixer can you look at #57?")
	capital := e.post(t, "builder", "general", "@Fixer please check")
	msgs, _, err := e.r.Unread(ctx, "docs-writer", rooms.Addressed, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, m := range msgs {
		ids = append(ids, m.ID)
		if m.ID == before && m.Body != "@fixer before the rename" {
			t.Errorf("earlier body rewritten: %q", m.Body)
		}
	}
	if !slices.Contains(ids, before) || !slices.Contains(ids, after) || !slices.Contains(ids, capital) {
		t.Errorf("addressed to docs-writer: %v, want %d, %d and %d", ids, before, after, capital)
	}
}
