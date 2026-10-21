package eas

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/outbound"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// cmdComposeMail sends drafts produced by the device.
func (s *Server) cmdComposeMail(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageComposeMail, "ComposeMail")
	saveInSent := req.Child("SaveInSentItems") != nil
	for _, name := range []string{"SendMail", "SmartReply", "SmartForward"} {
		node := req.Child(name)
		if node == nil {
			continue
		}
		status := s.sendComposed(user, node, saveInSent)
		resp.Children = append(resp.Children, wbxml.N(pageComposeMail, name,
			wbxml.T(pageComposeMail, "Status", status)))
	}
	if len(resp.Children) == 0 {
		resp.Children = append(resp.Children, wbxml.T(pageComposeMail, "Status", "2"))
	}
	return resp, nil
}

// cmdSendMail handles the ComposeMail-page commands whose request root is the
// command element itself (SendMail, SmartReply, SmartForward). EAS 14 servers
// always keep a copy in Sent Items; older clients ask for it explicitly.
func (s *Server) cmdSendMail(user *store.User, cmd string, req *wbxml.Node) (*wbxml.Node, error) {
	status := s.sendComposed(user, req, true)
	return wbxml.N(pageComposeMail, cmd,
		wbxml.T(pageComposeMail, "Status", status)), nil
}

func (s *Server) sendComposed(user *store.User, node *wbxml.Node, saveInSent bool) string {
	raw, err := decodeMime(node.Child("Mime"))
	if err != nil {
		s.Log.Warn("cannot decode outbound mime", "err", err, "user", user.Name)
		return "6"
	}

	parsed, err := mailparse.Parse(raw)
	if err != nil && parsed == nil {
		return "6"
	}
	if path := dumpMIME(raw); path != "" {
		s.Log.Debug("eas mime dumped", "path", path)
	}
	if parsed.MessageID == "" {
		// Clients reconcile the Sent copy by Message-ID, so make sure there is
		// one even when the device left it out.
		domain := "lifemail.local"
		if at := strings.LastIndex(user.Address, "@"); at >= 0 {
			domain = user.Address[at+1:]
		}
		var id string
		raw, id = mailparse.EnsureMessageID(raw, domain)
		parsed.MessageID = id
	}
	var recipients []string
	seen := map[string]bool{}
	for _, list := range []string{parsed.To, parsed.Cc, parsed.Bcc} {
		for _, addr := range mailparse.Addresses(list) {
			if !seen[addr] {
				seen[addr] = true
				recipients = append(recipients, addr)
			}
		}
	}
	if len(recipients) == 0 {
		return "2"
	}

	// Recipients on this server are delivered directly; addresses on a
	// configured domain without an account are rejected instead of being
	// relayed through Cloudflare, which would loop back or bounce.
	local, external, unknown := s.classifyRecipients(recipients)
	if len(unknown) > 0 {
		s.Log.Warn("unknown local recipient", "user", user.Name,
			"recipients", strings.Join(unknown, ","))
		return "6"
	}

	// The account sends as its primary address unless the From header names
	// another address the account owns.
	sender := user.Address
	if sender == "" {
		s.Log.Warn("cannot send mail: account has no address", "user", user.Name)
		return "6"
	}
	if from := strings.ToLower(strings.TrimSpace(parsed.FromAddr)); from != "" && !strings.EqualFold(from, sender) {
		if owner, err := s.Store.UserByAddress(from); err == nil && owner.ID == user.ID {
			sender = from
		}
	}

	// MIUI Mail sends meeting replies (and some other generated mail) with an
	// empty From header. Fall back to the sending account so recipients see a
	// real sender instead of a blank line. The stored and relayed MIME has to
	// be fixed too, not just the in-memory parse: clients that fetch the whole
	// message read the header, not our database.
	if strings.TrimSpace(parsed.FromAddr) == "" {
		parsed.FromAddr = sender
		if parsed.FromName == "" {
			parsed.FromName = user.DisplayName
		}
		raw = mailparse.EnsureFrom(raw, parsed.FromName, parsed.FromAddr)
	}

	if len(external) > 0 {
		envelope := s.Sender.EnvelopeFrom(sender)
		if !s.Sender.Enabled() {
			s.Log.Error("cannot send mail: SMTP is not configured",
				"user", user.Name, "recipients", strings.Join(external, ","))
			return "6"
		}
		if err := s.Sender.Send(context.Background(), envelope, external, raw); err != nil {
			id, qerr := outbound.Enqueue(s.Store, envelope, external, raw)
			if qerr != nil {
				s.Log.Error("cannot queue outbound mail", "err", qerr, "user", user.Name)
				return "6"
			}
			s.Log.Warn("queued outbound mail", "id", id, "user", user.Name,
				"recipients", strings.Join(external, ","), "reason", err)
		}
	}

	for _, owner := range local {
		if parsed.MessageID != "" {
			if exists, err := s.Store.MessageIDExists(owner.ID, parsed.MessageID); err == nil && exists {
				continue
			}
		}
		if _, err := deliver.ToUser(s.Store, owner, deliver.FolderInbox, parsed, raw); err != nil {
			s.Log.Error("local delivery failed", "err", err, "user", user.Name, "recipient", owner.Name)
			return "6"
		}
		s.Log.Info("delivered locally", "from", sender, "to", owner.Name)
	}

	if saveInSent {
		s.saveSent(user, raw, parsed)
	}
	return "1"
}

// classifyRecipients splits recipients into local accounts, external
// addresses, and addresses on a configured domain that no account owns.
func (s *Server) classifyRecipients(recipients []string) (local []*store.User, external, unknown []string) {
	for _, addr := range recipients {
		if owner, err := s.Store.UserByAddress(addr); err == nil {
			local = append(local, owner)
			continue
		}
		if s.IsLocalDomain != nil && s.IsLocalDomain(mailparse.Domain(addr)) {
			unknown = append(unknown, addr)
			continue
		}
		external = append(external, addr)
	}
	return local, external, unknown
}

// saveSent stores a copy of an outbound message in Sent Items.
func (s *Server) saveSent(user *store.User, raw []byte, parsed *mailparse.Message) {
	if _, err := deliver.ToUser(s.Store, user, deliver.FolderSent, parsed, raw); err != nil {
		s.Log.Warn("cannot store sent message", "err", err, "user", user.Name)
	}
}

// dumpMIME stores the decoded outbound message when LIFEMAIL_EAS_DUMP is set,
// which makes client-specific MIME quirks inspectable offline.
func dumpMIME(raw []byte) string {
	dir := os.Getenv("LIFEMAIL_EAS_DUMP")
	if dir == "" || len(raw) == 0 {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-sendmail.eml", time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return ""
	}
	return path
}

// decodeMime accepts the two forms clients put in the Mime element: base64
// text (what the protocol asks for) and raw RFC822 bytes (some Android
// clients send those instead).
func decodeMime(n *wbxml.Node) ([]byte, error) {
	if n == nil {
		return nil, errors.New("missing mime")
	}
	text := n.Text
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty mime")
	}
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	if raw, err := base64.StdEncoding.DecodeString(compact); err == nil {
		return raw, nil
	}
	// OPAQUE payloads and raw MIME both arrive as plain text here.
	if n.Binary || strings.Contains(text, ":") {
		return []byte(text), nil
	}
	return nil, errors.New("invalid base64 mime")
}
