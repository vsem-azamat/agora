package hub

import (
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
