package api

import (
	"net/http"
	"strings"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

// composeInput is the body of POST /api/user/mail/send.
type composeInput struct {
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Bcc     []string `json:"bcc"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
}

// maxComposeSubject caps the subject so a crafted request cannot blow up the
// stored message.
const maxComposeSubject = 500

// send starts a new message: it writes a copy to Sent and delivers it through
// the same path replies use (local delivery, external SMTP, outbound queue).
func (h *User) send(w http.ResponseWriter, r *http.Request, user *store.User) {
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
	var in composeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		writeJSONError(w, http.StatusBadRequest, "message body is empty")
		return
	}
	subject := strings.TrimSpace(in.Subject)
	if len(subject) > maxComposeSubject {
		subject = subject[:maxComposeSubject]
	}

	to, cc := h.explicitRecipients(user, in.To, in.Cc)
	bcc, _ := h.explicitRecipients(user, in.Bcc, nil)
	if len(to) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no recipient left after removing your own address")
		return
	}
	rcpts := make([]string, 0, len(to)+len(cc)+len(bcc))
	for _, list := range [][]string{to, cc, bcc} {
		for _, addr := range list {
			if !strings.Contains(addr, "@") {
				writeJSONError(w, http.StatusBadRequest, "invalid recipient: "+addr)
				return
			}
			if !containsFold(rcpts, addr) {
				rcpts = append(rcpts, addr)
			}
		}
	}
	if len(rcpts) > maxReplyRecipients {
		writeJSONError(w, http.StatusBadRequest, "too many recipients")
		return
	}

	out, messageID, err := buildMessage(user, to, cc, subject, body, "", nil)
	if err != nil {
		h.logError("cannot build message", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot build message")
		return
	}
	sent, err := mailparse.Parse(out)
	if sent == nil {
		if err != nil {
			h.logError("cannot parse message", err)
		}
		writeJSONError(w, http.StatusInternalServerError, "cannot parse message")
		return
	}
	if _, err := deliver.ToUser(h.Store, user, deliver.FolderSent, sent, out); err != nil {
		h.logError("cannot store sent message", err)
		writeJSONError(w, http.StatusInternalServerError, "cannot store sent message")
		return
	}

	queued, err := h.deliverReply(user, sent, out, rcpts)
	if err != nil {
		// The Sent copy exists, so retrying from a mail client still works.
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	if h.Log != nil {
		h.Log.Info("webmail message sent", "user", user.Name, "to", strings.Join(to, ","),
			"cc", strings.Join(cc, ","), "bcc_count", len(bcc),
			"message_id", messageID, "queued", queued)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued})
}
