package api

import (
	"net/http"
	"strconv"
	"strings"
)

type adminOutboxView struct {
	ID           int64    `json:"id"`
	EnvelopeFrom string   `json:"envelope_from"`
	Recipients   []string `json:"recipients"`
	Attempts     int      `json:"attempts"`
	LastError    string   `json:"last_error"`
	NextAttempt  int64    `json:"next_attempt"`
	Failed       bool     `json:"failed"`
	CreatedAt    int64    `json:"created_at"`
	Size         int      `json:"size"`
}

// outbox serves /api/admin/outbox and /api/admin/outbox/{id}[/retry].
func (h *Admin) outbox(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "outbox is not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/outbox"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.listOutbox(w, r)
		return
	}

	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid outbox id")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodPost && action == "retry":
		if !sameOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if _, err := h.Store.OutboundByID(id); err != nil {
			writeJSONError(w, http.StatusNotFound, "message not found")
			return
		}
		if err := h.Store.RetryOutboundNow(id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if h.Log != nil {
			h.Log.Info("outbound message requeued", "id", id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodDelete && action == "":
		if !sameOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if err := h.Store.DeleteOutbound(id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if h.Log != nil {
			h.Log.Info("outbound message dropped", "id", id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Admin) listOutbox(w http.ResponseWriter, r *http.Request) {
	failedOnly := r.URL.Query().Get("failed") == "1" || r.URL.Query().Get("failed") == "true"
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.Store.ListOutbound(failedOnly, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]adminOutboxView, 0, len(items))
	for _, item := range items {
		views = append(views, adminOutboxView{
			ID:           item.ID,
			EnvelopeFrom: item.EnvelopeFrom,
			Recipients:   item.Recipients,
			Attempts:     item.Attempts,
			LastError:    item.LastError,
			NextAttempt:  item.NextAttempt,
			Failed:       item.Failed,
			CreatedAt:    item.CreatedAt,
			Size:         len(item.Raw),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}
