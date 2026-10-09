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

const prFields = `fragment pr on PullRequest {
  number headRefName headRefOid isCrossRepository state isDraft mergeable
  commits(last: 1) { nodes { commit {
    statusCheckRollup { contexts(first: 100) {
      nodes {
        __typename
        ... on CheckRun { name status conclusion }
        ... on StatusContext { context state }
      }
      pageInfo { hasNextPage }
    } }
    checkSuites(first: 50) { nodes { status workflowRun { id } } }
  } } }
}`

// Lookup asks for the default branch, the open pull requests from each branch and each pull
// request by number, in one GraphQL request.
func (g *GitHub) Lookup(ctx context.Context, repo Repo, q Query) (Result, error) {
	owner, name, ok := strings.Cut(repo.Path, "/")
	if !ok || strings.Contains(name, "/") {
		return Result{}, fmt.Errorf("%s is not an owner/name repository", repo.Key())
	}
	vars := []string{"$owner: String!", "$name: String!"}
	fields := []string{"defaultBranchRef { name }"}
	args := []string{"api", "graphql", "--hostname", repo.Host, "-f", "owner=" + owner, "-f", "name=" + name}
	seenBranch := map[string]bool{}
	for _, b := range q.Branches {
		if b == "" || seenBranch[b] {
			continue
		}
		seenBranch[b] = true
		k := "b" + strconv.Itoa(len(seenBranch)-1)
		vars = append(vars, "$"+k+": String!")
		fields = append(fields, k+": pullRequests(headRefName: $"+k+", states: OPEN, first: 5) { nodes { ...pr } }")
		args = append(args, "-f", k+"="+b)
	}
	seenNumber := map[int]bool{}
	for _, n := range q.Numbers {
		if n <= 0 || seenNumber[n] {
			continue
		}
		seenNumber[n] = true
		k := "n" + strconv.Itoa(len(seenNumber)-1)
		vars = append(vars, "$"+k+": Int!")
		fields = append(fields, k+": pullRequest(number: $"+k+") { ...pr }")
		args = append(args, "-F", k+"="+strconv.Itoa(n))
	}
	query := "query(" + strings.Join(vars, ", ") + ") {\n  repository(owner: $owner, name: $name) {\n    " +
		strings.Join(fields, "\n    ") + "\n  }\n}\n" + prFields
	args = append(args, "-f", "query="+query)

	run := g.Run
	if run == nil {
		run = runGH
	}
	out, runErr := run(ctx, args)
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
			if alias, ok := e.Path[1].(string); ok && strings.HasPrefix(alias, "n") {
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
	json.Unmarshal(resp.Data.Repository["defaultBranchRef"], &def)
	res.DefaultBranch = def.Name
	seenPR := map[int]bool{}
	add := func(p ghPR) {
		if p.Number == 0 || seenPR[p.Number] {
			return
		}
		seenPR[p.Number] = true
		res.PRs = append(res.PRs, p.pr())
	}
	for i := range len(seenBranch) {
		var list struct {
			Nodes []ghPR `json:"nodes"`
		}
		json.Unmarshal(resp.Data.Repository["b"+strconv.Itoa(i)], &list)
		for _, p := range list.Nodes {
			add(p)
		}
	}
	numbers := make([]int, 0, len(seenNumber))
	for _, n := range q.Numbers {
		if seenNumber[n] && !slices.Contains(numbers, n) {
			numbers = append(numbers, n)
		}
	}
	for i, n := range numbers {
		k := "n" + strconv.Itoa(i)
		if missing[k] {
			res.NotFound = append(res.NotFound, n)
			continue
		}
		var p ghPR
		json.Unmarshal(resp.Data.Repository[k], &p)
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
	out := PR{Number: p.Number, Branch: p.HeadRefName, Head: p.HeadRefOid, Fork: p.IsCrossRepository,
		Open: p.State == "OPEN", Draft: p.IsDraft}
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
	cmd := exec.CommandContext(ctx, "gh", args...)
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
