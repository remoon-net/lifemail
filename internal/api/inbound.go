// Package api exposes the HTTP endpoint Cloudflare Email Routing workers
// forward inbound messages to.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/settings"
	"remoon.net/lifemail/internal/store"
)

// MessageSender submits a MIME message synchronously. *smtp.Sender implements
// it; tests can substitute a fake.
type MessageSender interface {
	EnvelopeFrom(userEmail string) string
	Send(ctx context.Context, from string, to []string, raw []byte) error
}

// Inbound receives raw MIME messages.
type Inbound struct {
	Store    *store.Store
	Token    string
	Log      *slog.Logger
	MaxBytes int64
	// Settings supplies the per-domain catch-all destinations. Optional.
	Settings *settings.Store
	// Sender forwards catch-all mail synchronously. Optional; without it an
	// unknown recipient is rejected instead of forwarded.
	Sender MessageSender
}

type delivered struct {
	To        string `json:"to"`
	MessageID int64  `json:"message_id"`
}

type skipped struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type forwarded struct {
	To          string `json:"to"`
	Destination string `json:"destination"`
}

type response struct {
	Delivered []delivered `json:"delivered"`
	Forwarded []forwarded `json:"forwarded,omitempty"`
	Skipped   []skipped   `json:"skipped,omitempty"`
}

type jsonRequest struct {
	To  []string `json:"to"`
	Raw string   `json:"raw"` // base64 encoded MIME
}

// ServeHTTP implements POST /api/inbound.
func (h *Inbound) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	limit := h.MaxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	var envelope []string
	raw := body
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var req jsonRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Raw == "" {
			http.Error(w, "raw is required", http.StatusBadRequest)
			return
		}
		raw, err = base64.StdEncoding.DecodeString(req.Raw)
		if err != nil {
			http.Error(w, "raw must be base64", http.StatusBadRequest)
			return
		}
		envelope = req.To
	}
	if len(envelope) == 0 {
		envelope = splitAddresses(r.Header.Get("X-Envelope-To"))
	}
	if len(envelope) == 0 {
		envelope = splitAddresses(r.URL.Query().Get("to"))
	}

	parsed, parseErr := mailparse.Parse(raw)
	if parsed == nil {
		parsed = &mailparse.Message{}
		if parseErr != nil {
			h.Log.Warn("cannot parse inbound mime", "err", parseErr)
		}
	}
	if len(envelope) == 0 {
		envelope = append(envelope, mailparse.Addresses(parsed.To)...)
		envelope = append(envelope, mailparse.Addresses(parsed.Cc)...)
	}
	if len(envelope) == 0 {
		http.Error(w, "no recipients", http.StatusBadRequest)
		return
	}

	// Resolve every recipient before storing anything. A message that cannot be
	// routed must not partially land in some inboxes and then fail, because the
	// worker keeps retrying and the other recipients would see duplicates.
	catchAll := map[string]string{}
	if h.Settings != nil {
		catchAll = h.Settings.Snapshot().CatchAll
	}
	type target struct {
		address string
		user    *store.User
		forward string
	}
	var targets []target
	var unknown []string
	var resolveFailed bool
	seen := make(map[string]bool)
	for _, recipient := range envelope {
		addr := normalizeAddress(recipient)
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true

		user, err := h.lookupUser(addr)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				h.Log.Error("cannot resolve inbound recipient", "err", err, "to", addr)
				resolveFailed = true
				continue
			}
			if dest := catchAll[domainOf(addr)]; dest != "" {
				targets = append(targets, target{address: addr, forward: dest})
				continue
			}
			unknown = append(unknown, addr)
			continue
		}
		targets = append(targets, target{address: addr, user: user})
	}

	if len(unknown) > 0 {
		// 550 tells the Cloudflare worker this is permanent: retrying the same
		// envelope will never succeed, so the sender should get a bounce.
		h.Log.Warn("inbound recipient unknown and no catch-all configured",
			"recipients", strings.Join(unknown, ","))
		writeInboundError(w, 550, "unknown recipient", map[string]any{"unknown": unknown})
		return
	}
	if resolveFailed {
		writeInboundError(w, http.StatusInternalServerError, "cannot resolve recipient", nil)
		return
	}

	var resp response
	var storageFailed, forwardFailed bool
	for _, t := range targets {
		addr := t.address

		if t.forward != "" {
			if h.Sender == nil {
				h.Log.Error("catch-all forwarding is not configured", "to", addr)
				resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "forwarding not configured"})
				forwardFailed = true
				continue
			}
			// The catch-all address belongs to one of our domains, so it can be
			// the envelope sender. Forwarding is synchronous by design: if the
			// relay rejects the message the request fails and Cloudflare retries.
			from := h.Sender.EnvelopeFrom(addr)
			if err := h.Sender.Send(r.Context(), from, []string{t.forward}, raw); err != nil {
				h.Log.Error("cannot forward inbound message",
					"to", addr, "destination", t.forward, "err", err)
				resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "forward failed"})
				forwardFailed = true
				continue
			}
			h.Log.Info("inbound message forwarded", "to", addr, "destination", t.forward)
			resp.Forwarded = append(resp.Forwarded, forwarded{To: addr, Destination: t.forward})
			continue
		}

		user := t.user
		inbox, err := h.Store.FolderByType(user.ID, 2)
		if err != nil {
			resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "no inbox"})
			storageFailed = true
			continue
		}
		duplicate, err := h.Store.MessageIDExists(user.ID, parsed.MessageID)
		if err != nil {
			resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "storage error"})
			storageFailed = true
			continue
		}
		if duplicate {
			resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "duplicate"})
			continue
		}

		m := &store.Message{
			UserID:            user.ID,
			FolderID:          inbox.ID,
			Subject:           parsed.Subject,
			FromAddr:          parsed.FromAddr,
			FromName:          parsed.FromName,
			ToAddr:            parsed.To,
			CcAddr:            parsed.Cc,
			DateReceived:      time.Now().Unix(),
			Size:              int64(len(raw)),
			Importance:        1,
			MessageClass:      "IPM.Note",
			SenderAddr:        parsed.SenderAddr,
			SenderName:        parsed.SenderName,
			ReceivedAsBcc:     receivedAsBcc(parsed, addr),
			ConversationID:    deliver.ConversationID(h.Store, user, parsed),
			InternetMessageID: parsed.MessageID,
			BodyText:          parsed.Text,
			BodyHTML:          parsed.HTML,
			Raw:               raw,
			HasAttachments:    len(parsed.Parts) > 0,
		}
		id, err := h.Store.InsertMessage(m)
		if err != nil {
			h.Log.Error("cannot store inbound message", "err", err, "to", addr)
			resp.Skipped = append(resp.Skipped, skipped{To: addr, Reason: "storage error"})
			storageFailed = true
			continue
		}
		for _, part := range parsed.Parts {
			if _, err := h.Store.InsertAttachment(&store.Attachment{
				MessageID:   id,
				FileName:    part.FileName,
				ContentType: part.ContentType,
				ContentID:   part.ContentID,
				IsInline:    part.Inline,
				Data:        part.Data,
			}); err != nil {
				h.Log.Warn("cannot store attachment", "err", err, "message", id)
			}
		}
		if _, err := h.Store.AddChange(inbox.ID, "add", "email", id); err != nil {
			h.Log.Error("cannot record change", "err", err, "message", id)
		}
		// The inbound API is a delivery path of its own, so the same hook that
		// runs for SMTP deliveries has to run here too.
		deliver.NotifyInbox(user, parsed, raw)
		resp.Delivered = append(resp.Delivered, delivered{To: addr, MessageID: id})
	}

	status := http.StatusOK
	switch {
	case storageFailed:
		// Local delivery is idempotent (Message-ID check), so a retry is safe.
		status = http.StatusInternalServerError
	case forwardFailed:
		status = http.StatusBadGateway
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeInboundError(w http.ResponseWriter, status int, message string, extra map[string]any) {
	body := map[string]any{"error": message}
	for key, value := range extra {
		body[key] = value
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *Inbound) authorized(r *http.Request) bool {
	return tokenMatches(requestToken(r), h.Token)
}

func requestToken(r *http.Request) string {
	if token := r.Header.Get("X-Lifemail-Token"); token != "" {
		return token
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func tokenMatches(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (h *Inbound) lookupUser(addr string) (*store.User, error) {
	return h.Store.UserByAddress(addr)
}

func splitAddresses(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if addr := normalizeAddress(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func normalizeAddress(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.LastIndex(value, "<"); i >= 0 {
		if j := strings.Index(value[i:], ">"); j > 0 {
			value = value[i+1 : i+j]
		}
	}
	return strings.ToLower(strings.Trim(value, "<> \t\r\n"))
}

// domainOf returns the domain part of a normalized address.
func domainOf(addr string) string {
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		return addr[at+1:]
	}
	return ""
}

// receivedAsBcc reports whether the recipient is absent from both the To and Cc
// headers, which means the message arrived through Bcc.
func receivedAsBcc(parsed *mailparse.Message, addr string) bool {
	if parsed == nil || addr == "" {
		return false
	}
	for _, list := range []string{parsed.To, parsed.Cc} {
		for _, candidate := range mailparse.Addresses(list) {
			if strings.EqualFold(candidate, addr) {
				return false
			}
		}
	}
	return true
}
