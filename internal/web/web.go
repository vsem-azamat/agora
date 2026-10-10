// Package web holds the web app's built files, embedded in the binary, and serves them.
//
// The files in dist/ are built from web/ by `pnpm --dir web build` and committed, so building
// agora needs no Node.js; CI checks that they match the sources.
package web

import (
	"embed"
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

// Handler serves the built app: hashed files under /assets/ are cached for good, everything
// else is revalidated, and missing files are not found.
func Handler() http.Handler {
	server := http.FileServerFS(files())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		server.ServeHTTP(w, r)
	})
}
