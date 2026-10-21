package ical

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
)

// Handler serves the read-only subscription feed at /calendar/<uuid>.ics.
//
// There is no authentication: the UUID is the credential. It is fixed once the
// calendar is created, so subscribers keep working across renames, and the
// owner decides per calendar whether the UUID is allowed to return anything at
// all.
type Handler struct {
	Store *store.Store
	// Now is only set by tests; production uses the wall clock.
	Now func() time.Time
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uuid, ok := feedUUID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	folder, err := h.Store.FolderByUUID(uuid)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Unknown and private calendars answer identically so the feed leaks
		// nothing about which UUIDs exist.
		http.NotFound(w, r)
		return
	}
	if !folder.Public {
		http.NotFound(w, r)
		return
	}

	events, err := h.Store.EventsByFolder(folder.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Occurrence overrides are stored apart from the event row, so without this
	// a rescheduled meeting would be advertised as its unmodified series.
	exceptions, err := h.Store.ExceptionsByFolder(folder.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		event.Exceptions = exceptions[event.ID]
	}

	now := time.Now
	if h.Now != nil {
		now = h.Now
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Robots-Tag", "noindex")
	if r.Method == http.MethodHead {
		return
	}
	feed := Feed{Name: folder.Name, Events: events}
	_, _ = w.Write([]byte(feed.Render(now())))
}

// feedUUID extracts the identifier from /calendar/<uuid>.ics.
func feedUUID(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/calendar/")
	if !ok {
		return "", false
	}
	uuid, ok := strings.CutSuffix(rest, ".ics")
	if !ok || uuid == "" || strings.ContainsAny(uuid, "/\\") {
		return "", false
	}
	return uuid, true
}
