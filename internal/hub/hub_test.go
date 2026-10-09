package hub_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-hub-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

type running struct {
	client agorav1connect.ResourceServiceClient
	clock  *clock
	stop   context.CancelFunc
	done   chan struct{} // closed when Serve returns
	err    error         // what Serve returned; read after done
}

func start(t *testing.T) *running {
	t.Helper()
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now()}
	socket := filepath.Join(dir, "hub.sock")
	l, err := hub.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	r := &running{clock: c, stop: cancel, done: make(chan struct{})}
	go func() {
		r.err = hub.Open(db, c.now, nil).Serve(ctx, l)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
		db.Close()
	})
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	r.client = agorav1connect.NewResourceServiceClient(&http.Client{Transport: transport}, "http://agora")
	return r
}

func join(t *testing.T, r *running, key, agent string, lease time.Duration) {
	t.Helper()
	if _, err := r.client.Join(context.Background(), connect.NewRequest(&agorav1.JoinRequest{Key: key, Agent: agent, Lease: durationpb.New(lease)})); err != nil {
		t.Fatal(err)
	}
}

// waitFor starts a wait stream and returns a channel that receives its error when it ends.
func waitFor(t *testing.T, r *running, key, agent string) <-chan error {
	t.Helper()
	ended := make(chan error, 1)
	stream, err := r.client.Wait(context.Background(), connect.NewRequest(&agorav1.WaitRequest{Key: key, Agent: agent}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() { // the first message: the current position
		t.Fatalf("no first message: %v", stream.Err())
	}
	go func() {
		defer stream.Close()
		for stream.Receive() {
		}
		ended <- stream.Err()
	}()
	return ended
}

func TestWaiterWakesWhenAListingOffersItTheSlot(t *testing.T) {
	r := start(t)
	join(t, r, "r", "a", time.Minute)
	join(t, r, "r", "b", time.Minute)
	ended := waitFor(t, r, "r", "b")
	r.clock.add(time.Minute) // a's lease ends; nothing has settled it yet
	if _, err := r.client.List(context.Background(), connect.NewRequest(&agorav1.ListRequest{})); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond): // well before the one-second safety net
		t.Fatal("waiter was not woken by the listing")
	}
}

func TestShutdownEndsWaitsAndReturns(t *testing.T) {
	r := start(t)
	join(t, r, "r", "a", time.Minute)
	join(t, r, "r", "b", time.Minute)
	ended := waitFor(t, r, "r", "b")
	r.stop()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatal(r.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("hub did not stop")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("wait stream did not end")
	}
}

func TestListenNeverRemovesAFileThatIsNotASocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "notes.txt")
	os.WriteFile(path, []byte("keep me"), 0o600)
	if _, err := hub.Listen(path); err == nil {
		t.Fatal("listened on a regular file")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Fatalf("file changed: %q %v", b, err)
	}
}

func TestSecondHubOnTheSameSocketIsRefused(t *testing.T) {
	path := filepath.Join(shortDir(t), "hub.sock")
	l, err := hub.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := hub.Listen(path); err == nil {
		t.Fatal("second hub was allowed")
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	path := filepath.Join(shortDir(t), "hub.sock")
	// a hub that crashed leaves its socket file behind, with nobody listening and no lock held
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	old.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	l, err := hub.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestLongSocketPathIsExplained(t *testing.T) {
	long := filepath.Join(shortDir(t), strings.Repeat("x", hub.MaxSocketPath))
	if _, err := hub.Listen(long); err == nil || !strings.Contains(err.Error(), "unix sockets allow at most") {
		t.Fatalf("err = %v", err)
	}
}
