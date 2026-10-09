// Package gitinfo reads the branch of a git checkout from its files, without running git.
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// Head describes what is checked out in the checkout that contains dir.
type Head struct {
	GitDir   string // the checkout's git directory; "" outside any checkout
	Branch   string // the branch, or the first 12 characters of the commit when detached
	Detached bool
}

// Read returns the head of the checkout that contains dir.
func Read(dir string) Head {
	gitDir := find(dir)
	if gitDir == "" {
		return Head{}
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return Head{GitDir: gitDir}
	}
	h := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(h, "ref: "); ok {
		return Head{GitDir: gitDir, Branch: strings.TrimPrefix(ref, "refs/heads/")}
	}
	if len(h) > 12 {
		h = h[:12]
	}
	return Head{GitDir: gitDir, Branch: h, Detached: true}
}

// Branch returns the branch checked out in the checkout that contains dir, the first 12
// characters of the commit when detached, or "" outside any checkout.
func Branch(dir string) string { return Read(dir).Branch }

// find returns the git directory of the checkout that contains dir: a .git directory, or the
// directory a worktree's .git file points to.
func find(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil || dir == "" {
		return ""
	}
	for {
		dotgit := filepath.Join(d, ".git")
		if info, err := os.Stat(dotgit); err == nil {
			if info.IsDir() {
				return dotgit
			}
			b, err := os.ReadFile(dotgit)
			if err != nil {
				return ""
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return ""
			}
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(d, target)
			}
			return target
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
