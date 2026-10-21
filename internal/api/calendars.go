package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// stats serves GET /api/admin/stats: the few counters the dashboard shows.
func (h *Admin) stats(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "statistics are not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	users, err := h.Store.CountUsers()
	if err != nil {
		h.logError("cannot count users", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count users")
		return
	}
	messages, err := h.Store.CountMessages()
	if err != nil {
		h.logError("cannot count messages", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count messages")
		return
	}
	devices, err := h.Store.CountDevices()
	if err != nil {
		h.logError("cannot count devices", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count devices")
		return
	}
	pending, err := h.Store.CountOutbound(false)
	if err != nil {
		h.logError("cannot count queued mail", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count queued mail")
		return
	}
	failed, err := h.Store.CountOutbound(true)
	if err != nil {
		h.logError("cannot count failed mail", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count failed mail")
		return
	}
	domains := h.Settings.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"users":          users,
		"messages":       messages,
		"devices":        devices,
		"outbox_pending": pending,
		"outbox_failed":  failed,
		"domains":        len(domains.Domains),
		"smtp_ready":     domains.SMTPPassword != "",
		"version":        h.version(),
		"dialect":        h.Store.Dialect(),
	})
}

// migrations serves GET /api/admin/migrations.
func (h *Admin) migrations(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "migrations are not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := h.Store.MigrationStatus()
	if err != nil {
		h.logError("cannot read migration status", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot read migration status")
		return
	}
	type item struct {
		Version   int    `json:"version"`
		Name      string `json:"name"`
		Applied   bool   `json:"applied"`
		AppliedAt int64  `json:"applied_at"`
	}
	out := make([]item, 0, len(status))
	for _, m := range status {
		out = append(out, item{Version: m.Version, Name: m.Name, Applied: m.Applied, AppliedAt: m.AppliedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dialect": h.Store.Dialect(), "items": out})
}

// calendars serves /api/admin/calendars and
// /api/admin/calendars/{id}/public. It is the operator's view of the
// read-only iCalendar feeds; owners normally use /api/me/calendars.
func (h *Admin) calendars(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "calendars are not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/calendars"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.listCalendars(w)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		w.Header().Set("Allow", "PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if !isJSON(r) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	idText := strings.Trim(strings.TrimSuffix(rest, "/public"), "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid calendar id")
		return
	}
	var body struct {
		Public *bool `json:"public"`
	}
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil ||
		body.Public == nil {
		writeJSONError(w, http.StatusBadRequest, `expected {"public": true|false}`)
		return
	}
	if err := h.Store.SetFolderPublic(id, *body.Public); err != nil {
		writeJSONError(w, http.StatusNotFound, "calendar not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "public": *body.Public})
}

func (h *Admin) listCalendars(w http.ResponseWriter) {
	calendars, err := h.Store.AllCalendars()
	if err != nil {
		h.logError("cannot list calendars", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list calendars")
		return
	}
	type item struct {
		ID           int64  `json:"id"`
		UserID       string `json:"user_id"`
		Owner        string `json:"owner"`
		Name         string `json:"name"`
		Public       bool   `json:"public"`
		SubscribeURL string `json:"subscribe_url,omitempty"`
	}
	out := make([]item, 0, len(calendars))
	for _, c := range calendars {
		entry := item{ID: c.ID, UserID: c.UserID, Owner: c.Owner, Name: c.Name, Public: c.Public}
		if c.UUID != "" {
			entry.SubscribeURL = strings.TrimSuffix(h.BaseURL, "/") + "/calendar/" + c.UUID + ".ics"
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
