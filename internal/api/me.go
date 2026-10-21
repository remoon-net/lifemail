package api

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/store"
)

// Me exposes the account owner's own calendar settings over HTTP, authenticated
// with the same mailbox credentials the client already uses for IMAP/SMTP.
//
// It exists so that "share this calendar publicly" is the owner's decision
// rather than an operator task.
type Me struct {
	Store *store.Store
	Log   *slog.Logger
	// AuthLimiter throttles repeated bad passwords. Optional.
	AuthLimiter *authlimit.Limiter
	// BaseURL is prepended to feed paths in listings, for example
	// "https://mail.example.com". Optional.
	BaseURL string
}

func (h *Me) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	user := h.authenticate(w, r)
	if user == nil {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/me/calendars")
	switch {
	case rest == "" || rest == "/":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.list(w, user)
	case strings.HasSuffix(rest, "/public"):
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.setPublic(w, r, user, strings.TrimSuffix(rest, "/public"))
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// authenticate resolves the Basic credentials to an account, replying on
// failure. It returns nil when the caller must stop.
func (h *Me) authenticate(w http.ResponseWriter, r *http.Request) *store.User {
	login, password, ok := basicAuth(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="lifemail"`)
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return nil
	}
	if h.AuthLimiter != nil {
		if locked, retry := h.AuthLimiter.Locked(login); locked {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			writeJSONError(w, http.StatusTooManyRequests, "too many attempts")
			return nil
		}
	}
	user, err := h.Store.UserByLogin(login)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if h.AuthLimiter != nil {
			h.AuthLimiter.Fail(login)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="lifemail"`)
		writeJSONError(w, http.StatusUnauthorized, "bad credentials")
		return nil
	}
	if h.AuthLimiter != nil {
		h.AuthLimiter.Reset(login)
	}
	return user
}

func (h *Me) list(w http.ResponseWriter, user *store.User) {
	folders, err := h.Store.CalendarFolders(user.ID)
	if err != nil {
		h.fail(w, "cannot list calendars", err)
		return
	}
	type calendar struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Public       bool   `json:"public"`
		SubscribeURL string `json:"subscribeUrl,omitempty"`
	}
	out := make([]calendar, 0, len(folders))
	for _, f := range folders {
		c := calendar{ID: f.ID, Name: f.Name, Public: f.Public}
		if f.UUID != "" {
			c.SubscribeURL = strings.TrimSuffix(h.BaseURL, "/") + "/calendar/" + f.UUID + ".ics"
		}
		out = append(out, c)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"calendars": out})
}

// setPublic toggles one calendar. The path carries the folder id, which is
// scoped to the authenticated user, so no one can flip somebody else's feed.
func (h *Me) setPublic(w http.ResponseWriter, r *http.Request, user *store.User, suffix string) {
	idText := strings.Trim(strings.TrimSuffix(suffix, "/public"), "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid calendar id")
		return
	}

	var body struct {
		Public *bool `json:"public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Public == nil {
		writeJSONError(w, http.StatusBadRequest, `expected {"public": true|false}`)
		return
	}

	folder, err := h.Store.FolderByID(id)
	if err != nil || folder.UserID != user.ID || folder.Deleted {
		writeJSONError(w, http.StatusNotFound, "calendar not found")
		return
	}
	if err := h.Store.SetFolderPublic(id, *body.Public); err != nil {
		writeJSONError(w, http.StatusNotFound, "not a calendar folder")
		return
	}

	folder.Public = *body.Public
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           folder.ID,
		"name":         folder.Name,
		"public":       folder.Public,
		"subscribeUrl": strings.TrimSuffix(h.BaseURL, "/") + "/calendar/" + folder.UUID + ".ics",
	})
}

func (h *Me) fail(w http.ResponseWriter, msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, "err", err)
	}
	writeJSONError(w, http.StatusInternalServerError, "internal error")
}

// basicAuth pulls credentials without letting net/http's BasicAuth helper
// silently accept an empty password.
func basicAuth(r *http.Request) (string, string, bool) {
	login, password, ok := r.BasicAuth()
	login = strings.TrimSpace(login)
	if !ok || login == "" || subtle.ConstantTimeEq(int32(len(password)), 0) == 1 {
		return "", "", false
	}
	return login, password, true
}
