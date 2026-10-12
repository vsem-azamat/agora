package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// avatarHub is a hub, not serving, with the bridge example-chat, whose log goes to log.
func avatarHub(t *testing.T) (*Hub, *bytes.Buffer) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var log bytes.Buffer
	h := Open(db, nil, slog.New(slog.NewTextHandler(&log, nil)))
	if _, err := h.sessions.Join(ctx, "builder", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.bridges.Add(ctx, "builder", "example-chat", "true", "", nil); err != nil {
		t.Fatal(err)
	}
	return h, &log
}

func jpegBase64(t *testing.T, shade uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (h *Hub) line(t *testing.T, format string, a ...any) {
	t.Helper()
	h.bridgeLine(context.Background(), "example-chat", []byte(fmt.Sprintf(format, a...)))
}

func (h *Hub) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTheSameRevIsNotDecodedAgain(t *testing.T) {
	h, log := avatarHub(t)
	h.line(t, `{"type":"avatar","author":"42","mime":"image/jpeg","data":%q,"rev":"p1"}`, jpegBase64(t, 200))
	rev, ok, err := h.bridges.AvatarRev(context.Background(), "example-chat", "42")
	if err != nil || !ok || rev != "p1" {
		t.Fatalf("rev %q %v %v", rev, ok, err)
	}
	log.Reset()
	h.line(t, `{"type":"avatar","author":42,"mime":"image/jpeg","data":"bm90IGFuIGltYWdl","rev":"p1"}`)
	if log.Len() != 0 || h.count(t, `SELECT COUNT(*) FROM outside_avatars WHERE avatar IS NOT NULL`) != 1 {
		t.Fatalf("a repeated rev was looked at:\n%s", log)
	}
}

func TestTooManyAvatarsAtOnce(t *testing.T) {
	h, log := avatarHub(t)
	for i := range 40 {
		h.line(t, `{"type":"avatar","author":"%d","data":%q,"rev":"p1"}`, i, jpegBase64(t, uint8(i)))
	}
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars`); n != avatarsPerMinute {
		t.Fatalf("%d avatars stored, want %d", n, avatarsPerMinute)
	}
	if n := strings.Count(log.String(), "bridge sends more avatars than the hub takes"); n != 1 {
		t.Fatalf("the limit is logged %d times:\n%s", n, log)
	}
}

func TestAvatarsOfTooManyPeople(t *testing.T) {
	h, log := avatarHub(t)
	if _, err := h.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO outside_avatars (room, author, rev, avatar, updated_at) SELECT 'example-chat', 'p' || i, 'r', NULL, 0 FROM n`, bridges.MaxOutsideAvatars); err != nil {
		t.Fatal(err)
	}
	h.line(t, `{"type":"avatar","author":"new","data":%q,"rev":"p1"}`, jpegBase64(t, 1))
	h.line(t, `{"type":"avatar","author":"newer","data":%q,"rev":"p1"}`, jpegBase64(t, 2))
	h.line(t, `{"type":"avatar","author":"p1","data":%q,"rev":"p2"}`, jpegBase64(t, 3))
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars`); n != bridges.MaxOutsideAvatars {
		t.Fatalf("%d people have avatars", n)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars WHERE author = 'p1' AND rev = 'p2' AND avatar IS NOT NULL`); n != 1 {
		t.Fatal("a person who had an avatar could not change it")
	}
	if n := strings.Count(log.String(), "bridge keeps the most avatars it may"); n != 1 {
		t.Fatalf("the limit is logged %d times:\n%s", n, log)
	}
}

func TestAnAvatarThatIsNotAnImage(t *testing.T) {
	h, log := avatarHub(t)
	gif := base64.StdEncoding.EncodeToString([]byte("GIF89a\x01\x00\x01\x00"))
	h.line(t, `{"type":"avatar","author":"42","mime":"image/gif","data":%q,"rev":"p1"}`, gif)
	h.line(t, `{"type":"avatar","author":"43","data":"***","rev":"p1"}`)
	h.line(t, `{"type":"avatar","author":"","data":%q,"rev":"p1"}`, jpegBase64(t, 1))
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars`); n != 0 {
		t.Fatalf("%d avatars stored", n)
	}
	for _, want := range []string{"bridge avatar refused", "not base64", "without an author"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("the log does not say %q:\n%s", want, log)
		}
	}
}

func TestAPersonWithoutAnAvatar(t *testing.T) {
	h, _ := avatarHub(t)
	h.line(t, `{"type":"avatar","author":"42","data":%q,"rev":"p1"}`, jpegBase64(t, 9))
	h.line(t, `{"type":"avatar","author":"42","data":"","rev":"none"}`)
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars WHERE author = '42' AND rev = 'none' AND avatar IS NULL`); n != 1 {
		t.Fatal("the person still has an avatar")
	}
	if n := h.count(t, `SELECT COUNT(*) FROM avatars`); n != 0 {
		t.Fatalf("%d unused avatars stay", n)
	}
}

func TestAnInLineWithoutAvatarRevChangesNothing(t *testing.T) {
	h, _ := avatarHub(t)
	h.line(t, `{"type":"avatar","author":"42","data":%q,"rev":"p1"}`, jpegBase64(t, 5))
	h.line(t, `{"type":"in","id":"1","author":{"id":"42","name":"Ada"},"text":"hi"}`)
	h.line(t, `{"type":"in","id":"2","author":{"id":"42","name":"Ada","avatar_rev":null},"text":"hi"}`)
	msgs, err := h.rooms.History(context.Background(), "example-chat", 10)
	if err != nil || len(msgs) != 2 || msgs[0].ExtAuthorAvatar == "" || msgs[1].ExtAuthorAvatar == "" {
		t.Fatalf("history: %v %+v", err, msgs)
	}
	unread, _, err := h.rooms.Unread(context.Background(), "builder", rooms.Everything, 0)
	if err != nil || len(unread) == 0 || unread[len(unread)-1].ExtAuthorAvatar != msgs[0].ExtAuthorAvatar {
		t.Fatalf("unread: %v %+v", err, unread)
	}
}

func TestAvatarLinesOverTheirLimits(t *testing.T) {
	h, log := avatarHub(t)
	h.line(t, `{"type":"avatar","author":"42","data":%q,"rev":%q}`, jpegBase64(t, 5), strings.Repeat("r", bridges.MaxIdentifier+1))
	h.line(t, `{"type":"avatar","author":"43","data":%q,"rev":"p1"}`, strings.Repeat("A", maxAvatarData+4))
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars`); n != 0 {
		t.Fatalf("%d avatars stored", n)
	}
	for _, want := range []string{"identifiers are at most", "bridge avatar too large"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("the log does not say %q:\n%s", want, log)
		}
	}
}

func TestANewPictureWhileTheBridgeIsFull(t *testing.T) {
	h, _ := avatarHub(t)
	if _, err := h.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO outside_avatars (room, author, rev, avatar, updated_at) SELECT 'example-chat', 'p' || i, 'r', NULL, 0 FROM n`, bridges.MaxOutsideAvatars-1); err != nil {
		t.Fatal(err)
	}
	h.line(t, `{"type":"avatar","author":"42","data":%q,"rev":"p1"}`, jpegBase64(t, 5))
	h.line(t, `{"type":"in","id":"1","author":{"id":"42","name":"Ada","avatar_rev":"p2"},"text":"new photo"}`)
	h.line(t, `{"type":"avatar","author":"43","data":%q,"rev":"p1"}`, jpegBase64(t, 7)) // someone new: the bridge is still full
	h.line(t, `{"type":"avatar","author":"42","data":%q,"rev":"p2"}`, jpegBase64(t, 6))
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars WHERE author = '42' AND rev = 'p2' AND avatar IS NOT NULL`); n != 1 {
		t.Fatal("the person lost the avatar while the bridge is full")
	}
	if n := h.count(t, `SELECT COUNT(*) FROM outside_avatars WHERE author = '43'`); n != 0 {
		t.Fatal("someone new took the place of a person whose picture changed")
	}
}
