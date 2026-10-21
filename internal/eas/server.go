// Package eas implements the Exchange ActiveSync HTTP protocol.
package eas

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

const (
	maxRequestBytes = 32 << 20

	supportedVersions = "12.1,14.0,14.1,16.0,16.1"
	// Only commands that dispatch actually implements are advertised: a client
	// that sees GetHierarchy/ValidateCert here will call them and get an error.
	supportedCommands = "Sync,SendMail,SmartForward,SmartReply,GetAttachment," +
		"FolderSync,FolderCreate,FolderDelete,FolderUpdate,MoveItems," +
		"GetItemEstimate,MeetingResponse,Search,Settings,Ping,ItemOperations," +
		"Provision,ResolveRecipients"
)

// Server handles /Microsoft-Server-ActiveSync requests.
type Server struct {
	Store  *store.Store
	Sender *smtp.Sender
	Log    *slog.Logger

	// IsLocalDomain reports whether a domain is hosted here. Mail to such a
	// domain is never relayed; unknown mailboxes are rejected.
	IsLocalDomain func(domain string) bool

	// AuthLimiter throttles repeated bad passwords. Optional.
	AuthLimiter *authlimit.Limiter

	// DumpDir, when set, receives the raw WBXML of every request so a client
	// problem can be reproduced offline.
	DumpDir string

	authMu    sync.Mutex
	authCache map[string]authCacheEntry

	// pingMu guards the Ping bookkeeping: pingNotified throttles repeated
	// change notifications and pingWait holds the in-flight long poll per
	// device.
	pingMu       sync.Mutex
	pingNotified map[string]pingNotifyEntry
	pingWait     map[string]chan struct{}
}

// authCacheTTL bounds how long a verified password is reused. Entries are also
// invalidated when the stored password hash changes, so resets take effect at
// once.
const authCacheTTL = 5 * time.Minute

type authCacheEntry struct {
	digest  [32]byte
	hash    string
	expires time.Time
}

// checkPassword verifies a password, reusing a recent successful bcrypt result
// for the same account and password.
func (s *Server) checkPassword(user *store.User, password string) bool {
	digest := sha256.Sum256([]byte(password))
	now := time.Now()

	s.authMu.Lock()
	entry, cached := s.authCache[user.ID]
	s.authMu.Unlock()
	if cached && now.Before(entry.expires) && entry.hash == user.PasswordHash &&
		subtle.ConstantTimeCompare(entry.digest[:], digest[:]) == 1 {
		return true
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return false
	}

	s.authMu.Lock()
	if s.authCache == nil {
		s.authCache = make(map[string]authCacheEntry)
	}
	if len(s.authCache) > 256 {
		for key, value := range s.authCache {
			if now.After(value.expires) || value.hash != user.PasswordHash {
				delete(s.authCache, key)
			}
		}
	}
	s.authCache[user.ID] = authCacheEntry{
		digest:  digest,
		hash:    user.PasswordHash,
		expires: now.Add(authCacheTTL),
	}
	s.authMu.Unlock()
	return true
}

// ServeHTTP implements the EAS endpoint.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeOptions(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("Cmd") == "Options" {
		writeOptions(w)
		return
	}

	query := r.URL.Query()
	cmd := query.Get("Cmd")
	deviceID := query.Get("DeviceId")
	protocol := r.Header.Get("MS-ASProtocolVersion")

	email, password, ok := basicAuth(r)
	if !ok {
		s.Log.Warn("eas unauthorized", "reason", "missing credentials",
			"cmd", cmd, "device", deviceID, "ua", r.UserAgent())
		unauthorized(w)
		return
	}
	ip := httpx.ClientIP(r)
	limitKey := "eas|" + ip + "|" + strings.ToLower(email)
	if locked, retry := s.AuthLimiter.Locked(limitKey); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
		s.Log.Warn("eas unauthorized", "reason", "too many failed attempts",
			"user", email, "ip", ip, "cmd", cmd, "device", deviceID, "ua", r.UserAgent())
		unauthorized(w)
		return
	}
	user, err := s.Store.UserByLogin(email)
	if err != nil {
		s.AuthLimiter.Fail(limitKey)
		s.Log.Warn("eas unauthorized", "reason", "unknown user",
			"user", email, "ip", ip, "cmd", cmd, "device", deviceID, "ua", r.UserAgent())
		unauthorized(w)
		return
	}
	if !s.checkPassword(user, password) {
		s.AuthLimiter.Fail(limitKey)
		s.Log.Warn("eas unauthorized", "reason", "wrong password",
			"user", email, "ip", ip, "cmd", cmd, "device", deviceID, "ua", r.UserAgent())
		unauthorized(w)
		return
	}
	s.AuthLimiter.Reset(limitKey)

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		s.Log.Warn("eas cannot read body", "err", err, "user", user.Name, "cmd", cmd)
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	s.Log.Debug("eas request", "user", user.Name, "cmd", cmd, "device", deviceID,
		"protocol", protocol, "content_type", r.Header.Get("Content-Type"),
		"bytes", len(body), "ua", r.UserAgent())
	// GetAttachment carries no WBXML body: the attachment is named in the URL
	// and the answer is the raw bytes, so it has to be handled before decoding.
	if cmd == "GetAttachment" {
		s.serveGetAttachment(w, user, query.Get("AttachmentName"))
		return
	}
	doc, err := wbxml.Decode(body, codec)
	if err != nil {
		s.Log.Warn("eas wbxml decode failed", "err", err, "user", user.Name, "cmd", cmd,
			"device", deviceID, "protocol", protocol,
			"content_type", r.Header.Get("Content-Type"), "bytes", len(body),
			"head", hexHead(body, 32), "ua", r.UserAgent())
		if path, err := dumpRequestBody(s.DumpDir, body, cmd); err == nil {
			s.Log.Warn("eas request body dumped", "path", path)
		} else {
			s.Log.Warn("cannot dump request body", "err", err)
		}
		http.Error(w, "invalid wbxml: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.Store.RecordDevice(user.ID, deviceID, query.Get("DeviceType"), protocol, ip); err != nil {
		s.Log.Warn("cannot record device", "err", err, "device", deviceID)
	}

	if cmd == "SendMail" || cmd == "SmartReply" || cmd == "SmartForward" || cmd == "MoveItems" || cmd == "Search" {
		s.dumpBody(body, cmd)
	}
	// Sync uploads (client Add/Change/Delete) are the hardest part to diagnose
	// from logs alone, so keep the exact bytes when dumping is enabled.
	if cmd == "Sync" && containsElement(doc, "Commands") {
		s.dumpBody(body, "SyncCommands")
	}

	// A client that only allows one EAS operation at a time would otherwise
	// wait for the long poll to expire before its upload is processed.
	if cmd != "Ping" && deviceID != "" {
		s.cancelPing(user.ID + "|" + deviceID)
	}

	resp, err := s.dispatch(r.Context(), user, deviceID, cmd, doc)
	if err != nil {
		s.Log.Error("eas command failed", "cmd", cmd, "user", user.Name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// EAS 14 changed SendMail semantics: success is a 200 with no body. Clients
	// treat any status document as a failure, so failures still send one.
	if emptySendResponse(cmd, protocol) && resp.ChildText("Status") == "1" {
		h := w.Header()
		h.Set("MS-Server-ActiveSync", "14.1")
		h.Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}
	out, err := wbxml.Encode(resp, codec)
	if err != nil {
		s.Log.Error("cannot encode response", "cmd", cmd, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	status := ""
	if child := resp.Child("Status"); child != nil {
		status = child.Text
	}
	s.Log.Debug("eas response", "cmd", cmd, "status", status, "bytes", len(out),
		"head", hexHead(out, 32))
	h := w.Header()
	h.Set("Content-Type", "application/vnd.ms-sync.wbxml")
	h.Set("MS-Server-ActiveSync", "14.1")
	h.Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// serveGetAttachment answers the URL-based attachment download that older
// clients use instead of an ItemOperations Fetch.
func (s *Server) serveGetAttachment(w http.ResponseWriter, user *store.User, ref string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		http.Error(w, "attachment not specified", http.StatusBadRequest)
		return
	}
	msgID, attID := parseAttachmentRef(ref)
	if msgID == 0 || attID == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	msg, err := s.Store.MessageByID(msgID)
	if err != nil || msg.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	att, err := s.Store.AttachmentByID(attID)
	if err != nil || att.MessageID != msgID || len(att.Data) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	contentType := att.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(att.Data)))
	h.Set("MS-Server-ActiveSync", "14.1")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(att.Data)
}

// emptySendResponse reports whether the command succeeds with an empty body.// From EAS 14 on, SendMail/SmartReply/SmartForward have no response document
// when the message is accepted.
func emptySendResponse(cmd, protocol string) bool {
	switch cmd {
	case "SendMail", "SmartReply", "SmartForward":
	default:
		return false
	}
	major := 0
	if i := strings.IndexByte(protocol, '.'); i > 0 {
		major, _ = strconv.Atoi(protocol[:i])
	} else {
		major, _ = strconv.Atoi(protocol)
	}
	return major >= 14
}

func writeOptions(w http.ResponseWriter) {
	h := w.Header()
	h.Set("MS-ASProtocolVersions", supportedVersions)
	h.Set("MS-ASProtocolCommands", supportedCommands)
	h.Set("MS-Server-ActiveSync", "14.1")
	h.Set("Allow", "OPTIONS, POST")
	w.WriteHeader(http.StatusOK)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="lifemail"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// hexHead renders the first n bytes of a request body so WBXML decode
// failures can be diagnosed from the log.
func hexHead(body []byte, n int) string {
	if len(body) > n {
		body = body[:n]
	}
	return fmt.Sprintf("%x", body)
}

// containsElement reports whether the tree holds an element with the given
// name at any depth.
func containsElement(n *wbxml.Node, name string) bool {
	if n == nil {
		return false
	}
	if n.Name == name {
		return true
	}
	for _, child := range n.Children {
		if containsElement(child, name) {
			return true
		}
	}
	return false
}

// dumpBody stores a request body when LIFEMAIL_EAS_DUMP is set and reports
// the outcome, so a broken dump directory is not silently ignored.
func (s *Server) dumpBody(body []byte, cmd string) {
	path, err := dumpRequestBody(s.DumpDir, body, cmd)
	if err != nil {
		s.Log.Warn("cannot dump request body", "err", err, "cmd", cmd)
		return
	}
	s.Log.Debug("eas request body dumped", "path", path, "cmd", cmd)
}

// dumpRequestBody writes a client body into dir, when set, so its exact bytes
// can be inspected offline.
func dumpRequestBody(dir string, body []byte, cmd string) (string, error) {
	if dir == "" || len(body) == 0 {
		return "", nil
	}
	name := cmd
	if name == "" {
		name = "unknown"
	}
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%s.wbxml", time.Now().UnixNano(), name))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func basicAuth(r *http.Request) (string, string, bool) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return "", "", false
	}
	// Clients may send DOMAIN\user; the domain says nothing about the account,
	// which is looked up by its bare login name.
	if i := strings.LastIndexAny(user, `\/`); i >= 0 {
		user = user[i+1:]
	}
	return user, pass, true
}

func (s *Server) dispatch(ctx context.Context, user *store.User, deviceID, cmd string, doc *wbxml.Node) (*wbxml.Node, error) {
	switch cmd {
	case "Provision":
		return s.cmdProvision(user, deviceID, doc)
	case "FolderSync":
		return s.cmdFolderSync(user, doc)
	case "FolderCreate":
		return s.cmdFolderCreate(user, doc)
	case "FolderDelete":
		return s.cmdFolderDelete(user, doc)
	case "FolderUpdate":
		return s.cmdFolderUpdate(user, doc)
	case "Sync":
		return s.cmdSync(user, deviceID, doc)
	case "Ping":
		return s.cmdPing(ctx, user, deviceID, doc)
	case "GetItemEstimate":
		return s.cmdGetItemEstimate(user, deviceID, doc)
	case "MoveItems":
		return s.cmdMoveItems(user, doc)
	case "ItemOperations":
		return s.cmdItemOperations(user, doc)
	case "ComposeMail":
		return s.cmdComposeMail(user, doc)
	case "SendMail", "SmartReply", "SmartForward":
		return s.cmdSendMail(user, cmd, doc)
	case "MeetingResponse":
		return s.cmdMeetingResponse(user, doc)
	case "ResolveRecipients":
		return s.cmdResolveRecipients(user, doc)
	case "Search":
		return s.cmdSearch(user, doc)
	case "Settings":
		return s.cmdSettings(user, doc)
	case "Options":
		return wbxml.N(pageAirSync, "Options"), nil
	default:
		return nil, fmt.Errorf("unsupported command %q", cmd)
	}
}

// Int helpers shared by the command implementations.

func parseInt(value string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return n
}

func atoiDefault(value string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return def
	}
	return n
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func itoa(v int) string { return strconv.Itoa(v) }

func i64toa(v int64) string { return strconv.FormatInt(v, 10) }

var easTimeLayouts = []string{
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.0000000Z",
	"2006-01-02T15:04:05.000000Z",
	"20060102T150405Z",
	"20060102T150405",
	// Clients occasionally omit the zone or the colon in the offset; the
	// protocol says UTC, so parse those as UTC.
	"2006-01-02T15:04:05.000",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.000Z",
	"2006-01-02 15:04:05Z",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z07:00",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05.000Z0700",
	"2006-01-02T15:04:05Z0700",
}

func formatEasTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseEasTime(value string) time.Time {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, "\x00")
	if strings.HasSuffix(value, "z") {
		value = value[:len(value)-1] + "Z"
	}
	for _, layout := range easTimeLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func truncateUTF8(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
