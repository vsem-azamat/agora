// Package gitinfo reads the branch and repository of a git checkout from its files, without
// running git.
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// Head describes what is checked out in the checkout that contains dir.
type Head struct {
	GitDir    string // the checkout's git directory; "" outside any checkout
	CommonDir string // the git directory shared by every worktree of the repository
	Branch    string // the branch, or the first 12 characters of the commit when detached
	Detached  bool
}

// Read returns the head of the checkout that contains dir.
func Read(dir string) Head {
	gitDir := find(dir)
	if gitDir == "" {
		return Head{}
	}
	out := Head{GitDir: gitDir, CommonDir: commonDir(gitDir)}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return out
	}
	h := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(h, "ref: "); ok {
		out.Branch = strings.TrimPrefix(ref, "refs/heads/")
		return out
	}
	if len(h) > 12 {
		h = h[:12]
	}
	out.Branch, out.Detached = h, true
	return out
}

// commonDir returns the git directory shared by all worktrees: the one a worktree's
// `commondir` file names, or gitDir itself.
func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	dir := strings.TrimSpace(string(b))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitDir, dir)
	}
	return filepath.Clean(dir)
}

// Origin returns the URL of the `origin` remote from the config in a repository's common git
// directory, or "" when there is none.
func Origin(commonDir string) string {
	b, err := os.ReadFile(filepath.Join(commonDir, "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.ReplaceAll(line, " ", "") == `[remote"origin"]`
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if inOrigin && ok && strings.TrimSpace(key) == "url" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Repo returns the name of the repository that contains dir: the directory of its main
// checkout, also from a linked worktree, or, for a worktree of a bare repository, the bare
// repository's name without `.git`. It returns "" outside any checkout.
func Repo(dir string) string {
	gitDir := find(dir)
	if gitDir == "" {
		return ""
	}
	gitDir = commonDir(gitDir)
	if filepath.Base(gitDir) == ".git" {
		gitDir = filepath.Dir(gitDir)
	}
	name := strings.TrimSuffix(filepath.Base(gitDir), ".git")
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
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
