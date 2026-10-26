package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/outbound"
	"remoon.net/lifemail/internal/store"
)

// replyInput is the body of POST /api/user/mail/reply.
type replyInput struct {
	// MessageID is the message being replied to (or the latest one in the
	// conversation).
	MessageID int64  `json:"messageId"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	ReplyAll  bool   `json:"replyAll"`
	// To and Cc override the computed recipients when the user edited them in
	// the reader. Leaving them empty keeps the Reply / Reply All defaults.
	To []string `json:"to"`
	Cc []string `json:"cc"`
}

// replyTarget is the default reply offered at the bottom of a conversation:
// who it goes to and with which subject.
type replyTarget struct {
	MessageID int64    `json:"messageId"`
	Subject   string   `json:"subject"`
	To        []string `json:"to"`
	Cc        []string `json:"cc"`
}

// reply sends a plain-text reply, stores a copy in Sent and delivers it.
func (h *User) reply(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.mutable(w, r) {
		return
	}
	if user.Address == "" {
		writeJSONError(w, http.StatusBadRequest, "account has no primary address")
		return
	}
	var in replyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		writeJSONError(w, http.StatusBadRequest, "reply body is empty")
		return
	}

	original, err := h.Store.MessageMetaByID(in.MessageID)
	if err != nil || original.UserID != user.ID || original.Deleted {
		writeJSONError(w, http.StatusNotFound, "message not found")
		return
	}
	raw, err := h.Store.LoadRaw(original)
	if err != nil {
		h.logError("cannot read original message", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot read original message")
		return
	}
	parsed, parseErr := mailparse.Parse(raw)
	if parsed == nil {
		if parseErr != nil {
			h.logError("cannot parse original message", parseErr)
		}
		writeJSONError(w, http.StatusUnprocessableEntity, "cannot parse original message")
		return
	}
	// Keep the thread intact even when the MIME source lacks a Message-ID but
	// the index has one.
	if strings.TrimSpace(parsed.MessageID) == "" {
		parsed.MessageID = strings.Trim(original.InternetMessageID, "<> ")
	}

	to, cc := h.replyRecipients(user, parsed, original, in.ReplyAll)
	if len(in.To) > 0 || len(in.Cc) > 0 {
		to, cc = h.explicitRecipients(user, in.To, in.Cc)
		if len(to) == 0 {
			writeJSONError(w, http.StatusBadRequest, "no recipient left after removing your own address")
			return
		}
	}
	if len(to) == 0 {
		writeJSONError(w, http.StatusBadRequest, "original message has no reply address")
		return
	}
	if len(to)+len(cc) > maxReplyRecipients {
		writeJSONError(w, http.StatusBadRequest, "too many recipients")
		return
	}
	for _, addr := range append(append([]string{}, to...), cc...) {
		if !strings.Contains(addr, "@") {
			writeJSONError(w, http.StatusBadRequest, "invalid recipient: "+addr)
			return
		}
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		subject = replySubject(parsed.Subject)
		if strings.TrimSpace(subject) == "Re:" {
			subject = replySubject(original.Subject)
		}
	}
	out, messageID, err := buildMessage(user, to, cc, subject, in.Body,
		parsed.MessageID, replyReferences(parsed))
	if err != nil {
		h.logError("cannot build reply", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot build reply")
		return
	}

	sent, err := mailparse.Parse(out)
	if sent == nil {
		if err != nil {
			h.logError("cannot parse reply", err)
		}
		writeJSONError(w, http.StatusInternalServerError, "cannot parse reply")
		return
	}
	if _, err := deliver.ToUser(h.Store, user, deliver.FolderSent, sent, out); err != nil {
		h.logError("cannot store sent reply", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot store sent reply")
		return
	}

	queued, err := h.deliverReply(user, sent, out, append(append([]string{}, to...), cc...))
	if err != nil {
		// The Sent copy exists, so the user can retry from the client if they
		// want; report the failure instead of pretending it went out.
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	if h.Log != nil {
		h.Log.Info("webmail reply sent", "user", user.Name, "to", strings.Join(to, ","),
			"cc", strings.Join(cc, ","), "message_id", messageID, "queued", queued)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued})
}

// replyRecipients works out Reply vs Reply All, ignoring the account's own
// addresses (primary and aliases). The indexed store columns are used when the
// MIME source carries no usable address headers.
func (h *User) replyRecipients(user *store.User, original *mailparse.Message, meta *store.Message,
	all bool) (to, cc []string) {
	own := h.ownAddresses(user)
	sender := strings.TrimSpace(original.FromAddr)
	if sender == "" {
		sender = strings.TrimSpace(meta.FromAddr)
	}
	if sender != "" && !own[strings.ToLower(sender)] {
		to = append(to, sender)
	}
	if !all {
		return to, nil
	}
	toList := mailparse.Addresses(original.To)
	if len(toList) == 0 {
		toList = mailparse.Addresses(meta.ToAddr)
	}
	ccList := mailparse.Addresses(original.Cc)
	if len(ccList) == 0 {
		ccList = mailparse.Addresses(meta.CcAddr)
	}
	for _, addr := range toList {
		addr = bareAddress(addr)
		if addr == "" || own[strings.ToLower(addr)] || containsFold(to, addr) {
			continue
		}
		to = append(to, addr)
	}
	for _, addr := range ccList {
		addr = bareAddress(addr)
		if addr == "" || own[strings.ToLower(addr)] || containsFold(to, addr) || containsFold(cc, addr) {
			continue
		}
		cc = append(cc, addr)
	}
	return to, cc
}

// bareAddress strips a display name or angle brackets from a header value so
// recipients are stored and relayed as plain user@host addresses.
func bareAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := mail.ParseAddress(value); err == nil {
		return parsed.Address
	}
	return strings.Trim(value, "<> ")
}

// maxReplyRecipients caps a single reply so a crafted request cannot turn the
// reader into an open relay.
const maxReplyRecipients = 50

// explicitRecipients sanitises the recipient lists the user edited in the
// reader: own addresses and duplicates are dropped, To wins over Cc.
func (h *User) explicitRecipients(user *store.User, toList, ccList []string) (to, cc []string) {
	own := h.ownAddresses(user)
	collect := func(values, other []string) []string {
		var out []string
		for _, value := range values {
			addr := bareAddress(value)
			if addr == "" || own[strings.ToLower(addr)] ||
				containsFold(out, addr) || containsFold(other, addr) {
				continue
			}
			out = append(out, addr)
		}
		return out
	}
	to = collect(toList, nil)
	cc = collect(ccList, to)
	return to, cc
}

// replyTargetFor picks the message a reader reply addresses (the newest
// inbound one) and derives its default recipients.
func (h *User) replyTargetFor(user *store.User, messages []*store.Message, sentFolderID int64) *replyTarget {
	var target *store.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].UserID != user.ID {
			continue
		}
		if messages[i].FolderID != sentFolderID {
			target = messages[i]
			break
		}
		if target == nil {
			target = messages[i]
		}
	}
	if target == nil {
		return nil
	}
	to, cc := h.recipientsFromMeta(user, target)
	if len(to) == 0 {
		return nil
	}
	if cc == nil {
		cc = []string{}
	}
	return &replyTarget{
		MessageID: target.ID,
		Subject:   replySubject(target.Subject),
		To:        to,
		Cc:        cc,
	}
}

// recipientsFromMeta derives recipients from the indexed header columns, so
// the reader can show them without loading the MIME source.
func (h *User) recipientsFromMeta(user *store.User, m *store.Message) (to, cc []string) {
	if m == nil {
		return nil, nil
	}
	own := h.ownAddresses(user)
	if sender := bareAddress(m.FromAddr); sender != "" && !own[strings.ToLower(sender)] {
		to = append(to, sender)
	}
	for _, addr := range mailparse.Addresses(m.ToAddr) {
		addr = bareAddress(addr)
		if addr == "" || own[strings.ToLower(addr)] || containsFold(to, addr) {
			continue
		}
		to = append(to, addr)
	}
	for _, addr := range mailparse.Addresses(m.CcAddr) {
		addr = bareAddress(addr)
		if addr == "" || own[strings.ToLower(addr)] || containsFold(to, addr) || containsFold(cc, addr) {
			continue
		}
		cc = append(cc, addr)
	}
	return to, cc
}

func (h *User) ownAddresses(user *store.User) map[string]bool {
	out := make(map[string]bool, 4)
	if user.Address != "" {
		out[strings.ToLower(user.Address)] = true
	}
	if aliases, err := h.Store.AliasesByUser(user.ID); err == nil {
		for _, alias := range aliases {
			out[strings.ToLower(alias.Address)] = true
		}
	}
	return out
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func replySubject(subject string) string {
	trimmed := strings.TrimSpace(subject)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"re:", "re：", "回复:", "回复：", "aw:", "sv:"} {
		if strings.HasPrefix(lower, prefix) {
			return trimmed
		}
	}
	if trimmed == "" {
		return "Re:"
	}
	return "Re: " + trimmed
}

// buildMessage renders a plain-text message. Threading headers are written only
// when inReplyTo is set, so the same builder serves replies and new mail.
func buildMessage(user *store.User, to, cc []string, subject, body, inReplyTo string,
	references []string) ([]byte, string, error) {
	if user.Address == "" {
		return nil, "", errors.New("account has no primary address")
	}
	messageID := store.NewUUID() + "@lifemail"
	from := (&mail.Address{Name: user.DisplayName, Address: user.Address}).String()
	if from == "" {
		return nil, "", errors.New("cannot format sender")
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s>\r\n", messageID)
	if trimmed := strings.Trim(inReplyTo, "<> "); trimmed != "" {
		fmt.Fprintf(&b, "In-Reply-To: <%s>\r\n", trimmed)
	}
	if len(references) > 0 {
		fmt.Fprintf(&b, "References: %s\r\n", strings.Join(references, " "))
	}
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(&b, "\r\n")
	writeBase64(&b, body)

	return b.Bytes(), messageID, nil
}

// replyReferences keeps the thread root and the recent history, capped so the
// header cannot grow without bound.
func replyReferences(original *mailparse.Message) []string {
	ids := make([]string, 0, len(original.References)+1)
	seen := make(map[string]bool, len(original.References)+1)
	add := func(id string) {
		id = strings.Trim(id, "<> ")
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, "<"+id+">")
	}
	for _, id := range original.References {
		add(id)
	}
	add(original.MessageID)
	if len(ids) > 10 {
		ids = ids[len(ids)-10:]
	}
	return ids
}

func writeBase64(dst *bytes.Buffer, value string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	for len(encoded) > 76 {
		dst.WriteString(encoded[:76])
		dst.WriteString("\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		dst.WriteString(encoded)
		dst.WriteString("\r\n")
	}
}

// deliverReply stores the message for local recipients and relays the rest.
func (h *User) deliverReply(user *store.User, parsed *mailparse.Message, raw []byte, rcpts []string) (bool, error) {
	var external []string
	for _, rcpt := range rcpts {
		owner, err := h.Store.UserByAddress(rcpt)
		if err == nil {
			if _, err := deliver.ToUser(h.Store, owner, deliver.FolderInbox, parsed, raw); err != nil {
				return false, fmt.Errorf("cannot deliver to %s", rcpt)
			}
			continue
		}
		if h.IsLocalDomain != nil && h.IsLocalDomain(mailparse.Domain(rcpt)) {
			return false, fmt.Errorf("unknown local recipient: %s", rcpt)
		}
		external = append(external, rcpt)
	}
	if len(external) == 0 {
		return false, nil
	}
	if h.Sender == nil || !h.Sender.Enabled() {
		return false, errors.New("outbound SMTP is not configured")
	}
	if err := h.Sender.Send(context.Background(), user.Address, external, raw); err != nil {
		if h.Log != nil {
			h.Log.Warn("reply delivery failed; queueing", "err", err,
				"recipients", strings.Join(external, ","))
		}
		if _, qerr := outbound.Enqueue(h.Store, user.Address, external, raw); qerr != nil {
			return false, errors.New("cannot queue outbound mail")
		}
		return true, nil
	}
	return false, nil
}
