package avatars_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/avatars"
	"github.com/vsem-azamat/agora/internal/store"
)

// webp1x1 is a lossy WebP image of one grey pixel.
const webp1x1 = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"

// gradient draws an opaque w×h picture.
func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func pngOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func refused(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want invalid with %q", err, want)
	}
}

func TestAPhoto(t *testing.T) {
	p, err := avatars.Prepare(jpegOf(t, gradient(600, 400)), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if p.MIME != avatars.JPEG || p.W != 256 || p.H != 256 || len(p.Hash) != 64 {
		t.Fatalf("picture = %s %dx%d %q", p.MIME, p.W, p.H, p.Hash)
	}
}

func TestWebPIsAccepted(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString(webp1x1)
	p, err := avatars.Prepare(data, "image/webp")
	if err != nil {
		t.Fatal(err)
	}
	if p.MIME != avatars.JPEG || p.W != 1 || p.Small != nil {
		t.Fatalf("picture = %s %dx%d", p.MIME, p.W, p.H)
	}
}

func TestTooLarge(t *testing.T) {
	noise := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	_, _ = rand.Read(noise.Pix) // never fails
	_, err := avatars.Prepare(pngOf(t, noise), "")
	refused(t, err, "at most 256 KiB")
	_, err = avatars.Prepare(pngOf(t, image.NewNRGBA(image.Rect(0, 0, 2000, 2000))), "")
	refused(t, err, "at most 1024×1024 pixels")
	_, err = avatars.Prepare([]byte(`<svg xmlns="http://www.w3.org/2000/svg">`+strings.Repeat("<g></g>", 5000)+`</svg>`), "")
	refused(t, err, "at most 32 KiB")
}

func TestNotAnImage(t *testing.T) {
	for _, data := range [][]byte{[]byte("GIF89a\x01\x00\x01\x00"), []byte("hello")} {
		_, err := avatars.Prepare(data, "")
		refused(t, err, avatars.Accepted)
	}
}

func TestADeclaredTypeThatLies(t *testing.T) {
	_, err := avatars.Prepare(jpegOf(t, gradient(10, 10)), "image/png")
	refused(t, err, "the image is image/jpeg, not image/png")
}

func TestMetadataGoes(t *testing.T) {
	plain := jpegOf(t, gradient(300, 300))
	exif := append([]byte("Exif\x00\x00"), "secret-camera"...)
	data := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(exif) + 2)}, exif...)
	data = append(data, plain[2:]...)
	data = append(data, "secret-trailer"...)
	p, err := avatars.Prepare(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p.Data, []byte("secret")) || bytes.Contains(p.Small, []byte("secret")) {
		t.Fatal("the stored picture keeps metadata")
	}
	large, err := jpeg.Decode(bytes.NewReader(p.Data))
	if err != nil || large.Bounds().Dx() != 256 {
		t.Fatalf("large: %v %v", err, large)
	}
	small, err := jpeg.Decode(bytes.NewReader(p.Small))
	if err != nil || small.Bounds().Dx() != 128 || small.Bounds().Dy() != 128 {
		t.Fatalf("small: %v %v", err, small)
	}
}

func TestTransparencyStays(t *testing.T) {
	img := gradient(64, 64)
	img.Set(0, 0, color.NRGBA{})
	p, err := avatars.Prepare(pngOf(t, img), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if p.MIME != avatars.PNG || p.W != 64 || p.Small != nil {
		t.Fatalf("picture = %s %dx%d small %d", p.MIME, p.W, p.H, len(p.Small))
	}
	got, err := png.Decode(bytes.NewReader(p.Data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := got.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("corner alpha = %d", a)
	}
}

func TestADamagedImage(t *testing.T) {
	data := pngOf(t, gradient(100, 100))
	_, err := avatars.Prepare(data[:len(data)/2], "")
	refused(t, err, "cannot be decoded")
}

func TestACleanDrawing(t *testing.T) {
	svg := `<?xml version="1.0"?>
<!-- drawn by hand -->
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10">
  <defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="rgb(0,0,0)"/></linearGradient></defs>
  <circle cx="5" cy="5" r="4" fill="url(#g)" style="stroke: red; stroke-width: 1"/>
  <path d="M1 1L9 9" transform="rotate(45 5 5)"/>
  <linearGradient id="h" xlink:href="#g"/>
  <text x="1" y="9" font-family="serif">&lt;script&gt; &amp; "q"</text>
  <style>circle { fill: url(#g) }</style>
</svg>`
	p, err := avatars.Prepare([]byte(svg), "image/svg+xml")
	if err != nil {
		t.Fatal(err)
	}
	want := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">
  <defs><linearGradient id="g"><stop offset="0" stop-color="#fff"></stop><stop offset="1" stop-color="rgb(0,0,0)"></stop></linearGradient></defs>
  <circle cx="5" cy="5" r="4" fill="url(#g)" style="stroke: red; stroke-width: 1"></circle>
  <path d="M1 1L9 9" transform="rotate(45 5 5)"></path>
  <linearGradient id="h" href="#g"></linearGradient>
  <text x="1" y="9" font-family="serif">&lt;script&gt; &amp; "q"</text>
  <style>circle { fill: url(#g) }</style>
</svg>`
	if p.MIME != avatars.SVG || string(p.Data) != want {
		t.Fatalf("stored %s:\n%s", p.MIME, p.Data)
	}
}

func TestSVGRefusals(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"script", `<script>alert(1)</script>`, "the element script is not allowed"},
		{"handler", `<circle onload="alert(1)"/>`, "the attribute onload on circle is not allowed"},
		{"foreign object", `<foreignObject><div/></foreignObject>`, "the element foreignObject is not allowed"},
		{"outside href", `<linearGradient href="https://example.com/a.svg#x"/>`, "refers outside the image"},
		{"outside xlink", `<linearGradient xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="data:image/svg+xml,x"/>`, "refers outside the image"},
		{"copies", `<defs><g id="a"><rect/></g></defs><use href="#a"/>`, "the element use is not allowed"},
		{"symbol", `<symbol id="s"/>`, "the element symbol is not allowed"},
		{"pattern", `<pattern id="p"/>`, "the element pattern is not allowed"},
		{"mask", `<mask id="a0"><rect/></mask><mask id="a1"><rect mask="url(#a0)"/></mask>`, "the element mask is not allowed"},
		{"clip path", `<clipPath id="a0"><rect/></clipPath>`, "the element clipPath is not allowed"},
		{"masked", `<rect mask="url(#a0)"/>`, "the attribute mask on rect is not allowed"},
		{"clipped", `<rect clip-path="url(#a0)"/>`, "the attribute clip-path on rect is not allowed"},
		{"outside fill", `<rect fill="url(https://example.com/x)"/>`, "refers outside the image"},
		{"outside fill quoted", `<rect fill="URL( 'x.svg#a')"/>`, "refers outside the image"},
		{"import", `<style>@import "x.css";</style>`, "uses @ rules"},
		{"escape", `<rect style="fill: u\72l(x)"/>`, "uses escapes"},
		{"image-set", `<style>rect { fill: image-set("x.png" 1x) }</style>`, "uses image-set()"},
		{"costly style", `<style>text { text-shadow: 0 0 50px red, 0 0 50px blue }</style>`, "the style element uses the property text-shadow"},
		{"costly style attribute", `<text style="fill: red; filter: blur(9px)"/>`, "the attribute style on text uses the property filter"},
		{"hidden in a comment", `<style>text { fill: red; text-shadow/**/: 0 0 9px red }</style>`, "uses the property text-shadow"},
		{"custom property", `<style>rect { --x: 1px; stroke-width: var(--x) }</style>`, "uses the property --x"},
		{"split by an XML comment", `<style>text { text-shadow<!---->: 0 0 50px red }</style>`, "uses the property text-shadow"},
		{"split by CDATA", `<style>text { text-shadow<![CDATA[]]>: 0 0 50px red }</style>`, "uses the property text-shadow"},
		{"comment marks in strings", `<style>text { font-family: "/*"; text-shadow: 0 0 50px red; font-family: "*/" }</style>`, "uses the property text-shadow"},
		{"comment marks in attribute strings", `<text style='font-family: "/*"; text-shadow: 0 0 50px red; font-family: "*/"'/>`, "uses the property text-shadow"},
		{"element in a style", `<style>text { fill: red }<g/></style>`, "the style element holds an element"},
		{"nested", `<style>g { fill: red; &amp; text { text-shadow: 0 0 9px red } }</style>`, "uses the property text-shadow"},
		{"image", `<image href="#x"/>`, "the element image is not allowed"},
		{"animate", `<animate attributeName="href" to="javascript:alert(1)"/>`, "the element animate is not allowed"},
		{"other namespace", `<x:circle xmlns:x="https://example.com/ns"/>`, "the element circle is not SVG"},
		{"twice", `<linearGradient href="#a" xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="#b"/>`, "given twice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := avatars.Prepare([]byte(`<svg xmlns="http://www.w3.org/2000/svg">`+c.body+`</svg>`), "")
			refused(t, err, c.want)
		})
	}
	for _, c := range []struct{ name, svg, want string }{
		{"doctype", `<!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg"/>`, "document type declarations"},
		{"instruction", `<svg xmlns="http://www.w3.org/2000/svg"><?php echo 1 ?></svg>`, "processing instructions"},
		{"not svg", `<html xmlns="http://www.w3.org/1999/xhtml"/>`, "is not SVG"},
		{"html root", `<html/>`, "the element html is not allowed"},
		{"broken", `<svg xmlns="http://www.w3.org/2000/svg"><g></svg>`, "not well-formed"},
		{"two roots", `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`, "more than one root element"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := avatars.Prepare([]byte(c.svg), "")
			refused(t, err, c.want)
		})
	}
}

func open(t *testing.T, names ...string) (*sql.DB, *avatars.Avatars) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = store.InTx(ctx, db, func(tx *sql.Tx) error {
		for _, n := range names {
			if err := agents.RegisterTx(ctx, tx, n, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, avatars.New(db, nil)
}

func stored(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM avatars`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func profileAvatar(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	p, err := agents.New(db, nil).Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p.Avatar
}

func TestTheSamePictureTwice(t *testing.T) {
	ctx := context.Background()
	db, a := open(t, "builder", "reviewer")
	face := pngOf(t, gradient(200, 200))
	p, err := a.SetAgent(ctx, "builder", face, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetAgent(ctx, "reviewer", face, ""); err != nil {
		t.Fatal(err)
	}
	if profileAvatar(t, db, "builder") != p.Hash || profileAvatar(t, db, "reviewer") != p.Hash || stored(t, db) != 1 {
		t.Fatalf("builder %q, reviewer %q, %d stored", profileAvatar(t, db, "builder"), profileAvatar(t, db, "reviewer"), stored(t, db))
	}
}

func TestReplaced(t *testing.T) {
	ctx := context.Background()
	db, a := open(t, "builder")
	old, err := a.SetAgent(ctx, "builder", pngOf(t, gradient(200, 200)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetAgent(ctx, "builder", jpegOf(t, gradient(50, 50)), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Get(ctx, old.Hash, false); !errors.Is(err, avatars.ErrNotFound) || stored(t, db) != 1 {
		t.Fatalf("old avatar: %v, %d stored", err, stored(t, db))
	}
}

func TestSettingAndClearing(t *testing.T) {
	ctx := context.Background()
	db, a := open(t, "builder")
	p, err := a.SetAgent(ctx, "builder", jpegOf(t, gradient(300, 300)), "")
	if err != nil {
		t.Fatal(err)
	}
	if profileAvatar(t, db, "builder") != p.Hash {
		t.Fatalf("avatar = %q", profileAvatar(t, db, "builder"))
	}
	mime, small, err := a.Get(ctx, p.Hash, true)
	if err != nil || mime != avatars.JPEG || !bytes.Equal(small, p.Small) {
		t.Fatalf("small: %v %s", err, mime)
	}
	if _, large, _ := a.Get(ctx, p.Hash, false); !bytes.Equal(large, p.Data) {
		t.Fatal("large differs")
	}
	if err := a.ClearAgent(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	if profileAvatar(t, db, "builder") != "" || stored(t, db) != 0 {
		t.Fatalf("after clearing: %q, %d stored", profileAvatar(t, db, "builder"), stored(t, db))
	}
}

func TestARefusedImageKeepsTheAvatar(t *testing.T) {
	ctx := context.Background()
	db, a := open(t, "builder")
	p, err := a.SetAgent(ctx, "builder", jpegOf(t, gradient(30, 30)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetAgent(ctx, "builder", []byte("hello"), ""); err == nil {
		t.Fatal("a text file was accepted")
	}
	if profileAvatar(t, db, "builder") != p.Hash {
		t.Fatal("the avatar changed")
	}
}

func TestUnknownAgent(t *testing.T) {
	_, a := open(t)
	_, err := a.SetAgent(context.Background(), "ghost", jpegOf(t, gradient(10, 10)), "")
	if !errors.Is(err, agents.ErrUnknown) {
		t.Fatalf("err = %v", err)
	}
}

func TestSVGWithAByteOrderMark(t *testing.T) {
	p, err := avatars.Prepare([]byte("\xef\xbb\xbf<svg><rect width=\"1\"/></svg>"), "image/svg+xml")
	if err != nil || string(p.Data) != `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1"></rect></svg>` {
		t.Fatalf("%v %s", err, p.Data)
	}
}

func TestSVGValuesStayValues(t *testing.T) {
	p, err := avatars.Prepare([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect id='x" onmouseover="location=name'/></svg>`), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := `<rect id="x&quot; onmouseover=&quot;location=name"></rect>`; !strings.Contains(string(p.Data), want) {
		t.Fatalf("stored %s", p.Data)
	}
}

func TestAPictureStillUsedStays(t *testing.T) {
	ctx := context.Background()
	db, a := open(t, "builder", "reviewer")
	shared, err := a.SetAgent(ctx, "builder", pngOf(t, gradient(40, 40)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetAgent(ctx, "reviewer", pngOf(t, gradient(40, 40)), ""); err != nil {
		t.Fatal(err)
	}
	if err := a.ClearAgent(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Get(ctx, shared.Hash, false); err != nil || stored(t, db) != 1 {
		t.Fatalf("the picture reviewer uses: %v, %d stored", err, stored(t, db))
	}
	if _, err := db.Exec(`INSERT INTO rooms (name, purpose, created_by, created_at) VALUES ('example-chat', '', 'builder', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO outside_avatars (room, author, rev, avatar, updated_at) VALUES ('example-chat', '42', 'p1', ?, 0)`, shared.Hash); err != nil {
		t.Fatal(err)
	}
	if err := a.ClearAgent(ctx, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if stored(t, db) != 1 {
		t.Fatal("a picture a person outside uses was deleted")
	}
}

func TestCopiesOfCopies(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"><defs><g id="a0"><rect width="1" height="1"/></g>`)
	for level := 1; level <= 6; level++ {
		b.WriteString(`<g id="a` + string(rune('0'+level)) + `">` + strings.Repeat(`<use href="#a`+string(rune('0'+level-1))+`"/>`, 10) + `</g>`)
	}
	b.WriteString(`</defs><use href="#a6"/></svg>`)
	_, err := avatars.Prepare([]byte(b.String()), "")
	refused(t, err, "the element use is not allowed")
}
