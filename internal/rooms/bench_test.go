package rooms_test

import (
	"fmt"
	"testing"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

// BenchmarkUnreadOnALargeBoard measures Unread with 100k messages in 10 rooms when nothing is
// unread, which is what every hook pays.
func BenchmarkUnreadOnALargeBoard(b *testing.B) {
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	r := rooms.New(db, nil)
	s := sessions.New(db, queue.New(db, nil), r, nil)
	s.Join(ctx, "builder", "", false)
	s.Join(ctx, "reader", "", false)
	tx, _ := db.Begin()
	for i := range 10 {
		tx.Exec(`INSERT INTO rooms (name, purpose, created_by, created_at) VALUES (?, 'x', 'builder', 0)`, fmt.Sprintf("room-%d", i))
	}
	for i := range 100_000 {
		tx.Exec(`INSERT INTO messages (room, author, body, at) VALUES (?, 'builder', 'hello', 0)`, fmt.Sprintf("room-%d", i%10))
	}
	tx.Commit()
	r.Subscribe(ctx, "reader", []string{"room-1", "room-2"}, true)
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := r.Unread(ctx, "reader", false, 5); err != nil {
			b.Fatal(err)
		}
	}
}
