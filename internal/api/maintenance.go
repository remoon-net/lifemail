package api

import (
	"encoding/json"
	"io"
	"net/http"
)

type maintenanceRequest struct {
	Vacuum bool `json:"vacuum"`
}

type maintenanceResult struct {
	ChangesPruned int64 `json:"changes_pruned"`
	Vacuumed      bool  `json:"vacuumed"`
}

// maintenance serves POST /api/admin/maintenance, the manual database cleanup
// hook. Pruning only drops change records every device has consumed; VACUUM is
// opt-in because it rewrites the whole database.
func (h *Admin) maintenance(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "maintenance is not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	var body maintenanceRequest
	if r.ContentLength != 0 {
		defer r.Body.Close()
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
	}

	pruned, err := h.Store.PruneChanges()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := maintenanceResult{ChangesPruned: pruned}
	if body.Vacuum {
		if err := h.Store.Vacuum(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result.Vacuumed = true
	}
	if h.Log != nil {
		h.Log.Info("database maintenance", "changes_pruned", pruned, "vacuumed", result.Vacuumed)
	}
	writeJSON(w, http.StatusOK, result)
}
