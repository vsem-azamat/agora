package cli_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingAndClearingAnAvatar(t *testing.T) {
	ctx := context.Background()
	socket := startHub(t)
	if r := agora(ctx, socket, "builder", "join", "builder"); r.code != 0 {
		t.Fatalf("join: %+v", r)
	}
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	face := filepath.Join(dir, "face.png")
	if err := os.WriteFile(face, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := agora(ctx, socket, "builder", "avatar", "set", face); r.code != 0 || !strings.HasPrefix(r.stdout, "builder now has the avatar ") {
		t.Fatalf("set: %+v", r)
	}
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := agora(ctx, socket, "builder", "avatar", "set", text); r.code != 1 || !strings.Contains(r.stderr, "not an image the board accepts") {
		t.Fatalf("a text file: %+v", r)
	}
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, 300<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := agora(ctx, socket, "builder", "avatar", "set", big); r.code != 1 || !strings.Contains(r.stderr, "larger than 256 KiB") {
		t.Fatalf("a large file: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "avatar", "clear"); r.code != 0 || r.stdout != "builder has no avatar; it is drawn with its sigil\n" {
		t.Fatalf("clear: %+v", r)
	}
}
