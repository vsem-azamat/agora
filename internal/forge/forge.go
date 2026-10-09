// Package forge asks code hosts (forges) about pull requests and their checks. Each forge is
// chosen by the host of a repository's origin remote.
package forge

import (
	"context"
	"net/url"
	"strings"
)

// Repo is a repository on a forge.
type Repo struct {
	Host string // lowercase, without port: github.com
	Path string // without .git: example-org/example-app
}

// Key identifies the repository: host and path.
func (r Repo) Key() string { return r.Host + "/" + r.Path }

// RepoFromKey is the inverse of Key.
func RepoFromKey(key string) Repo {
	host, path, _ := strings.Cut(key, "/")
	return Repo{Host: host, Path: path}
}

// ParseRemote reads a git remote URL (scp-like `git@host:path`, ssh://, https:// or http://)
// and reports false for local paths, file:// URLs and anything without a host and path.
func ParseRemote(remote string) (Repo, bool) {
	remote = strings.TrimSpace(remote)
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || (u.Scheme != "ssh" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "git") {
			return Repo{}, false
		}
		host, path = u.Hostname(), u.Path
	} else {
		h, p, ok := strings.Cut(remote, ":")
		if !ok || strings.Contains(h, "/") {
			return Repo{}, false // a local path
		}
		if i := strings.LastIndex(h, "@"); i >= 0 {
			h = h[i+1:]
		}
		host, path = h, p
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	host = strings.ToLower(host)
	if host == "" || path == "" {
		return Repo{}, false
	}
	return Repo{Host: host, Path: path}, true
}

// Query asks for the open pull requests from some branches and for some pull requests by number.
type Query struct {
	Branches []string
	Numbers  []int
}

// Result is what a forge knows about a repository.
type Result struct {
	DefaultBranch string
	// PRs holds the open pull requests from the queried branches and the queried numbers that
	// exist, in any state.
	PRs []PR
}

// PR is a pull request.
type PR struct {
	Number    int
	Branch    string // head branch
	Head      string // head commit
	Fork      bool   // the head branch is in another repository
	Open      bool
	Draft     bool
	Conflicts bool // the forge reports a conflict with the base branch
	Checks    []Check
}

// Check is one check on the head commit.
type Check struct {
	Name    string
	Outcome Outcome
}

// Outcome is how a check ended, in forge-neutral terms.
type Outcome int

const (
	// Unfinished checks are queued, running or waiting.
	Unfinished Outcome = iota
	// Succeeded includes skipped and neutral checks.
	Succeeded
	// Failed includes errors, timeouts, startup failures and checks that require action.
	Failed
	// Cancelled includes stale checks: neither failed nor unfinished.
	Cancelled
)

// Forge looks up pull requests of a repository with one request.
type Forge interface {
	Lookup(ctx context.Context, repo Repo, q Query) (Result, error)
}

// Forges maps a host to its forge.
type Forges map[string]Forge
