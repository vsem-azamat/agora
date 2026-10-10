package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GitHub looks up pull requests through the `gh` command-line tool, with whatever credentials
// it already has; Agora stores no tokens.
type GitHub struct {
	// Run runs gh with args and returns its standard output; nil runs the real gh.
	Run func(ctx context.Context, args []string) ([]byte, error)
}

// Limits of one lookup.
const (
	// branchPRs is how many open pull requests are asked for per branch.
	branchPRs = 5
	// maxChecks is how many checks are asked for per pull request; with more, it is incomplete.
	maxChecks = 100
	// maxSuites is how many check suites are asked for per pull request.
	maxSuites = 50
)

var prFields = fmt.Sprintf(`fragment pr on PullRequest {
  number headRefName headRefOid isCrossRepository state isDraft mergeable
  commits(last: 1) { nodes { commit {
    statusCheckRollup { contexts(first: %d) {
      nodes {
        __typename
        ... on CheckRun { name status conclusion }
        ... on StatusContext { context state }
      }
      pageInfo { hasNextPage }
    } }
    checkSuites(first: %d) { nodes { status workflowRun { id } } }
  } } }
}`, maxChecks, maxSuites)

// The GraphQL aliases of the i-th branch and the i-th pull request number in a lookup start
// with these prefixes.
const branchPrefix, numberPrefix = "b", "n"

func branchAlias(i int) string { return branchPrefix + strconv.Itoa(i) }
func numberAlias(i int) string { return numberPrefix + strconv.Itoa(i) }

// Lookup asks for the default branch, the open pull requests from each branch and each pull
// request by number, in one GraphQL request.
func (g *GitHub) Lookup(ctx context.Context, repo Repo, q Query) (Result, error) {
	owner, name, ok := strings.Cut(repo.Path, "/")
	if !ok || strings.Contains(name, "/") {
		return Result{}, fmt.Errorf("%s is not an owner/name repository", repo.Key())
	}
	lq := newLookup(repo.Host, owner, name, q)
	run := g.Run
	if run == nil {
		run = runGH
	}
	out, err := run(ctx, lq.args)
	return lq.parse(out, err)
}

// lookup is one GraphQL request: the gh arguments, and the branches and numbers it asks for,
// in alias order.
type lookup struct {
	args     []string
	branches []string
	numbers  []int
}

func newLookup(host, owner, name string, q Query) lookup {
	var lq lookup
	vars := []string{"$owner: String!", "$name: String!"}
	fields := []string{"defaultBranchRef { name }"}
	lq.args = []string{"api", "graphql", "--hostname", host, "-f", "owner=" + owner, "-f", "name=" + name}
	for _, b := range q.Branches {
		if b == "" || slices.Contains(lq.branches, b) {
			continue
		}
		k := branchAlias(len(lq.branches))
		lq.branches = append(lq.branches, b)
		vars = append(vars, "$"+k+": String!")
		fields = append(fields, fmt.Sprintf("%s: pullRequests(headRefName: $%s, states: OPEN, first: %d) { nodes { ...pr } }", k, k, branchPRs))
		lq.args = append(lq.args, "-f", k+"="+b)
	}
	for _, n := range q.Numbers {
		if n <= 0 || slices.Contains(lq.numbers, n) {
			continue
		}
		k := numberAlias(len(lq.numbers))
		lq.numbers = append(lq.numbers, n)
		vars = append(vars, "$"+k+": Int!")
		fields = append(fields, k+": pullRequest(number: $"+k+") { ...pr }")
		lq.args = append(lq.args, "-F", k+"="+strconv.Itoa(n))
	}
	query := "query(" + strings.Join(vars, ", ") + ") {\n  repository(owner: $owner, name: $name) {\n    " +
		strings.Join(fields, "\n    ") + "\n  }\n}\n" + prFields
	lq.args = append(lq.args, "-f", "query="+query)
	return lq
}

// parse reads gh's output for the lookup; runErr is how gh ended.
func (lq lookup) parse(out []byte, runErr error) (Result, error) {
	var resp struct {
		Data struct {
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Path    []any  `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	err := json.Unmarshal(out, &resp)
	// A pull request number that does not exist makes gh fail but still prints the rest of the
	// reply. That reply is used only when every error is such a missing pull request.
	missing := map[string]bool{}
	usable := err == nil && resp.Data.Repository != nil
	var msgs []string
	for _, e := range resp.Errors {
		msgs = append(msgs, e.Message)
		if e.Type == "NOT_FOUND" && len(e.Path) == 2 && e.Path[0] == "repository" {
			if alias, ok := e.Path[1].(string); ok && strings.HasPrefix(alias, numberPrefix) {
				missing[alias] = true
				continue
			}
		}
		usable = false
	}
	if !usable {
		if err == nil {
			err = errors.New("no usable reply")
		}
		return Result{}, fmt.Errorf("gh: %s: %w", strings.Join(msgs, "; "), errors.Join(runErr, err))
	}

	var res Result
	var def struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(resp.Data.Repository["defaultBranchRef"], &def) // absent or null: no default branch
	res.DefaultBranch = def.Name
	seenPR := map[int]bool{}
	add := func(p ghPR) {
		if p.Number == 0 || seenPR[p.Number] {
			return
		}
		seenPR[p.Number] = true
		res.PRs = append(res.PRs, p.pr())
	}
	for i := range lq.branches {
		var list struct {
			Nodes []ghPR `json:"nodes"`
		}
		_ = json.Unmarshal(resp.Data.Repository[branchAlias(i)], &list) // absent or null: no pull requests
		for _, p := range list.Nodes {
			add(p)
		}
	}
	for i, n := range lq.numbers {
		k := numberAlias(i)
		if missing[k] {
			res.NotFound = append(res.NotFound, n)
			continue
		}
		var p ghPR
		_ = json.Unmarshal(resp.Data.Repository[k], &p) // absent or null: a zero PR, which add skips
		add(p)
	}
	return res, nil
}

type ghPR struct {
	Number            int    `json:"number"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOid        string `json:"headRefOid"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	Mergeable         string `json:"mergeable"`
	Commits           struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							Context    string `json:"context"`
							State      string `json:"state"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
				CheckSuites struct {
					Nodes []struct {
						Status      string           `json:"status"`
						WorkflowRun *json.RawMessage `json:"workflowRun"`
					} `json:"nodes"`
				} `json:"checkSuites"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (p ghPR) pr() PR {
	out := PR{
		Number: p.Number, Branch: p.HeadRefName, Head: p.HeadRefOid, Fork: p.IsCrossRepository,
		Open: p.State == "OPEN", Draft: p.IsDraft,
	}
	switch p.Mergeable {
	case "MERGEABLE":
		out.Merge = Mergeable
	case "CONFLICTING":
		out.Merge = Conflicting
	}
	for _, n := range p.Commits.Nodes {
		// A workflow run that has not finished may not have reported its checks yet. Suites
		// without a workflow run are apps that may never report, so they are ignored.
		for _, s := range n.Commit.CheckSuites.Nodes {
			if s.WorkflowRun != nil && string(*s.WorkflowRun) != "null" && s.Status != "COMPLETED" {
				out.Incomplete = true
			}
		}
		if n.Commit.StatusCheckRollup == nil {
			continue
		}
		if n.Commit.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
			out.Incomplete = true
		}
		for _, c := range n.Commit.StatusCheckRollup.Contexts.Nodes {
			if c.Typename == "StatusContext" {
				out.Checks = append(out.Checks, Check{Name: c.Context, Outcome: statusOutcome(c.State)})
			} else {
				out.Checks = append(out.Checks, Check{Name: c.Name, Outcome: checkRunOutcome(c.Status, c.Conclusion)})
			}
		}
	}
	return out
}

func checkRunOutcome(status, conclusion string) Outcome {
	if status != "COMPLETED" {
		return Unfinished
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return Succeeded
	case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return Failed
	case "CANCELLED", "STALE":
		return Cancelled
	}
	return Failed // finished with a conclusion Agora does not know
}

func statusOutcome(state string) Outcome {
	switch state {
	case "SUCCESS":
		return Succeeded
	case "FAILURE", "ERROR":
		return Failed
	}
	return Unfinished // PENDING, EXPECTED
}

// runGH runs gh, killing it if ctx ends.
func runGH(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed binary; arguments are built by this package, not a shell
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}
