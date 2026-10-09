package forge_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/forge"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]forge.Repo{
		"git@github.com:example-org/example-app.git":          {Host: "github.com", Path: "example-org/example-app"},
		"https://github.com/example-org/example-app":          {Host: "github.com", Path: "example-org/example-app"},
		"https://user@GitHub.com/example-org/example-app/":    {Host: "github.com", Path: "example-org/example-app"},
		"ssh://git@github.com:22/example-org/example-app.git": {Host: "github.com", Path: "example-org/example-app"},
		"https://git.example.com/group/sub/example-app.git":   {Host: "git.example.com", Path: "group/sub/example-app"},
	}
	for url, want := range cases {
		if got, ok := forge.ParseRemote(url); !ok || got != want {
			t.Errorf("%s: %+v %v", url, got, ok)
		}
	}
	for _, url := range []string{"", "/srv/git/example-app.git", "../example-app", "file:///srv/git/example-app"} {
		if got, ok := forge.ParseRemote(url); ok {
			t.Errorf("%s: parsed as %+v", url, got)
		}
	}
}

const reply = `{"data":{"repository":{"defaultBranchRef":{"name":"dev"},
"b0":{"nodes":[
 {"number":57,"headRefName":"fix/login-timeout","headRefOid":"abc","isCrossRepository":false,"state":"OPEN","isDraft":false,"mergeable":"CONFLICTING",
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
   {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS"},
   {"__typename":"CheckRun","name":"docs","status":"COMPLETED","conclusion":"SKIPPED"},
   {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"CANCELLED"},
   {"__typename":"CheckRun","name":"e2e","status":"COMPLETED","conclusion":"TIMED_OUT"},
   {"__typename":"CheckRun","name":"build","status":"IN_PROGRESS","conclusion":null},
   {"__typename":"StatusContext","context":"ci/legacy","state":"ERROR"},
   {"__typename":"StatusContext","context":"ci/wait","state":"PENDING"}]}}}}]}},
 {"number":58,"headRefName":"fix/login-timeout","headRefOid":"def","isCrossRepository":true,"state":"OPEN","isDraft":true,"mergeable":"MERGEABLE",
  "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}]},
"n0":{"number":41,"headRefName":"feat/old","headRefOid":"0a1","isCrossRepository":false,"state":"MERGED","isDraft":false,"mergeable":"UNKNOWN","commits":{"nodes":[]}},
"n1":null}},
"errors":[{"type":"NOT_FOUND","path":["repository","n1"],"message":"Could not resolve to a PullRequest with the number of 9999."}]}`

func TestGitHubLookup(t *testing.T) {
	var args []string
	gh := &forge.GitHub{Run: func(_ context.Context, a []string) ([]byte, error) {
		args = a
		return []byte(reply), errors.New("exit status 1") // gh exits 1 when a number is not found
	}}
	res, err := gh.Lookup(context.Background(), forge.Repo{Host: "github.com", Path: "example-org/example-app"},
		forge.Query{Branches: []string{"fix/login-timeout"}, Numbers: []int{41, 9999}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"api graphql", "--hostname github.com", "owner=example-org", "name=example-app", "b0=fix/login-timeout", "n0=41", "n1=9999"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if res.DefaultBranch != "dev" || len(res.PRs) != 3 {
		t.Fatalf("result %+v", res)
	}
	p := res.PRs[0]
	if p.Number != 57 || p.Branch != "fix/login-timeout" || p.Head != "abc" || !p.Open || p.Draft || p.Fork || !p.Conflicts {
		t.Fatalf("pr %+v", p)
	}
	outcomes := map[string]forge.Outcome{}
	for _, c := range p.Checks {
		outcomes[c.Name] = c.Outcome
	}
	want := map[string]forge.Outcome{"lint": forge.Succeeded, "docs": forge.Succeeded, "test": forge.Cancelled, "e2e": forge.Failed,
		"build": forge.Unfinished, "ci/legacy": forge.Failed, "ci/wait": forge.Unfinished}
	for k, v := range want {
		if outcomes[k] != v {
			t.Errorf("%s: %v, want %v", k, outcomes[k], v)
		}
	}
	if q := res.PRs[1]; !q.Fork || !q.Draft || q.Conflicts || len(q.Checks) != 0 {
		t.Fatalf("fork pr %+v", q)
	}
	if q := res.PRs[2]; q.Number != 41 || q.Open {
		t.Fatalf("merged pr %+v", q)
	}
}

func TestGitHubLookupFails(t *testing.T) {
	gh := &forge.GitHub{Run: func(context.Context, []string) ([]byte, error) {
		return []byte(`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository"}]}`), errors.New("exit status 1")
	}}
	if _, err := gh.Lookup(context.Background(), forge.Repo{Host: "github.com", Path: "example-org/gone"}, forge.Query{}); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("err = %v", err)
	}
	if _, err := gh.Lookup(context.Background(), forge.Repo{Host: "github.com", Path: "group/sub/example-app"}, forge.Query{}); err == nil {
		t.Fatal("a nested path was looked up")
	}
}

func TestQueriesAreDeduplicated(t *testing.T) {
	var args []string
	gh := &forge.GitHub{Run: func(_ context.Context, a []string) ([]byte, error) {
		args = a
		return []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"}}}}`), nil
	}}
	gh.Lookup(context.Background(), forge.Repo{Host: "github.com", Path: "example-org/example-app"},
		forge.Query{Branches: []string{"a", "a", ""}, Numbers: []int{3, 3}})
	if slices.Contains(args, "b1=a") || slices.Contains(args, "n1=3") || slices.Contains(args, "b0=") {
		t.Fatalf("duplicates queried: %v", args)
	}
}
