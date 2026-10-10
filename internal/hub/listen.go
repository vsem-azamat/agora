package hub

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// maxSocketPath is the longest unix socket path that works on every supported system.
const maxSocketPath = 104

// Listen opens the hub's unix socket at path, readable and writable by the owner only. An
// exclusive lock on path+".lock" keeps a second hub away, so a socket left by a hub that no
// longer runs can be replaced safely; a path that is not a socket is never removed.
func Listen(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("socket path is %d bytes; unix sockets allow at most %d: %s", len(path), maxSocketPath, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("a hub is already running on %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			lock.Close()
			return nil, fmt.Errorf("%s exists and is not a socket; choose another --socket", path)
		}
		if err := os.Remove(path); err != nil { // a socket nobody serves: we hold the lock
			lock.Close()
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		lock.Close()
		return nil, err
	}
	return &lockedListener{Listener: l, lock: lock}, nil
}

// lockedListener releases the hub lock when the listener closes.
type lockedListener struct {
	net.Listener
	lock *os.File
	once sync.Once
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { l.lock.Close() })
	return err
}
