// Package webui serves the embedded SvelteKit build.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The build directory is produced by `make web` (SvelteKit + adapter-static).
//
//go:embed all:build
var assets embed.FS

// Handler serves the UI build from the site root. It contains two prerendered
// trees: /admin/* (operator console) and /app/* (account UI).
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "build")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// Script/style restrictions come from the CSP meta tag that SvelteKit
		// prerenders into index.html (with hashes for its inline bootstrap).
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		// Content-hashed assets never change; everything else must be
		// revalidated so a deploy is picked up immediately.
		if strings.Contains(r.URL.Path, "/_app/immutable/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
