package rooms_test

import (
	"fmt"
	"testing"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

// largeBoard returns Rooms over 100k messages from builder in 10 rooms. subscribe runs before the
// messages are posted, so positions set there are left behind them.
func largeBoard(b *testing.B, subscribe func(*rooms.Rooms)) *rooms.Rooms {
	b.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	r := rooms.New(db, nil)
	s := sessions.New(db, queue.New(db, nil), r, nil)
	for _, name := range []string{"builder", "reader"} {
		if _, err := s.Join(ctx, name, "", false); err != nil {
			b.Fatal(err)
		}
	}
	for i := range 10 {
		if _, err := db.Exec(`INSERT INTO rooms (name, purpose, created_by, created_at) VALUES (?, 'x', 'builder', 0)`, fmt.Sprintf("room-%d", i)); err != nil {
			b.Fatal(err)
		}
	}
	subscribe(r)
	tx, _ := db.Begin()
	for i := range 100_000 {
		if _, err := tx.Exec(`INSERT INTO messages (room, author, body, at) VALUES (?, 'builder', 'hello', 0)`, fmt.Sprintf("room-%d", i%10)); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return r
}

func benchUnread(b *testing.B, r *rooms.Rooms) {
	b.Helper()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := r.Unread(ctx, "reader", rooms.Everything, 5); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUnreadOnALargeBoard measures Unread with 100k messages in 10 rooms when nothing is
// unread, which is what every hook pays.
func BenchmarkUnreadOnALargeBoard(b *testing.B) {
	r := largeBoard(b, func(*rooms.Rooms) {})
	if _, err := r.Subscribe(ctx, "reader", []string{"room-1", "room-2"}, true, ""); err != nil {
		b.Fatal(err)
	}
	benchUnread(b, r)
}

// BenchmarkUnreadWithStaleMentionsRooms measures Unread when two rooms were followed with
// mentions before 100k messages arrived: their chatter is never read, so their reading
// positions stay behind all of it.
func BenchmarkUnreadWithStaleMentionsRooms(b *testing.B) {
	r := largeBoard(b, func(r *rooms.Rooms) {
		if _, err := r.Subscribe(ctx, "reader", []string{"room-1", "room-2"}, true, rooms.ModeMentions); err != nil {
			b.Fatal(err)
		}
	})
	benchUnread(b, r)
}
