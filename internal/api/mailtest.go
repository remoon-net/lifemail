package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type smtpTestRequest struct {
	To string `json:"to"`
}

// testSMTP sends a probe message through the saved SMTP settings so the console
// can prove the outbound path works end to end. It always uses the persisted
// configuration (not values still sitting in the form), and it does not touch
// the outbox: the result is returned synchronously.
func (h *Admin) testSMTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
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
	defer r.Body.Close()
	var body smtpTestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	to, err := cleanEmail(body.To)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.Sender == nil || h.Settings == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "smtp is not configured")
		return
	}
	cfg := h.Settings.Snapshot()
	if cfg.SMTPHost == "" || cfg.SMTPPassword == "" {
		writeJSONError(w, http.StatusConflict, "先保存 SMTP 主机与 API Token")
		return
	}
	if cfg.SMTPFrom == "" {
		// Cloudflare rejects MAIL FROM addresses outside the onboarded domains;
		// the fallback sender is what keeps that valid.
		writeJSONError(w, http.StatusConflict, "先保存兜底 From 地址")
		return
	}

	// The console should not wait forever if the relay is unreachable.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.Sender.Send(ctx, h.Sender.EnvelopeFrom(cfg.SMTPFrom), []string{to}, smtpTestMessage(cfg.SMTPFrom, to)); err != nil {
		h.logError("smtp test failed", err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.logInfo("smtp test message sent", "to", to)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "to": to})
}

// smtpTestMessage builds a minimal ASCII-headed MIME message.
func smtpTestMessage(from, to string) []byte {
	now := time.Now()
	messageID := fmt.Sprintf("<test-%d@%s>", now.UnixNano(), domainOf(to))
	headers := "From: lifemail <" + from + ">\r\n" +
		"To: " + to + "\r\n" +
		"Subject: lifemail SMTP test\r\n" +
		"Date: " + now.Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: " + messageID + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n"
	body := "这是 lifemail 控制台发出的测试邮件。\r\n" +
		"收到它说明 SMTP 设置可用。\r\n"
	return []byte(headers + body)
}
