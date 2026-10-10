package webtoken_test

import (
	"context"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
	"github.com/vsem-azamat/agora/internal/webtoken"
)

func TestEnsureCreatesKeepsAndRotates(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	at := time.UnixMilli(1_700_000_000_000)
	tokens := webtoken.New(db, func() time.Time { return at })
	if got, err := tokens.Get(ctx); err != nil || got != "" {
		t.Fatalf("before: %q %v", got, err)
	}
	first, err := tokens.Ensure(ctx, false)
	if err != nil || len(first) != 43 {
		t.Fatalf("created %q %v", first, err)
	}
	if again, err := tokens.Ensure(ctx, false); err != nil || again != first {
		t.Fatalf("kept %q %v", again, err)
	}
	var created int64
	if err := db.QueryRowContext(ctx, `SELECT created_at FROM web_token`).Scan(&created); err != nil || created != at.UnixMilli() {
		t.Fatalf("created at %d %v: the injected clock is not used", created, err)
	}
	rotated, err := tokens.Ensure(ctx, true)
	if err != nil || rotated == first {
		t.Fatalf("rotated %q %v", rotated, err)
	}
	if got, err := tokens.Get(ctx); err != nil || got != rotated {
		t.Fatalf("after: %q %v", got, err)
	}
}
