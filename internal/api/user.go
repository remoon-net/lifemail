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
	"remoon.net/lifemail/internal/smtp"
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
	// Sender delivers replies to external recipients. Optional: without it,
	// replies to outside addresses are refused.
	Sender *smtp.Sender
	// IsLocalDomain reports whether a domain is hosted here, so replies to
	// unknown local mailboxes can be rejected instead of queued.
	IsLocalDomain func(string) bool
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
	case path == "/mail/reply":
		h.reply(w, r, user)
	case path == "/mail/send":
		h.send(w, r, user)
	case path == "/mail/search":
		h.searchMail(w, r, user)
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
	sums, err := h.Store.ConversationSummaries(user.ID, folder.ID, limit)
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
	messages, err := h.Store.ConversationMessages(user.ID, key, folder.ID, 200)
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
	sentID := int64(0)
	if sent, err := h.Store.FolderByType(user.ID, 5); err == nil {
		sentID = sent.ID
	}
	payload := map[string]any{
		"folderId": folder.ID, "sentFolderId": sentID, "messages": out,
	}
	if target := h.replyTargetFor(user, messages, sentID); target != nil {
		payload["replyTarget"] = target
	}
	writeJSON(w, http.StatusOK, payload)
}

// searchMail serves GET /mail/search?q=&folder=&limit=. The search covers every
// folder of the account unless folder is given.
func (h *User) searchMail(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"query": "", "total": 0, "hits": []any{},
		})
		return
	}
	folderID := int64(queryInt(r, "folder", 0))
	if folderID > 0 {
		folder, err := h.Store.FolderByID(folderID)
		if err != nil || folder.UserID != user.ID || folder.Deleted {
			writeJSONError(w, http.StatusNotFound, "folder not found")
			return
		}
	}
	limit := queryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	hits, total, err := h.Store.SearchMail(user.ID, query, folderID, limit, 0)
	if err != nil {
		h.logError("cannot search messages", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot search messages")
		return
	}
	type hit struct {
		ID         int64  `json:"id"`
		FolderID   int64  `json:"folderId"`
		FolderName string `json:"folderName"`
		Key        string `json:"key"`
		Subject    string `json:"subject"`
		FromName   string `json:"fromName"`
		FromAddr   string `json:"fromAddr"`
		To         string `json:"to"`
		Date       int64  `json:"date"`
		IsRead     bool   `json:"isRead"`
		Preview    string `json:"preview"`
	}
	out := make([]hit, 0, len(hits))
	for _, item := range hits {
		m := item.Message
		out = append(out, hit{
			ID: m.ID, FolderID: m.FolderID, FolderName: item.FolderName,
			Key: encodeKey(item.ConversationKey), Subject: m.Subject,
			FromName: m.FromName, FromAddr: m.FromAddr, To: m.ToAddr,
			Date: m.DateReceived, IsRead: m.IsRead, Preview: item.Preview,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query": query, "total": total, "hits": out,
	})
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
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid calendar id")
		return
	}
	f, err := h.Store.FolderByID(id)
	if err != nil || f.UserID != user.ID || f.Deleted || f.Type != store.FolderTypeCalendar {
		writeJSONError(w, http.StatusNotFound, "calendar not found")
		return
	}
	switch {
	case len(parts) == 1:
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
	case len(parts) == 2 && parts[1] == "public":
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
	case len(parts) == 2 && parts[1] == "events":
		h.calendarEvents(w, r, user, f)
	case len(parts) == 3 && parts[1] == "events":
		h.calendarEvent(w, r, user, f, parts[2])
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

// eventView is the wire shape of a calendar event.
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

func eventViewOf(e *store.Event) eventView {
	return eventView{
		ID: e.ID, UID: e.UID, Subject: e.Subject, Location: e.Location,
		Body: e.Body, Start: e.StartTime, End: e.EndTime, AllDay: e.AllDay,
		BusyStatus: e.BusyStatus, Recurring: e.Recurrence.Active(),
		Organizer: e.OrganizerName, OrganizerAt: e.OrganizerEmail,
	}
}

// eventInput carries the fields the account UI may change. Only single
// occurrences are created here; a repeated event keeps its recurrence rule
// when edited, because the UI never sends one.
type eventInput struct {
	Subject    *string `json:"subject"`
	Location   *string `json:"location"`
	Body       *string `json:"body"`
	Start      *int64  `json:"start"`
	End        *int64  `json:"end"`
	AllDay     *bool   `json:"allDay"`
	BusyStatus *int    `json:"busyStatus"`
	Reminder   *int    `json:"reminder"`
}

func (h *User) calendarEvents(w http.ResponseWriter, r *http.Request, user *store.User, f *store.Folder) {
	switch r.Method {
	case http.MethodGet:
		from := int64(queryInt(r, "from", 0))
		to := int64(queryInt(r, "to", 0))
		events, err := h.Store.EventsByFolderBetween(f.ID, from, to)
		if err != nil {
			h.logError("cannot list events", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot list events")
			return
		}
		out := make([]eventView, 0, len(events))
		for _, e := range events {
			out = append(out, eventViewOf(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": out})
	case http.MethodPost:
		h.createEvent(w, r, user, f)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *User) createEvent(w http.ResponseWriter, r *http.Request, user *store.User, f *store.Folder) {
	if !h.mutable(w, r) {
		return
	}
	var in eventInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Subject == nil || strings.TrimSpace(*in.Subject) == "" {
		writeJSONError(w, http.StatusBadRequest, "subject is required")
		return
	}
	if in.Start == nil || in.End == nil {
		writeJSONError(w, http.StatusBadRequest, "start and end are required")
		return
	}
	e := &store.Event{
		UserID: f.UserID, FolderID: f.ID,
		UID:            store.NewUUID() + "@lifemail",
		Subject:        strings.TrimSpace(*in.Subject),
		StartTime:      *in.Start,
		EndTime:        *in.End,
		OrganizerName:  user.DisplayName,
		OrganizerEmail: user.Address,
	}
	if err := applyEventInput(e, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.Store.InsertEvent(e)
	if err != nil {
		h.logError("cannot create event", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot create event")
		return
	}
	e.ID = id
	if _, err := h.Store.AddChange(f.ID, "add", "event", id); err != nil {
		h.logError("cannot record event change", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"event": eventViewOf(e)})
}

func (h *User) calendarEvent(w http.ResponseWriter, r *http.Request, user *store.User, f *store.Folder, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid event id")
		return
	}
	e, err := h.Store.EventByID(id)
	if err != nil || e.UserID != user.ID || e.FolderID != f.ID || e.Deleted {
		writeJSONError(w, http.StatusNotFound, "event not found")
		return
	}
	switch r.Method {
	case http.MethodPatch, http.MethodPut:
		if !h.mutable(w, r) {
			return
		}
		var in eventInput
		if !decodeJSON(w, r, &in) {
			return
		}
		if err := applyEventInput(e, &in); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.UpdateEvent(e); err != nil {
			h.logError("cannot update event", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot update event")
			return
		}
		if _, err := h.Store.AddChange(f.ID, "change", "event", e.ID); err != nil {
			h.logError("cannot record event change", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"event": eventViewOf(e)})
	case http.MethodDelete:
		if !h.mutable(w, r) {
			return
		}
		e.Deleted = true
		if err := h.Store.UpdateEvent(e); err != nil {
			h.logError("cannot delete event", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot delete event")
			return
		}
		if _, err := h.Store.AddChange(f.ID, "delete", "event", e.ID); err != nil {
			h.logError("cannot record event change", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "PATCH, PUT, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// applyEventInput copies the provided fields and validates the result. The
// recurrence, attendees and meeting state are left untouched: EAS clients own
// those, and the browser only edits a single occurrence's basics.
func applyEventInput(e *store.Event, in *eventInput) error {
	if in.Subject != nil {
		subject := strings.TrimSpace(*in.Subject)
		if subject == "" || len([]rune(subject)) > 255 {
			return errors.New("subject must be 1-255 characters")
		}
		e.Subject = subject
	}
	if in.Location != nil {
		e.Location = strings.TrimSpace(*in.Location)
	}
	if in.Body != nil {
		e.Body = *in.Body
	}
	if in.Start != nil {
		e.StartTime = *in.Start
	}
	if in.End != nil {
		e.EndTime = *in.End
	}
	if in.AllDay != nil {
		e.AllDay = *in.AllDay
	}
	if in.BusyStatus != nil {
		if *in.BusyStatus < 0 || *in.BusyStatus > 4 {
			return errors.New("busy status must be 0-4")
		}
		e.BusyStatus = *in.BusyStatus
	}
	if in.Reminder != nil {
		if *in.Reminder < 0 || *in.Reminder > 40320 {
			return errors.New("reminder must be 0-40320 minutes")
		}
		e.Reminder = *in.Reminder
	}
	if e.StartTime <= 0 || e.EndTime <= 0 {
		return errors.New("start and end must be unix seconds")
	}
	if e.EndTime < e.StartTime {
		return errors.New("end must not be before start")
	}
	if e.EndTime-e.StartTime > 366*86400 {
		return errors.New("event is longer than a year")
	}
	return nil
}

// --- contacts --------------------------------------------------------------

func (h *User) contacts(w http.ResponseWriter, r *http.Request, user *store.User) {
	switch r.Method {
	case http.MethodGet:
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
	case http.MethodPost:
		h.createContact(w, r, user)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *User) contact(w http.ResponseWriter, r *http.Request, user *store.User, idText string) {
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
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"contact": contactViewOf(c)})
	case http.MethodPatch, http.MethodPut:
		if !h.mutable(w, r) {
			return
		}
		var in contactInput
		if !decodeJSON(w, r, &in) {
			return
		}
		if err := applyContactInput(c, &in); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !contactIdentified(c) {
			writeJSONError(w, http.StatusBadRequest, "contact needs a name, company or email")
			return
		}
		if c.FileAs == "" {
			c.FileAs = contactDisplayName(c)
		}
		if err := h.Store.UpdateContact(c); err != nil {
			h.logError("cannot update contact", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot update contact")
			return
		}
		if _, err := h.Store.AddChange(c.FolderID, "change", "contact", c.ID); err != nil {
			h.logError("cannot record contact change", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"contact": contactViewOf(c)})
	case http.MethodDelete:
		if !h.mutable(w, r) {
			return
		}
		c.Deleted = true
		if err := h.Store.UpdateContact(c); err != nil {
			h.logError("cannot delete contact", err)
			writeJSONError(w, http.StatusInternalServerError, "cannot delete contact")
			return
		}
		if _, err := h.Store.AddChange(c.FolderID, "delete", "contact", c.ID); err != nil {
			h.logError("cannot record contact change", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "GET, PATCH, PUT, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
	Home2Phone  string `json:"home2Phone"`
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
		HomePhone: c.HomePhone, Home2Phone: c.Home2Phone, Mobile: c.MobilePhone,
		Business: c.BusinessPhone, Fax: c.BusinessFax,
		HomeStreet: c.HomeStreet, HomeCity: c.HomeCity, HomeState: c.HomeState,
		HomePostal: c.HomePostalCode, HomeCountry: c.HomeCountry,
		WorkStreet: c.BusinessStreet, WorkCity: c.BusinessCity,
		WorkState: c.BusinessState, WorkPostal: c.BusinessPostalCode,
		WorkCountry: c.BusinessCountry, Body: c.Body,
	}
}

// --- helpers ---------------------------------------------------------------

// contactInput carries the address book fields the account UI may change.
type contactInput struct {
	FolderID *int64 `json:"folderId"`

	FileAs     *string `json:"fileAs"`
	FirstName  *string `json:"firstName"`
	LastName   *string `json:"lastName"`
	MiddleName *string `json:"middleName"`

	Company    *string `json:"company"`
	Department *string `json:"department"`
	JobTitle   *string `json:"jobTitle"`

	Email1 *string `json:"email1"`
	Email2 *string `json:"email2"`
	Email3 *string `json:"email3"`

	HomePhone     *string `json:"homePhone"`
	Home2Phone    *string `json:"home2Phone"`
	MobilePhone   *string `json:"mobilePhone"`
	BusinessPhone *string `json:"businessPhone"`
	BusinessFax   *string `json:"businessFax"`

	HomeStreet     *string `json:"homeStreet"`
	HomeCity       *string `json:"homeCity"`
	HomeState      *string `json:"homeState"`
	HomePostalCode *string `json:"homePostalCode"`
	HomeCountry    *string `json:"homeCountry"`

	BusinessStreet     *string `json:"businessStreet"`
	BusinessCity       *string `json:"businessCity"`
	BusinessState      *string `json:"businessState"`
	BusinessPostalCode *string `json:"businessPostalCode"`
	BusinessCountry    *string `json:"businessCountry"`

	Body *string `json:"body"`
}

func (h *User) createContact(w http.ResponseWriter, r *http.Request, user *store.User) {
	if !h.mutable(w, r) {
		return
	}
	var in contactInput
	if !decodeJSON(w, r, &in) {
		return
	}
	folder, ok := h.contactFolder(w, user, in.FolderID)
	if !ok {
		return
	}
	c := &store.Contact{UserID: user.ID, FolderID: folder.ID}
	if err := applyContactInput(c, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !contactIdentified(c) {
		writeJSONError(w, http.StatusBadRequest, "contact needs a name, company or email")
		return
	}
	if c.FileAs == "" {
		c.FileAs = contactDisplayName(c)
	}
	id, err := h.Store.InsertContact(c)
	if err != nil {
		h.logError("cannot create contact", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot create contact")
		return
	}
	c.ID = id
	if _, err := h.Store.AddChange(folder.ID, "add", "contact", id); err != nil {
		h.logError("cannot record contact change", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"contact": contactViewOf(c)})
}

// contactFolder resolves the target address book, defaulting to the account's
// Contacts folder.
func (h *User) contactFolder(w http.ResponseWriter, user *store.User, id *int64) (*store.Folder, bool) {
	if id == nil {
		f, err := h.Store.FolderByType(user.ID, 9)
		if err != nil {
			h.logError("contacts folder is missing", err)
			writeJSONError(w, http.StatusInternalServerError, "contacts folder is missing")
			return nil, false
		}
		return f, true
	}
	f, err := h.Store.FolderByID(*id)
	if err != nil || f.UserID != user.ID || f.Deleted || f.Class != "Contacts" {
		writeJSONError(w, http.StatusNotFound, "contacts folder not found")
		return nil, false
	}
	return f, true
}

func applyContactInput(c *store.Contact, in *contactInput) error {
	if in.FileAs != nil {
		fileAs := strings.TrimSpace(*in.FileAs)
		if len([]rune(fileAs)) > 100 {
			return errors.New("display name must be at most 100 characters")
		}
		c.FileAs = fileAs
	}
	if in.FirstName != nil {
		c.FirstName = strings.TrimSpace(*in.FirstName)
	}
	if in.LastName != nil {
		c.LastName = strings.TrimSpace(*in.LastName)
	}
	if in.MiddleName != nil {
		c.MiddleName = strings.TrimSpace(*in.MiddleName)
	}
	if in.Company != nil {
		c.Company = strings.TrimSpace(*in.Company)
	}
	if in.Department != nil {
		c.Department = strings.TrimSpace(*in.Department)
	}
	if in.JobTitle != nil {
		c.JobTitle = strings.TrimSpace(*in.JobTitle)
	}
	if in.Email1 != nil {
		c.Email1 = strings.TrimSpace(*in.Email1)
	}
	if in.Email2 != nil {
		c.Email2 = strings.TrimSpace(*in.Email2)
	}
	if in.Email3 != nil {
		c.Email3 = strings.TrimSpace(*in.Email3)
	}
	if in.HomePhone != nil {
		c.HomePhone = strings.TrimSpace(*in.HomePhone)
	}
	if in.Home2Phone != nil {
		c.Home2Phone = strings.TrimSpace(*in.Home2Phone)
	}
	if in.MobilePhone != nil {
		c.MobilePhone = strings.TrimSpace(*in.MobilePhone)
	}
	if in.BusinessPhone != nil {
		c.BusinessPhone = strings.TrimSpace(*in.BusinessPhone)
	}
	if in.BusinessFax != nil {
		c.BusinessFax = strings.TrimSpace(*in.BusinessFax)
	}
	if in.HomeStreet != nil {
		c.HomeStreet = strings.TrimSpace(*in.HomeStreet)
	}
	if in.HomeCity != nil {
		c.HomeCity = strings.TrimSpace(*in.HomeCity)
	}
	if in.HomeState != nil {
		c.HomeState = strings.TrimSpace(*in.HomeState)
	}
	if in.HomePostalCode != nil {
		c.HomePostalCode = strings.TrimSpace(*in.HomePostalCode)
	}
	if in.HomeCountry != nil {
		c.HomeCountry = strings.TrimSpace(*in.HomeCountry)
	}
	if in.BusinessStreet != nil {
		c.BusinessStreet = strings.TrimSpace(*in.BusinessStreet)
	}
	if in.BusinessCity != nil {
		c.BusinessCity = strings.TrimSpace(*in.BusinessCity)
	}
	if in.BusinessState != nil {
		c.BusinessState = strings.TrimSpace(*in.BusinessState)
	}
	if in.BusinessPostalCode != nil {
		c.BusinessPostalCode = strings.TrimSpace(*in.BusinessPostalCode)
	}
	if in.BusinessCountry != nil {
		c.BusinessCountry = strings.TrimSpace(*in.BusinessCountry)
	}
	if in.Body != nil {
		c.Body = strings.TrimSpace(*in.Body)
	}
	for _, email := range []string{c.Email1, c.Email2, c.Email3} {
		if email != "" && (!strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n")) {
			return errors.New("invalid email address")
		}
	}
	return nil
}

func contactIdentified(c *store.Contact) bool {
	return c.FileAs != "" || c.FirstName != "" || c.LastName != "" ||
		c.Email1 != "" || c.Company != ""
}

func contactDisplayName(c *store.Contact) string {
	first, last := c.FirstName, c.LastName
	if first != "" || last != "" {
		// Chinese names read family-name first and carry no separator.
		if isCJK(first) && isCJK(last) {
			return last + first
		}
		return strings.TrimSpace(first + " " + last)
	}
	if c.Email1 != "" {
		return c.Email1
	}
	return "未命名"
}

func isCJK(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 0x2e80 {
			return false
		}
	}
	return true
}

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
