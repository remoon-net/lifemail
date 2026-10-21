package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"remoon.net/lifemail/internal/store"
)

type adminAliasView struct {
	Address   string `json:"address"`
	UserID    string `json:"user_id"`
	IsPrimary bool   `json:"is_primary"`
	CreatedAt int64  `json:"created_at"`
}

type adminAliasCreate struct {
	Address string `json:"address"`
	UserID  string `json:"user_id"`
}

func viewAlias(a *store.Alias) adminAliasView {
	return adminAliasView{Address: a.Address, UserID: a.UserID, IsPrimary: a.IsPrimary, CreatedAt: a.CreatedAt}
}

// aliases serves /api/admin/aliases and /api/admin/aliases/{address}.
func (h *Admin) aliases(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "alias management is not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/aliases"), "/")
	if raw == "" {
		switch r.Method {
		case http.MethodGet:
			h.listAliases(w)
		case http.MethodPost:
			h.createAlias(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	address, err := url.PathUnescape(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid alias address")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.deleteAlias(w, r, address)
	default:
		w.Header().Set("Allow", "DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Admin) listAliases(w http.ResponseWriter) {
	list, err := h.Store.ListAliases()
	if err != nil {
		h.logError("cannot list aliases", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list aliases")
		return
	}
	views := make([]adminAliasView, 0, len(list))
	for _, a := range list {
		views = append(views, viewAlias(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": views})
}

func (h *Admin) createAlias(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if !isJSON(r) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	defer r.Body.Close()
	var body adminAliasCreate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	address, err := cleanEmail(body.Address)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.UserID) == "" {
		writeJSONError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if owner, err := h.Store.UserByAddress(address); err == nil {
		writeJSONError(w, http.StatusConflict, "address already belongs to "+owner.Name)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		h.logError("cannot check address", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot check address")
		return
	}
	if msg := h.checkAliasDomain(address); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	alias, err := h.Store.CreateAlias(address, body.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrDuplicate):
			writeJSONError(w, http.StatusConflict, "alias already exists")
		case errors.Is(err, store.ErrNotFound):
			writeJSONError(w, http.StatusNotFound, "user not found")
		default:
			h.logError("cannot create alias", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot create alias")
		}
		return
	}
	h.logInfo("alias created", "address", alias.Address, "user", alias.UserID)
	writeJSON(w, http.StatusCreated, map[string]any{"alias": viewAlias(alias)})
}

func (h *Admin) deleteAlias(w http.ResponseWriter, r *http.Request, address string) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if err := h.Store.DeleteAlias(address); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "alias not found")
			return
		}
		if errors.Is(err, store.ErrPrimaryAddress) {
			writeJSONError(w, http.StatusConflict,
				"this is the primary address; set another primary address first")
			return
		}
		h.logError("cannot delete alias", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot delete alias")
		return
	}
	h.logInfo("alias deleted", "address", strings.ToLower(strings.TrimSpace(address)))
	w.WriteHeader(http.StatusNoContent)
}

// checkAliasDomain rejects aliases outside the configured domains, so typos do
// not silently create undeliverable addresses. With no domains configured the
// check is skipped.
func (h *Admin) checkAliasDomain(address string) string {
	if h.Settings == nil {
		return ""
	}
	cfg := h.Settings.Snapshot()
	if len(cfg.Domains) == 0 {
		return ""
	}
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return "invalid email address"
	}
	domain := address[at+1:]
	for _, d := range cfg.Domains {
		if strings.EqualFold(d.Name, domain) {
			return ""
		}
	}
	return "domain " + domain + " is not configured"
}
