package gitinfo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsem-azamat/agora/internal/gitinfo"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBranchOfACheckoutAndItsWorktree(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(filepath.Join(repo, "src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if b := gitinfo.Read(filepath.Join(repo, "src", "deep")).Branch; b != "main" {
		t.Fatalf("branch %q", b)
	}
	wt := filepath.Join(repo, ".worktrees", "login-fix")
	gitdir := filepath.Join(repo, ".git", "worktrees", "login-fix")
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/fix/login-timeout\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	if b := gitinfo.Read(wt).Branch; b != "fix/login-timeout" {
		t.Fatalf("worktree branch %q", b)
	}
}

func TestDetachedAndOutside(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	h := gitinfo.Read(repo)
	if h.Branch != "0123456789ab" || !h.Detached || h.GitDir == "" {
		t.Fatalf("head %+v", h)
	}
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/deadbeefcafe\n")
	if h := gitinfo.Read(repo); h.Detached || h.Branch != "deadbeefcafe" {
		t.Fatalf("hex-named branch taken for a commit: %+v", h)
	}
	if b := gitinfo.Read(t.TempDir()).Branch; b != "" {
		t.Fatalf("outside: %q", b)
	}
}

func TestRepositoryName(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "example-app")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n := gitinfo.Repo(filepath.Join(repo, "src")); n != "example-app" {
		t.Fatalf("repo %q", n)
	}
	wt := filepath.Join(t.TempDir(), "login-fix")
	gitdir := filepath.Join(repo, ".git", "worktrees", "login-fix")
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/fix/login-timeout\n")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	if n := gitinfo.Repo(wt); n != "example-app" {
		t.Fatalf("worktree repo %q", n)
	}
	if n := gitinfo.Repo(t.TempDir()); n != "" {
		t.Fatalf("outside: %q", n)
	}
}

func TestRepositoryNameOfABareRepositoryWorktree(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "example-app.git")
	gitdir := filepath.Join(bare, "worktrees", "main")
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	wt := filepath.Join(t.TempDir(), "main")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	if n := gitinfo.Repo(wt); n != "example-app" {
		t.Fatalf("repo %q", n)
	}
}

func TestWorktreesShareTheRepositoryAndItsOrigin(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(repo, ".git", "config"), `[core]
	bare = false
[remote "upstream"]
	url = https://github.com/example-org/other.git
[remote "origin"]
	url = git@github.com:example-org/example-app.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
	remote = origin
`)
	wt := filepath.Join(repo, ".worktrees", "login-fix")
	gitdir := filepath.Join(repo, ".git", "worktrees", "login-fix")
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/fix/login-timeout\n")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")

	main, other := gitinfo.Read(repo), gitinfo.Read(wt)
	if main.CommonDir == "" || main.CommonDir != other.CommonDir || main.CommonDir != filepath.Join(repo, ".git") {
		t.Fatalf("common dirs %q and %q", main.CommonDir, other.CommonDir)
	}
	if o := gitinfo.Origin(other.CommonDir); o != "git@github.com:example-org/example-app.git" {
		t.Fatalf("origin %q", o)
	}
	if o := gitinfo.Origin(t.TempDir()); o != "" {
		t.Fatalf("origin without a config: %q", o)
	}
}
