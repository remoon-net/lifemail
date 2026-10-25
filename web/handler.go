// Package webui serves the SvelteKit build from a directory on disk.
//
// The build is no longer embedded in the binary, so the UI can be updated by
// replacing files on disk (and so the binary stays small). The directory holds
// two prerendered trees: /admin/* (operator console) and /app/* (account UI).
package webui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DirHandler serves the UI build from dir. The returned handler sets the
// security and cache headers the build expects; the caller only has to mount
// it on "/".
func DirHandler(dir string) (http.Handler, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("web dir is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("web dir %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("web dir %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("web dir %q is not a directory", abs)
	}
	// The prerendered root page proves the directory is a SvelteKit build and
	// not, say, a mount point that failed to mount.
	if _, err := os.Stat(filepath.Join(abs, "index.html")); err != nil {
		return nil, fmt.Errorf("web dir %q has no index.html (run make web): %w", abs, err)
	}

	files := http.FileServer(http.Dir(abs))
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
	}), nil
}
