package gitinfo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsem-azamat/agora/internal/gitinfo"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBranchOfACheckoutAndItsWorktree(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	os.MkdirAll(filepath.Join(repo, "src", "deep"), 0o755)
	if b := gitinfo.Branch(filepath.Join(repo, "src", "deep")); b != "main" {
		t.Fatalf("branch %q", b)
	}
	wt := filepath.Join(repo, ".worktrees", "login-fix")
	gitdir := filepath.Join(repo, ".git", "worktrees", "login-fix")
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/fix/login-timeout\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	if b := gitinfo.Branch(wt); b != "fix/login-timeout" {
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
	if b := gitinfo.Branch(t.TempDir()); b != "" {
		t.Fatalf("outside: %q", b)
	}
}
