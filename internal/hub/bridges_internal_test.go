package hub

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestStderrLinesAreLimited(t *testing.T) {
	l := logLimit{max: 3, window: time.Minute}
	start := time.Now()
	let := 0
	for i := range 10 {
		ok, dropped := l.allow(start.Add(time.Duration(i) * time.Second))
		if ok {
			let++
		}
		if dropped != 0 {
			t.Fatalf("line %d: %d dropped within the window", i, dropped)
		}
	}
	if let != 3 {
		t.Fatalf("%d lines let through, want 3", let)
	}
	ok, dropped := l.allow(start.Add(time.Minute))
	if !ok || dropped != 7 {
		t.Fatalf("next window: %v, %d left out", ok, dropped)
	}
}

func TestStderrLinesAreLoggedAsInfo(t *testing.T) {
	var out bytes.Buffer
	h := &Hub{log: slog.New(slog.NewTextHandler(&out, nil))}
	h.logStderr("example-chat", strings.NewReader("tg-bridge INFO hello\n"+strings.Repeat("x", maxLogLine+1)+"\n"))
	got := out.String()
	if !strings.Contains(got, `level=INFO msg="bridge stderr" bridge=example-chat line="tg-bridge INFO hello"`) {
		t.Fatalf("a stderr line is not logged as info:\n%s", got)
	}
	if !strings.Contains(got, `level=WARN msg="bridge stderr line too long; skipped"`) {
		t.Fatalf("a line too long is not a warning:\n%s", got)
	}
}
