package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/settings"
	"remoon.net/lifemail/internal/store"
)

const (
	adminCookieName = "lifemail_admin"
	adminSessionTTL = 12 * time.Hour
)

// Admin exposes the browser UI session endpoints and runtime settings.
// Requests may authenticate with the Bearer token or with the session cookie
// issued by POST /api/admin/login.
type Admin struct {
	Settings *settings.Store
	Store    *store.Store
	Token    string
	Version  string
	Log      *slog.Logger

	// AuthLimiter throttles repeated bad tokens. Optional.
	AuthLimiter *authlimit.Limiter

	// BaseURL is prepended to calendar subscription paths.
	BaseURL string
}

type adminSMTPView struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	From        string `json:"from"`
}

type adminDomain struct {
	Name    string `json:"name"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`
}

// UnmarshalJSON accepts both "example.com" and {"name":"example.com"} so
// older clients keep working.
func (d *adminDomain) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var name string
		if err := json.Unmarshal(data, &name); err != nil {
			return err
		}
		d.Name = name
		return nil
	}
	type plain adminDomain
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*d = adminDomain(p)
	return nil
}

type adminView struct {
	Domains []adminDomain `json:"domains"`
	SMTP    adminSMTPView `json:"smtp"`
}

type adminSMTPPatch struct {
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	From     *string `json:"from"`
}

type adminPatch struct {
	Domains *[]adminDomain  `json:"domains"`
	SMTP    *adminSMTPPatch `json:"smtp"`
}

// ServeHTTP routes /api/admin/* requests.
func (h *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	switch r.URL.Path {
	case "/api/admin/login":
		h.login(w, r)
	case "/api/admin/logout":
		h.logout(w, r)
	case "/api/admin/session":
		h.session(w, r)
	case "/api/admin/settings":
		h.settings(w, r)
	case "/api/admin/stats":
		h.stats(w, r)
	case "/api/admin/migrations":
		h.migrations(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/admin/users") {
			h.users(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/aliases") {
			h.aliases(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/outbox") {
			h.outbox(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/devices") {
			h.devices(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/maintenance") {
			h.maintenance(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/calendars") {
			h.calendars(w, r)
			return
		}
		http.NotFound(w, r)
	}
}

func (h *Admin) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.Token == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "admin token is not configured")
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
	limitKey := "admin|" + ip
	if locked, retry := h.AuthLimiter.Locked(limitKey); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
		if h.Log != nil {
			h.Log.Warn("admin login throttled", "ip", ip, "retry_after", retry.Round(time.Second))
		}
		writeJSONError(w, http.StatusTooManyRequests, "too many failed login attempts, try again later")
		return
	}
	defer r.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if !tokenMatches(body.Token, h.Token) {
		if h.AuthLimiter.Fail(limitKey) && h.Log != nil {
			h.Log.Warn("admin login locked", "ip", ip)
		}
		time.Sleep(200 * time.Millisecond)
		writeJSONError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	h.AuthLimiter.Reset(limitKey)
	h.issueSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"version":       h.version(),
	})
}

func (h *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Admin) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": h.authorized(r),
		"version":       h.version(),
	})
}

func (h *Admin) settings(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.writeSettings(w, http.StatusOK, h.Settings.Snapshot())

	case http.MethodPut, http.MethodPatch:
		if !sameOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if !isJSON(r) {
			writeJSONError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
			return
		}
		defer r.Body.Close()
		var body adminPatch
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		patch := settings.Patch{}
		if body.Domains != nil {
			domains := make([]settings.Domain, 0, len(*body.Domains))
			for _, d := range *body.Domains {
				domains = append(domains, settings.Domain{
					Name:    d.Name,
					TLSCert: d.TLSCert,
					TLSKey:  d.TLSKey,
				})
			}
			patch.Domains = &domains
		}
		if body.SMTP != nil {
			patch.SMTPHost = body.SMTP.Host
			patch.SMTPPort = body.SMTP.Port
			patch.SMTPUser = body.SMTP.Username
			patch.SMTPPassword = body.SMTP.Password
			patch.SMTPFrom = body.SMTP.From
		}
		cfg, err := h.Settings.Update(patch)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if h.Log != nil {
			h.Log.Info("runtime settings updated")
		}
		h.writeSettings(w, http.StatusOK, cfg)

	default:
		w.Header().Set("Allow", "GET, PUT, PATCH")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Admin) writeSettings(w http.ResponseWriter, status int, cfg settings.Config) {
	domains := make([]adminDomain, 0, len(cfg.Domains))
	for _, d := range cfg.Domains {
		domains = append(domains, adminDomain{
			Name:    d.Name,
			TLSCert: d.TLSCert,
			TLSKey:  d.TLSKey,
		})
	}
	view := adminView{
		Domains: domains,
		SMTP: adminSMTPView{
			Host:        cfg.SMTPHost,
			Port:        cfg.SMTPPort,
			Username:    cfg.SMTPUser,
			PasswordSet: cfg.SMTPPassword != "",
			From:        cfg.SMTPFrom,
		},
	}
	writeJSON(w, status, view)
}

func (h *Admin) authorized(r *http.Request) bool {
	return tokenMatches(requestToken(r), h.Token) || h.validSession(r)
}

func (h *Admin) issueSession(w http.ResponseWriter, r *http.Request) {
	expiry := time.Now().Add(adminSessionTTL).Unix()
	payload := strconv.FormatInt(expiry, 10)
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    payload + "." + h.sign(payload),
		Path:     "/",
		MaxAge:   int(adminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Admin) validSession(r *http.Request) bool {
	if h.Token == "" {
		return false
	}
	cookie, err := r.Cookie(adminCookieName)
	if err != nil {
		return false
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || expiry < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(signature), []byte(h.sign(payload)))
}

func (h *Admin) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(h.Token))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Admin) version() string {
	if h.Version == "" {
		return "dev"
	}
	return h.Version
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func isJSON(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return strings.EqualFold(parsed.Host, host)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
