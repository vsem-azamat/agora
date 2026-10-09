// Package install adds Agora's integrations to the machine and removes them: the Claude Code
// hooks, the hub's systemd user unit and the agent skill. Every function is idempotent and
// writes files atomically.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Outcome says what an install or uninstall did.
type Outcome int

const (
	// Unchanged: the target was already installed as asked; nothing was written.
	Unchanged Outcome = iota
	// Created: the target was not there and was written.
	Created
	// Updated: the target was there in another form and was rewritten.
	Updated
	// Removed: the target was there and was removed.
	Removed
	// Absent: the target was not installed; nothing was written.
	Absent
)

// Change is the result of changing a file that keeps a backup.
type Change struct {
	Outcome Outcome
	Backup  string   // path of the copy made before the change, or ""
	Foreign []string // commands that look like Agora's hooks but were not written by it; left alone
}

// resolve follows symbolic links at path, also dangling ones, to the file they point to, so
// writes update the linked file instead of replacing the link. A path that is not a link
// resolves to itself.
func resolve(path string) (string, error) {
	for range 40 {
		fi, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", path)
}

// readIfExists returns the file's content and mode, with exists false when there is no file.
func readIfExists(path string) (content []byte, mode fs.FileMode, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0, false, err
	}
	return b, fi.Mode().Perm(), true, nil
}

// writeAtomic replaces path with content through a temporary file in the same directory, so
// readers see the old or the new file and never a partial one.
func writeAtomic(path string, content []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// backup copies content next to path as <path>.agora-backup-<YYYYMMDD-HHMMSS>, adding a
// counter when a backup of the same second exists, and returns the copy's path.
func backup(path string, content []byte, mode fs.FileMode, now time.Time) (string, error) {
	base := path + ".agora-backup-" + now.Local().Format("20060102-150405")
	name := base
	for i := 2; ; i++ {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			name = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(content); err != nil {
			f.Close()
			return "", err
		}
		return name, f.Close()
	}
}

// writeIfChanged writes content to path unless it already holds exactly that.
func writeIfChanged(path string, content []byte) (Outcome, error) {
	path, err := resolve(path)
	if err != nil {
		return 0, err
	}
	old, mode, exists, err := readIfExists(path)
	if err != nil {
		return 0, err
	}
	if exists && bytes.Equal(old, content) {
		return Unchanged, nil
	}
	if !exists {
		mode = 0o644
	}
	if err := writeAtomic(path, content, mode); err != nil {
		return 0, err
	}
	if exists {
		return Updated, nil
	}
	return Created, nil
}

// removeIfExists removes path and reports whether it was there.
func removeIfExists(path string) (Outcome, error) {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, nil
	}
	if err != nil {
		return 0, err
	}
	return Removed, nil
}
