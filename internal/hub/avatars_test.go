package hub_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
)

// photo is a w×w opaque JPEG.
func photo(t *testing.T, w int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, w))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fetched is an answer of the web listener, read to its end.
type fetched struct {
	status int
	header http.Header
	body   []byte
}

// fetch gets path from the web listener with token, or without one when token is empty.
func (w *webHub) fetch(t *testing.T, token, path string) fetched {
	t.Helper()
	resp, err := w.client(token).Get(w.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return fetched{resp.StatusCode, resp.Header, body}
}

func TestAPersonsAvatar(t *testing.T) {
	b := startBridgeHub(t, time.Hour)
	f := newFake(t)
	b.add(t, f)
	f.send(`{"type":"avatar","author":"42","mime":"image/jpeg","data":"`+base64.StdEncoding.EncodeToString(photo(t, 300))+`","rev":"p1"}`,
		`{"type":"in","id":"1","author":{"id":"42","name":"Ada","avatar_rev":"p1"},"text":"hello"}`)
	eventually(t, "the message", func() bool { return len(b.history(t)) == 1 })
	hash := b.history(t)[0].GetExternalAuthor().GetAvatar()
	if len(hash) != 64 {
		t.Fatalf("the author outside carries the avatar %q", hash)
	}
	token := b.token(t, false)
	got := b.fetch(t, token, "/avatars/"+hash+"?size=128")
	if got.status != http.StatusOK || got.header.Get("Content-Type") != "image/jpeg" ||
		got.header.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'" ||
		got.header.Get("X-Content-Type-Options") != "nosniff" || got.header.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("avatar: %d %v", got.status, got.header)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(got.body)); err != nil || cfg.Width != 128 {
		t.Fatalf("small avatar: %v %v", cfg, err)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(b.fetch(t, token, "/avatars/"+hash).body)); err != nil || cfg.Width != 256 {
		t.Fatalf("large avatar: %v %v", cfg, err)
	}
	if got := b.fetch(t, "", "/avatars/"+hash); got.status != http.StatusUnauthorized || bytes.Contains(got.body, []byte("JFIF")) {
		t.Fatalf("without the token: %d", got.status)
	}
	for _, path := range []string{"/avatars/" + string(bytes.Repeat([]byte("0"), 64)), "/avatars/../index.html", "/avatars/" + hash + "x"} {
		if got := b.fetch(t, token, path); got.status != http.StatusNotFound {
			t.Fatalf("%s: %d", path, got.status)
		}
	}

	// a new picture announced on an in line: no avatar until it comes
	f.send(`{"type":"in","id":"2","author":{"id":"42","name":"Ada","avatar_rev":"p2"},"text":"new photo"}`)
	eventually(t, "the second message", func() bool { return len(b.history(t)) == 2 })
	for _, m := range b.history(t) {
		if m.GetExternalAuthor().GetAvatar() != "" {
			t.Fatalf("message %d still carries the old avatar", m.GetId())
		}
	}
	if got := b.fetch(t, token, "/avatars/"+hash); got.status != http.StatusNotFound {
		t.Fatalf("the unused avatar is still served: %d", got.status)
	}
}

func TestAgentsSetAndClearTheirAvatar(t *testing.T) {
	w := startWeb(t, "owner")
	ctx := context.Background()
	if _, err := w.sessions.JoinName(ctx, connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	set, err := w.agents.SetAvatar(ctx, connect.NewRequest(&agorav1.SetAvatarRequest{Agent: "builder", Image: photo(t, 64)}))
	if err != nil || len(set.Msg.GetProfile().GetAvatar()) != 64 {
		t.Fatalf("set: %v %v", set, err)
	}
	if _, err := w.agents.SetAvatar(ctx, connect.NewRequest(&agorav1.SetAvatarRequest{Agent: "builder", Image: []byte("hello")})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a text file: %v", err)
	}
	cleared, err := w.agents.ClearAvatar(ctx, connect.NewRequest(&agorav1.ClearAvatarRequest{Agent: "builder"}))
	if err != nil || cleared.Msg.GetProfile().GetAvatar() != "" {
		t.Fatalf("clear: %v %v", cleared, err)
	}
	if _, err := w.agents.SetAvatar(ctx, connect.NewRequest(&agorav1.SetAvatarRequest{Agent: "ghost", Image: photo(t, 8)})); code(err) != connect.CodeNotFound {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestTheOperatorSetsItsAvatarFromTheApp(t *testing.T) {
	w := startWeb(t, "owner")
	ctx := context.Background()
	if _, err := w.sessions.JoinName(ctx, connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	app := agorav1connect.NewAgentServiceClient(w.client(w.token(t, false)), w.base)
	set, err := app.SetAvatar(ctx, connect.NewRequest(&agorav1.SetAvatarRequest{Agent: "builder", Image: photo(t, 64), Mime: "image/jpeg"}))
	if err != nil || set.Msg.GetProfile().GetName() != "owner" || set.Msg.GetProfile().GetAvatar() == "" {
		t.Fatalf("set from the app: %v %v", set, err)
	}
	list, err := w.agents.ListAgents(ctx, connect.NewRequest(&agorav1.ListAgentsRequest{All: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Msg.GetAgents() {
		if p.GetName() == "builder" && p.GetAvatar() != "" {
			t.Fatal("builder's avatar changed")
		}
		if p.GetName() == "owner" && p.GetActive() {
			t.Fatal("setting an avatar made the operator active on the board")
		}
	}
	if _, err := app.ClearAvatar(ctx, connect.NewRequest(&agorav1.ClearAvatarRequest{})); err != nil {
		t.Fatal(err)
	}
}
