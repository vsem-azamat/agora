// Package gitinfo reads the branch of a git checkout from its files, without running git.
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// Branch returns the branch checked out in the checkout that contains dir: the branch name, the
// first 12 characters of the commit when the checkout is detached, or "" outside any checkout.
func Branch(dir string) string {
	gitDir := find(dir)
	if gitDir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(h, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(h) > 12 {
		h = h[:12]
	}
	return h
}

// Detached reports whether branch is a commit (as Branch returns for a detached checkout).
func Detached(branch string) bool {
	if len(branch) != 12 {
		return false
	}
	for _, c := range branch {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

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
