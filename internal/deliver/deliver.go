// Package deliver stores parsed messages into local mailboxes. It is shared by
// inbound mail and by local delivery of mail sent between accounts on this
// server.
package deliver

import (
	"strings"
	"time"

	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

// Folder types follow the EAS/IMAP convention used across the store.
const (
	FolderInbox = 2
	FolderSent  = 5
)

// InboxHook runs after a message lands in an inbox. It lets callers react to new
// mail (out-of-office notices, notifications) without this package knowing about
// SMTP. It must not block for long: it runs on the delivery path.
type InboxHook func(user *store.User, parsed *mailparse.Message, raw []byte, messageID int64, conversationID string)

// OnInboxDelivered is called for every message stored in an inbox.
var OnInboxDelivered InboxHook

// receivedAsBcc reports whether the account address is absent from both the To
// and Cc headers, which means the message arrived through Bcc.
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

// NotifyInbox runs the inbox hook, if any. Every code path that stores a
// message in an inbox must call it, otherwise auto-replies and calendar
// responses silently stop working for that path.
func NotifyInbox(user *store.User, parsed *mailparse.Message, raw []byte, messageID int64, conversationID string) {
	if OnInboxDelivered != nil {
		OnInboxDelivered(user, parsed, raw, messageID, conversationID)
	}
}

// ToUser stores parsed in the user's folder of the given type and records the
// change so devices sync it.
func ToUser(st *store.Store, user *store.User, folderType int, parsed *mailparse.Message, raw []byte) (int64, error) {
	folder, err := st.FolderByType(user.ID, folderType)
	if err != nil {
		return 0, err
	}
	received := time.Now().Unix()
	if !parsed.Date.IsZero() {
		received = parsed.Date.Unix()
	}
	m := &store.Message{
		UserID:            user.ID,
		FolderID:          folder.ID,
		Subject:           parsed.Subject,
		FromAddr:          parsed.FromAddr,
		FromName:          parsed.FromName,
		ToAddr:            parsed.To,
		CcAddr:            parsed.Cc,
		DateReceived:      received,
		Size:              int64(len(raw)),
		IsRead:            folderType == FolderSent,
		Importance:        1,
		MessageClass:      "IPM.Note",
		InternetMessageID: parsed.MessageID,
		BodyText:          parsed.Text,
		BodyHTML:          parsed.HTML,
		Raw:               raw,
		HasAttachments:    len(parsed.Parts) > 0,
		SenderAddr:        parsed.SenderAddr,
		SenderName:        parsed.SenderName,
		ReceivedAsBcc:     folderType == FolderInbox && receivedAsBcc(parsed, user.Address),
		ConversationID:    ConversationID(st, user, parsed),
	}
	id, err := st.InsertMessage(m)
	if err != nil {
		return 0, err
	}
	for _, part := range parsed.Parts {
		if _, err := st.InsertAttachment(&store.Attachment{
			MessageID:   id,
			FileName:    part.FileName,
			ContentType: part.ContentType,
			ContentID:   part.ContentID,
			IsInline:    part.Inline,
			Data:        part.Data,
		}); err != nil {
			return id, err
		}
	}
	if _, err := st.AddChange(folder.ID, "add", "email", id); err != nil {
		return id, err
	}
	if folderType == FolderInbox {
		NotifyInbox(user, parsed, raw, id, m.ConversationID)
	}
	return id, nil
}

// ConversationID picks the conversation key for a message that is about to be
// stored. Normally the MIME headers decide; some clients (MIUI Mail among them)
// send replies with only a "Re:" subject and drop References/In-Reply-To, which
// would start a new thread and hide the message being answered. For those we
// look up the thread by normalised subject plus participants.
//
// The lookup is best-effort: on error the header-derived key is used, so a
// mailbox is never rejected because the thread index could not be read.
func ConversationID(st *store.Store, user *store.User, parsed *mailparse.Message) string {
	id := mailparse.ConversationID(parsed)
	if len(parsed.References) > 0 || strings.TrimSpace(parsed.InReplyTo) != "" {
		return id
	}
	if !mailparse.IsReplySubject(parsed.Subject) {
		return id
	}
	thread, err := st.ThreadForReply(user.ID, parsed.Subject, parsed.FromAddr, mailparse.Addresses(parsed.To))
	if err != nil || thread == "" {
		return id
	}
	return thread
}
