package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/store"
)

type adminUserView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	CreatedAt   int64  `json:"created_at"`
}

type adminUserCreate struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type adminUserPatch struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	Address     *string `json:"address"`
}

// adminUserResponse carries a freshly generated password only once: it is
// returned on creation and on password reset, never on reads.
type adminUserResponse struct {
	User     adminUserView `json:"user"`
	Password string        `json:"password,omitempty"`
}

func viewUser(u *store.User) adminUserView {
	return adminUserView{
		ID:          u.ID,
		Name:        u.Name,
		Address:     u.Address,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt,
	}
}

// users serves /api/admin/users and /api/admin/users/{id}.
func (h *Admin) users(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "user management is not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users"), "/")
	if id == "" {
		switch r.Method {
		case http.MethodGet:
			h.listUsers(w)
		case http.MethodPost:
			h.createUser(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch r.Method {
	case http.MethodPatch:
		h.updateUser(w, r, id)
	case http.MethodDelete:
		h.deleteUser(w, r, id)
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Admin) listUsers(w http.ResponseWriter) {
	users, err := h.Store.ListUsers()
	if err != nil {
		h.logError("cannot list users", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list users")
		return
	}
	views := make([]adminUserView, 0, len(users))
	for _, u := range users {
		views = append(views, viewUser(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": views})
}

func (h *Admin) createUser(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if !isJSON(r) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	defer r.Body.Close()
	var body adminUserCreate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	address := ""
	if strings.TrimSpace(body.Address) != "" {
		cleaned, err := cleanEmail(body.Address)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := h.checkAliasDomain(cleaned); msg != "" {
			writeJSONError(w, http.StatusBadRequest, msg)
			return
		}
		address = cleaned
	}
	password := body.Password
	generated := false
	if password == "" {
		password = randomPassword()
		generated = true
	}
	hash, err := hashPassword(password)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := h.Store.CreateUser(name, address, strings.TrimSpace(body.DisplayName), hash)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeJSONError(w, http.StatusConflict, "user name or address already exists")
			return
		}
		h.logError("cannot create user", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot create user")
		return
	}
	h.logInfo("user created", "name", user.Name)
	resp := adminUserResponse{User: viewUser(user)}
	if generated {
		resp.Password = password
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Admin) updateUser(w http.ResponseWriter, r *http.Request, id string) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if !isJSON(r) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}
	defer r.Body.Close()
	var body adminUserPatch
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.DisplayName == nil && body.Password == nil && body.Address == nil {
		writeJSONError(w, http.StatusBadRequest, "nothing to update")
		return
	}

	var generated string
	if body.Address != nil {
		address, err := cleanEmail(*body.Address)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg := h.checkAliasDomain(address); msg != "" {
			writeJSONError(w, http.StatusBadRequest, msg)
			return
		}
		if _, err := h.Store.SetPrimaryAddress(id, address); err != nil {
			switch {
			case errors.Is(err, store.ErrDuplicate):
				writeJSONError(w, http.StatusConflict, "address already belongs to another account")
			default:
				h.writeUserError(w, "cannot update address", err)
			}
			return
		}
	}
	if body.DisplayName != nil {
		name := strings.TrimSpace(*body.DisplayName)
		if name == "" {
			writeJSONError(w, http.StatusBadRequest, "display_name must not be empty")
			return
		}
		if err := h.Store.UpdateDisplayName(id, name); err != nil {
			h.writeUserError(w, "cannot update user", err)
			return
		}
	}
	if body.Password != nil {
		password := *body.Password
		if password == "" {
			password = randomPassword()
			generated = password
		}
		hash, err := hashPassword(password)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.SetPasswordByID(id, hash); err != nil {
			h.writeUserError(w, "cannot update user", err)
			return
		}
	}

	user, err := h.Store.UserByID(id)
	if err != nil {
		h.writeUserError(w, "cannot load user", err)
		return
	}
	h.logInfo("user updated", "name", user.Name)
	writeJSON(w, http.StatusOK, adminUserResponse{User: viewUser(user), Password: generated})
}

func (h *Admin) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user, err := h.Store.UserByID(id)
	if err != nil {
		h.writeUserError(w, "cannot delete user", err)
		return
	}
	if err := h.Store.DeleteUser(id); err != nil {
		h.writeUserError(w, "cannot delete user", err)
		return
	}
	h.logInfo("user deleted", "name", user.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Admin) writeUserError(w http.ResponseWriter, message string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	h.logError(message, err)
	writeJSONError(w, http.StatusInternalServerError, message)
}

func (h *Admin) logInfo(msg string, args ...any) {
	if h.Log != nil {
		h.Log.Info(msg, args...)
	}
}

func (h *Admin) logError(msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, "err", err)
	}
}

func cleanEmail(raw string) (string, error) {
	address := strings.ToLower(strings.TrimSpace(raw))
	if address == "" {
		return "", errors.New("address is required")
	}
	addr, err := mail.ParseAddress(address)
	if err != nil || !strings.EqualFold(addr.Address, address) {
		return "", errors.New("invalid email address")
	}
	return address, nil
}

func hashPassword(password string) (string, error) {
	if len(password) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", errors.New("cannot hash password")
	}
	return string(hash), nil
}

func randomPassword() string {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
