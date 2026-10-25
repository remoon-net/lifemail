package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/store"
)

const (
	userCookieName  = "lifemail_user"
	userSessionTTL  = 12 * time.Hour
	userSessionSalt = "lifemail-user-session-v1"
)

// User serves the account owner's webmail API under /api/user/*. It is the
// read-mostly counterpart of the operator API: an account only ever sees its
// own folders, messages, calendars and contacts.
type User struct {
	Store *store.Store
	Log   *slog.Logger
	// Token is the admin token. The session cookie is signed with a key
	// derived from it, so no extra secret has to be configured. When empty the
	// webmail login is disabled (the Basic-auth /api/me surface still works).
	Token       string
	AuthLimiter *authlimit.Limiter
	// BaseURL is prepended to public calendar feed URLs.
	BaseURL string
	Version string
}

func (h *User) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/user"), "/")
	if path == "/session" {
		h.session(w, r)
		return
	}

	user := h.current(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	switch {
	case path == "/mail/folders":
		h.mailFolders(w, r, user)
	case path == "/mail/conversations":
		h.conversations(w, r, user)
	case path == "/mail/thread":
		h.thread(w, r, user)
	case strings.HasPrefix(path, "/mail/messages/"):
		h.message(w, r, user, strings.TrimPrefix(path, "/mail/messages/"))
	case strings.HasPrefix(path, "/mail/attachments/"):
		h.attachment(w, r, user, strings.TrimPrefix(path, "/mail/attachments/"))
	case path == "/calendars":
		h.calendars(w, r, user)
	case strings.HasPrefix(path, "/calendars/"):
		h.calendar(w, r, user, strings.TrimPrefix(path, "/calendars/"))
	case path == "/contacts":
		h.contacts(w, r, user)
	case strings.HasPrefix(path, "/contacts/"):
		h.contact(w, r, user, strings.TrimPrefix(path, "/contacts/"))
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// --- session ---------------------------------------------------------------

func (h *User) session(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if user := h.current(r); user != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"user":          userViewOf(user),
				"version":       h.version(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": false,
			"version":       h.version(),
		})
	case http.MethodPost:
		h.login(w, r)
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{
			Name: userCookieName, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *User) login(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.signingKey() == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "webmail login is not configured")
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
	ip := httpx.ClientIP(r)
	limitKey := "user|" + ip
	if h.AuthLimiter != nil {
		if locked, retry := h.AuthLimiter.Locked(limitKey); locked {
			w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
			writeJSONError(w, http.StatusTooManyRequests, "too many failed login attempts, try again later")
			return
		}
	}

	defer r.Body.Close()
	var body struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	user, err := h.Store.UserByLogin(body.Login)
	ok := err == nil && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) == nil
	if !ok {
		if h.AuthLimiter != nil {
			h.AuthLimiter.Fail(limitKey)
		}
		if h.Log != nil {
			h.Log.Warn("webmail login failed", "ip", ip, "login", body.Login)
		}
		time.Sleep(200 * time.Millisecond)
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if h.AuthLimiter != nil {
		h.AuthLimiter.Reset(limitKey)
	}
	h.issueSession(w, r, user)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          userViewOf(user),
		"version":       h.version(),
	})
}

func (h *User) issueSession(w http.ResponseWriter, r *http.Request, user *store.User) {
	expiry := time.Now().Add(userSessionTTL).Unix()
	payload := user.ID + "|" + strconv.FormatInt(expiry, 10)
	http.SetCookie(w, &http.Cookie{
		Name:     userCookieName,
		Value:    payload + "." + h.sign(payload),
		Path:     "/",
		MaxAge:   int(userSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// current resolves the session cookie to an account. It re-reads the user so a
// deleted account loses access immediately.
func (h *User) current(r *http.Request) *store.User {
	if h.Store == nil || h.signingKey() == nil {
		return nil
	}
	cookie, err := r.Cookie(userCookieName)
	if err != nil {
		return nil
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return nil
	}
	userID, expiryText, ok := strings.Cut(payload, "|")
	if !ok {
		return nil
	}
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil || expiry < time.Now().Unix() {
		return nil
	}
	if !hmac.Equal([]byte(signature), []byte(h.sign(payload))) {
		return nil
	}
	user, err := h.Store.UserByID(userID)
	if err != nil {
		return nil
	}
	return user
}

func (h *User) signingKey() []byte {
	if h.Token == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(h.Token))
	mac.Write([]byte(userSessionSalt))
	return mac.Sum(nil)
}

func (h *User) sign(payload string) string {
	mac := hmac.New(sha256.New, h.signingKey())
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *User) version() string {
	if h.Version == "" {
		return "dev"
	}
	return h.Version
}

func (h *User) logError(msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, "err", err)
	}
}

// --- mail ------------------------------------------------------------------

func (h *User) mailFolders(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	folders, err := h.Store.FoldersByUser(user.ID)
	if err != nil {
		h.logError("cannot list folders", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list folders")
		return
	}
	counts, err := h.Store.FolderCounts(user.ID)
	if err != nil {
		h.logError("cannot count folder messages", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot count folder messages")
		return
	}
	byFolder := make(map[int64]store.FolderCount, len(counts))
	for _, c := range counts {
		byFolder[c.FolderID] = c
	}
	type folder struct {
		ID       int64  `json:"id"`
		ParentID int64  `json:"parentId"`
		Name     string `json:"name"`
		Type     int    `json:"type"`
		Unread   int    `json:"unread"`
		Total    int    `json:"total"`
	}
	out := make([]folder, 0, len(folders))
	for _, f := range folders {
		if f.Deleted || f.Class != "Email" {
			continue
		}
		c := byFolder[f.ID]
		out = append(out, folder{
			ID: f.ID, ParentID: f.ParentID, Name: f.Name, Type: f.Type,
			Unread: c.Unread, Total: c.Total,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": out})
}

// resolveFolder reads the folder query parameter, defaulting to the inbox, and
// verifies it belongs to the account.
func (h *User) resolveFolder(r *http.Request, user *store.User) (*store.Folder, error) {
	value := r.URL.Query().Get("folder")
	if value == "" {
		return h.Store.FolderByType(user.ID, 2)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return nil, store.ErrNotFound
	}
	f, err := h.Store.FolderByID(id)
	if err != nil {
		return nil, err
	}
	if f.UserID != user.ID || f.Deleted || f.Class != "Email" {
		return nil, store.ErrNotFound
	}
	return f, nil
}

func (h *User) conversations(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	folder, err := h.resolveFolder(r, user)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "folder not found")
		return
	}
	limit := queryInt(r, "limit", 50)
	sums, err := h.Store.ConversationSummaries(folder.ID, limit)
	if err != nil {
		h.logError("cannot list conversations", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list conversations")
		return
	}
	type participant struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	type item struct {
		Key            string      `json:"key"`
		Subject        string      `json:"subject"`
		From           participant `json:"from"`
		To             string      `json:"to"`
		LastAt         int64       `json:"lastAt"`
		Count          int         `json:"count"`
		Unread         int         `json:"unread"`
		HasAttachments bool        `json:"hasAttachments"`
		Preview        string      `json:"preview"`
		LatestID       int64       `json:"latestMessageId"`
	}
	out := make([]item, 0, len(sums))
	for _, c := range sums {
		out = append(out, item{
			Key:            encodeKey(c.Key),
			Subject:        c.Subject,
			From:           participant{Name: c.FromName, Address: c.FromAddr},
			To:             c.ToAddr,
			LastAt:         c.LastAt,
			Count:          c.Count,
			Unread:         c.Unread,
			HasAttachments: c.HasAttachments,
			Preview:        c.Preview,
			LatestID:       c.LatestID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"folderId": folder.ID, "conversations": out})
}

func (h *User) thread(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	folder, err := h.resolveFolder(r, user)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "folder not found")
		return
	}
	key, err := decodeKey(r.URL.Query().Get("key"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid conversation key")
		return
	}
	messages, err := h.Store.ConversationMessages(folder.ID, key, 200)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "conversation not found")
			return
		}
		h.logError("cannot read conversation", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot read conversation")
		return
	}
	out := make([]messageView, 0, len(messages))
	for _, m := range messages {
		if m.UserID != user.ID {
			continue
		}
		view, err := h.messageView(m)
		if err != nil {
			h.logError("cannot read message", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read message")
			return
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"folderId": folder.ID, "messages": out})
}

func (h *User) message(w http.ResponseWriter, r *http.Request, user *store.User, rest string) {
	idText, action, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid message id")
		return
	}
	m, err := h.Store.MessageMetaByID(id)
	if err != nil || m.UserID != user.ID || m.Deleted {
		writeJSONError(w, http.StatusNotFound, "message not found")
		return
	}
	switch action {
	case "":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		view, err := h.messageView(m)
		if err != nil {
			h.logError("cannot read message", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read message")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": view})
	case "read":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !h.mutable(w, r) {
			return
		}
		if !m.IsRead {
			if err := h.Store.MarkMessageRead(m.ID, true); err != nil {
				h.logError("cannot mark message read", err)
				writeJSONError(w, http.StatusInternalServerError, "cannot mark message read")
				return
			}
			if _, err := h.Store.AddChange(m.FolderID, "change", "email", m.ID); err != nil {
				h.logError("cannot record read change", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "read": true})
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (h *User) attachment(w http.ResponseWriter, r *http.Request, user *store.User, idText string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}
	a, err := h.Store.AttachmentByID(id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "attachment not found")
		return
	}
	m, err := h.Store.MessageMetaByID(a.MessageID)
	if err != nil || m.UserID != user.ID {
		writeJSONError(w, http.StatusNotFound, "attachment not found")
		return
	}
	name := a.FileName
	if name == "" {
		name = "attachment"
	}
	contentType := a.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header := w.Header()
	header.Del("Content-Type")
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	header.Set("Content-Length", strconv.FormatInt(int64(len(a.Data)), 10))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Data)
}

// messageView is the wire shape of a single message, including its body and
// attachment metadata.
type messageView struct {
	ID             int64            `json:"id"`
	FolderID       int64            `json:"folderId"`
	Subject        string           `json:"subject"`
	FromName       string           `json:"fromName"`
	FromAddr       string           `json:"fromAddr"`
	To             string           `json:"to"`
	Cc             string           `json:"cc"`
	Date           int64            `json:"date"`
	IsRead         bool             `json:"isRead"`
	HasAttachments bool             `json:"hasAttachments"`
	BodyText       string           `json:"bodyText"`
	BodyHTML       string           `json:"bodyHtml"`
	Attachments    []attachmentView `json:"attachments"`
}

type attachmentView struct {
	ID          int64  `json:"id"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	IsInline    bool   `json:"isInline"`
}

func (h *User) messageView(m *store.Message) (messageView, error) {
	view := messageView{
		ID: m.ID, FolderID: m.FolderID, Subject: m.Subject,
		FromName: m.FromName, FromAddr: m.FromAddr, To: m.ToAddr, Cc: m.CcAddr,
		Date: m.DateReceived, IsRead: m.IsRead, HasAttachments: m.HasAttachments,
		BodyText: m.BodyText, BodyHTML: m.BodyHTML,
		Attachments: []attachmentView{},
	}
	attachments, err := h.Store.AttachmentsByMessage(m.ID)
	if err != nil {
		return view, err
	}
	for _, a := range attachments {
		view.Attachments = append(view.Attachments, attachmentView{
			ID: a.ID, FileName: a.FileName, ContentType: a.ContentType,
			Size: a.Size, IsInline: a.IsInline,
		})
	}
	return view, nil
}

// --- calendars -------------------------------------------------------------

func (h *User) calendars(w http.ResponseWriter, r *http.Request, user *store.User) {
	switch r.Method {
	case http.MethodGet:
		folders, err := h.Store.CalendarFolders(user.ID)
		if err != nil {
			h.logError("cannot list calendars", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot list calendars")
			return
		}
		out := make([]calendarView, 0, len(folders))
		for _, f := range folders {
			view, err := h.calendarViewOf(f)
			if err != nil {
				h.logError("cannot count calendar events", err)
				writeJSONError(w, http.StatusInternalServerError, "cannot count calendar events")
				return
			}
			out = append(out, view)
		}
		writeJSON(w, http.StatusOK, map[string]any{"calendars": out})
	case http.MethodPost:
		if !h.mutable(w, r) {
			return
		}
		name, ok := h.calendarName(w, r)
		if !ok {
			return
		}
		if _, err := h.Store.FolderByName(user.ID, name); err == nil {
			writeJSONError(w, http.StatusConflict, "a calendar with that name already exists")
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			h.logError("cannot check calendar name", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot check calendar name")
			return
		}
		f, err := h.Store.CreateFolder(user.ID, 0, name, store.FolderTypeCalendar)
		if err != nil {
			h.logError("cannot create calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot create calendar")
			return
		}
		view, err := h.calendarViewOf(f)
		if err != nil {
			h.logError("cannot read calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read calendar")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"calendar": view})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type calendarView struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Public       bool   `json:"public"`
	SubscribeURL string `json:"subscribeUrl,omitempty"`
	EventCount   int    `json:"eventCount"`
}

func (h *User) calendarViewOf(f *store.Folder) (calendarView, error) {
	events, err := h.Store.EventsByFolder(f.ID)
	if err != nil {
		return calendarView{}, err
	}
	view := calendarView{ID: f.ID, Name: f.Name, Public: f.Public, EventCount: len(events)}
	if f.UUID != "" {
		view.SubscribeURL = strings.TrimSuffix(h.BaseURL, "/") + "/calendar/" + f.UUID + ".ics"
	}
	return view, nil
}

func (h *User) calendar(w http.ResponseWriter, r *http.Request, user *store.User, rest string) {
	idText, action, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid calendar id")
		return
	}
	f, err := h.Store.FolderByID(id)
	if err != nil || f.UserID != user.ID || f.Deleted || f.Type != store.FolderTypeCalendar {
		writeJSONError(w, http.StatusNotFound, "calendar not found")
		return
	}
	switch action {
	case "":
		if r.Method != http.MethodPatch && r.Method != http.MethodPut {
			w.Header().Set("Allow", "PATCH, PUT")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !h.mutable(w, r) {
			return
		}
		name, ok := h.calendarName(w, r)
		if !ok {
			return
		}
		if err := h.Store.RenameFolder(f.ID, name); err != nil {
			h.logError("cannot rename calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot rename calendar")
			return
		}
		updated, err := h.Store.FolderByID(f.ID)
		if err != nil {
			h.logError("cannot read calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read calendar")
			return
		}
		view, err := h.calendarViewOf(updated)
		if err != nil {
			h.logError("cannot read calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read calendar")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"calendar": view})
	case "public":
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			w.Header().Set("Allow", "PUT, POST")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !h.mutable(w, r) {
			return
		}
		var body struct {
			Public *bool `json:"public"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Public == nil {
			writeJSONError(w, http.StatusBadRequest, "public is required")
			return
		}
		if err := h.Store.SetFolderPublic(f.ID, *body.Public); err != nil {
			h.logError("cannot update calendar feed", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot update calendar feed")
			return
		}
		updated, err := h.Store.FolderByID(f.ID)
		if err != nil {
			h.logError("cannot read calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read calendar")
			return
		}
		view, err := h.calendarViewOf(updated)
		if err != nil {
			h.logError("cannot read calendar", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot read calendar")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"calendar": view})
	case "events":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		from := int64(queryInt(r, "from", 0))
		to := int64(queryInt(r, "to", 0))
		events, err := h.Store.EventsByFolderBetween(f.ID, from, to)
		if err != nil {
			h.logError("cannot list events", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot list events")
			return
		}
		type eventView struct {
			ID          int64  `json:"id"`
			UID         string `json:"uid"`
			Subject     string `json:"subject"`
			Location    string `json:"location"`
			Body        string `json:"body"`
			Start       int64  `json:"start"`
			End         int64  `json:"end"`
			AllDay      bool   `json:"allDay"`
			BusyStatus  int    `json:"busyStatus"`
			Recurring   bool   `json:"recurring"`
			Organizer   string `json:"organizer"`
			OrganizerAt string `json:"organizerAddress"`
		}
		out := make([]eventView, 0, len(events))
		for _, e := range events {
			out = append(out, eventView{
				ID: e.ID, UID: e.UID, Subject: e.Subject, Location: e.Location,
				Body: e.Body, Start: e.StartTime, End: e.EndTime, AllDay: e.AllDay,
				BusyStatus: e.BusyStatus, Recurring: e.Recurrence.Active(),
				Organizer: e.OrganizerName, OrganizerAt: e.OrganizerEmail,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": out})
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (h *User) calendarName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return "", false
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 100 {
		writeJSONError(w, http.StatusBadRequest, "calendar name must be 1-100 characters")
		return "", false
	}
	return name, true
}

// --- contacts --------------------------------------------------------------

func (h *User) contacts(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := queryInt(r, "limit", 100)
	found, err := h.Store.SearchContacts(user.ID, r.URL.Query().Get("q"), limit)
	if err != nil {
		h.logError("cannot list contacts", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot list contacts")
		return
	}
	out := make([]contactView, 0, len(found))
	for _, c := range found {
		out = append(out, contactViewOf(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": out})
}

func (h *User) contact(w http.ResponseWriter, r *http.Request, user *store.User, idText string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid contact id")
		return
	}
	c, err := h.Store.ContactByID(id)
	if err != nil || c.UserID != user.ID || c.Deleted {
		writeJSONError(w, http.StatusNotFound, "contact not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contact": contactViewOf(c)})
}

type contactView struct {
	ID          int64  `json:"id"`
	Display     string `json:"display"`
	FileAs      string `json:"fileAs"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	MiddleName  string `json:"middleName"`
	Company     string `json:"company"`
	Department  string `json:"department"`
	JobTitle    string `json:"jobTitle"`
	Email1      string `json:"email1"`
	Email2      string `json:"email2"`
	Email3      string `json:"email3"`
	HomePhone   string `json:"homePhone"`
	Mobile      string `json:"mobilePhone"`
	Business    string `json:"businessPhone"`
	Fax         string `json:"businessFax"`
	HomeStreet  string `json:"homeStreet"`
	HomeCity    string `json:"homeCity"`
	HomeState   string `json:"homeState"`
	HomePostal  string `json:"homePostalCode"`
	HomeCountry string `json:"homeCountry"`
	WorkStreet  string `json:"businessStreet"`
	WorkCity    string `json:"businessCity"`
	WorkState   string `json:"businessState"`
	WorkPostal  string `json:"businessPostalCode"`
	WorkCountry string `json:"businessCountry"`
	Body        string `json:"body"`
}

func contactViewOf(c *store.Contact) contactView {
	display := c.FileAs
	if display == "" {
		display = strings.TrimSpace(c.FirstName + " " + c.LastName)
	}
	return contactView{
		ID: c.ID, Display: display, FileAs: c.FileAs,
		FirstName: c.FirstName, LastName: c.LastName, MiddleName: c.MiddleName,
		Company: c.Company, Department: c.Department, JobTitle: c.JobTitle,
		Email1: c.Email1, Email2: c.Email2, Email3: c.Email3,
		HomePhone: c.HomePhone, Mobile: c.MobilePhone, Business: c.BusinessPhone,
		Fax:        c.BusinessFax,
		HomeStreet: c.HomeStreet, HomeCity: c.HomeCity, HomeState: c.HomeState,
		HomePostal: c.HomePostalCode, HomeCountry: c.HomeCountry,
		WorkStreet: c.BusinessStreet, WorkCity: c.BusinessCity,
		WorkState: c.BusinessState, WorkPostal: c.BusinessPostalCode,
		WorkCountry: c.BusinessCountry, Body: c.Body,
	}
}

// --- helpers ---------------------------------------------------------------

func userViewOf(u *store.User) map[string]any {
	return map[string]any{
		"id":          u.ID,
		"name":        u.Name,
		"address":     u.Address,
		"displayName": u.DisplayName,
	}
}

// mutable enforces the CSRF guards for state-changing requests. A bodyless
// request (marking a message read) may omit Content-Type, but any body must be
// JSON: browsers send urlencoded for cross-site forms, which this rejects.
func (h *User) mutable(w http.ResponseWriter, r *http.Request) bool {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
		return false
	}
	if ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))); ct != "" && !isJSON(r) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if r.Body == nil {
		writeJSONError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	defer r.Body.Close()
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dest); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func encodeKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeKey(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty key")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	key := string(raw)
	if key == "" {
		return "", errors.New("empty key")
	}
	return key, nil
}
