// Package web holds the web app's built files, embedded in the binary, and serves them.
//
// The files in dist/ are built from web/ by `make build-web` before the Go build; git tracks only
// dist/.keep, so a binary built without them (go build, go install) embeds no app and says so
// on its page.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// files returns the built app.
func files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time
	}
	return sub
}

// Built reports whether the web app is built into this binary.
func Built() bool { return built(files()) }

func built(fsys fs.FS) bool {
	_, err := fs.Stat(fsys, "index.html")
	return !errors.Is(err, fs.ErrNotExist)
}

// notBuilt is the page of a binary built without the web app.
const notBuilt = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Agora</title></head>
<body>
<h1>Agora</h1>
<p>The web app is not built into this binary, so only its API answers here.</p>
<p>Install agora from a release archive (https://github.com/vsem-azamat/agora/releases), which has it,
or run <code>make build-web</code> in a checkout before building agora.</p>
</body></html>
`

// Handler serves the built app: hashed files under /assets/ are cached for good, the page is
// never kept, everything else is revalidated, and missing files are not found. Without the app
// the page says how to get it and nothing else is found.
func Handler() http.Handler { return HandlerOf(files()) }

// HandlerOf serves the app files in fsys as Handler serves the built ones.
func HandlerOf(fsys fs.FS) http.Handler {
	server := http.FileServerFS(fsys)
	app := built(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !app {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuilt))
			return
		}
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case r.URL.Path == "/": // /index.html only redirects here
			// a page kept by the browser would run an old bundle after Back
			w.Header().Set("Cache-Control", "no-store")
		default:
			w.Header().Set("Cache-Control", "no-cache")
		}
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		server.ServeHTTP(w, r)
	})
}
