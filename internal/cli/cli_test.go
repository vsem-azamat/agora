package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/cli"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/store"
)

// startHub runs a hub on a temporary socket and returns the socket path.
func startHub(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-test-") // short path: unix sockets have a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "hub.sock")
	l, err := hub.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		hub.New(queue.New(db, nil), nil).Serve(ctx, l)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		db.Close()
	})
	return socket
}

type result struct {
	code           int
	stdout, stderr string
}

func agora(ctx context.Context, socket, agent string, args ...string) result {
	var out, errOut bytes.Buffer
	all := append([]string{"--socket", socket, "--as", agent}, args...)
	code := cli.Run(ctx, all, &out, &errOut)
	return result{code, out.String(), errOut.String()}
}

func TestLockIsRefusedWithExitCodeTwo(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	if r := agora(ctx, socket, "a", "lock", "example-app/merge", "--ttl", "10m", "merging", "#57"); r.code != 0 || !strings.Contains(r.stdout, "locked example-app/merge") {
		t.Fatalf("first lock: %+v", r)
	}
	r := agora(ctx, socket, "b", "lock", "example-app/merge")
	if r.code != cli.ExitRefused || !strings.Contains(r.stderr, "held by a until") || !strings.Contains(r.stderr, "merging #57") {
		t.Fatalf("second lock: %+v", r)
	}
	if r := agora(ctx, socket, "b", "queue", "ls", "example-app/merge"); strings.Contains(r.stdout, " b ") {
		t.Fatalf("b was queued: %s", r.stdout)
	}
}

func TestUnlockOthersNeedsForce(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	agora(ctx, socket, "a", "lock", "db/shared")
	if r := agora(ctx, socket, "b", "unlock", "db/shared"); !strings.Contains(r.stdout, "b was not queued") {
		t.Fatalf("unlock without force: %+v", r)
	}
	if r := agora(ctx, socket, "b", "unlock", "db/shared", "--force"); r.code != 0 || !strings.Contains(r.stdout, "removed a from db/shared") {
		t.Fatalf("forced unlock: %+v", r)
	}
	if r := agora(ctx, socket, "c", "locks"); strings.Contains(r.stdout, "db/shared") {
		t.Fatalf("lock still held: %s", r.stdout)
	}
}

func TestWaitEndsWhenTheTurnComes(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agora(ctx, socket, "a", "queue", "join", "heavy/typecheck")
	if r := agora(ctx, socket, "b", "queue", "join", "heavy/typecheck"); !strings.Contains(r.stdout, "position 1") {
		t.Fatalf("join: %+v", r)
	}
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "b", "queue", "wait", "heavy/typecheck") }()
	time.Sleep(200 * time.Millisecond) // let the wait start streaming
	agora(ctx, socket, "a", "queue", "release", "heavy/typecheck")
	select {
	case r := <-waited:
		if r.code != 0 || !strings.Contains(r.stdout, "waiting for heavy/typecheck at position 1") || !strings.Contains(r.stdout, "holding heavy/typecheck") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestWaitEndsWithAnErrorWhenRemoved(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agora(ctx, socket, "a", "queue", "join", "r")
	agora(ctx, socket, "b", "queue", "join", "r")
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "b", "queue", "wait", "r") }()
	time.Sleep(200 * time.Millisecond)
	agora(ctx, socket, "a", "queue", "release", "r", "--agent", "b", "--force")
	select {
	case r := <-waited:
		if r.code == 0 || !strings.Contains(r.stderr, "not in the queue") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestSlotsAndListing(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	if r := agora(ctx, socket, "a", "queue", "slots", "heavy/typecheck", "2"); !strings.Contains(r.stdout, "heavy/typecheck has 2 slots") {
		t.Fatalf("slots: %+v", r)
	}
	for _, a := range []string{"a", "b", "c"} {
		agora(ctx, socket, a, "queue", "join", "heavy/typecheck", fmt.Sprintf("check by %s", a))
	}
	r := agora(ctx, socket, "a", "queue", "ls")
	for _, want := range []string{"heavy/typecheck (2 slots)", "held     a", "held     b", "#1       c"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("listing lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestNoHubIsReported(t *testing.T) {
	r := agora(context.Background(), filepath.Join(t.TempDir(), "none.sock"), "a", "locks")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot reach the hub") {
		t.Fatalf("%+v", r)
	}
}

func TestIdentityIsRequired(t *testing.T) {
	socket := startHub(t)
	r := agora(context.Background(), socket, "", "lock", "r")
	if r.code != 1 || !strings.Contains(r.stderr, "--as") {
		t.Fatalf("%+v", r)
	}
}
