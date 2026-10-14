package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWithoutTheAppThePageSaysHowToGetIt(t *testing.T) {
	h := HandlerOf(fstest.MapFS{".keep": {}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusServiceUnavailable || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"not built into this binary", "releases", "make build-web"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("page lacks %q: %s", want, body)
		}
	}
	for _, p := range []string{"/index.html", "/assets/index.js", "/.keep"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	if built(fstest.MapFS{".keep": {}}) || !built(fstest.MapFS{"index.html": {Data: []byte("<html>")}}) {
		t.Fatal("built() does not look for index.html")
	}
}
