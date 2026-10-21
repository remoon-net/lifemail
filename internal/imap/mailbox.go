package imap

import (
	"fmt"
	"path"
	"strings"
	"time"

	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

func (c *conn) listMailboxes(tag, rest string, lsub bool) {
	tokens := splitTokens(rest)
	if len(tokens) < 2 {
		c.bad(tag, "LIST expects reference and pattern")
		return
	}
	pattern := unquote(tokens[1])
	verb := "LIST"
	if lsub {
		verb = "LSUB"
	}
	folders, err := c.srv.Store.FoldersByUser(c.user.ID)
	if err != nil {
		c.no(tag, "cannot list mailboxes")
		return
	}
	for _, f := range folders {
		if f.Deleted || !matchMailbox(pattern, f.Name) {
			continue
		}
		attrs := []string{`\HasNoChildren`}
		if su := specialUse(f); su != "" {
			attrs = append(attrs, su)
		}
		if err := c.writeLine(`* %s (%s) "/" %s`, verb, strings.Join(attrs, " "), quote(f.Name)); err != nil {
			return
		}
	}
	c.ok(tag, verb+" completed")
}

func matchMailbox(pattern, name string) bool {
	pattern = strings.ToLower(strings.ReplaceAll(pattern, "%", "*"))
	name = strings.ToLower(name)
	if pattern == "" || pattern == "*" {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

func specialUse(f *store.Folder) string {
	switch f.Type {
	case 2:
		return `\Inbox`
	case 3:
		return `\Drafts`
	case 4:
		return `\Trash`
	case 5:
		return `\Sent`
	case 12:
		return `\Junk`
	}
	return ""
}

func (c *conn) findFolder(name string) (*store.Folder, error) {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "INBOX") {
		return c.srv.Store.FolderByType(c.user.ID, 2)
	}
	return c.srv.Store.FolderByName(c.user.ID, name)
}

func (c *conn) status(tag, rest string) {
	tokens := splitTokens(rest)
	if len(tokens) < 1 {
		c.bad(tag, "STATUS expects a mailbox")
		return
	}
	folder, err := c.findFolder(unquote(tokens[0]))
	if err != nil {
		c.no(tag, "no such mailbox")
		return
	}
	msgs, err := c.srv.Store.MessagesForIMAP(folder.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	unseen, _ := c.srv.Store.CountUnseen(folder.ID)
	next, _ := c.srv.Store.NextUID(folder.ID)

	items := strings.Trim(strings.Join(tokens[1:], " "), "()")
	var out []string
	for _, item := range strings.Fields(items) {
		switch strings.ToUpper(item) {
		case "MESSAGES":
			out = append(out, "MESSAGES "+itoa(len(msgs)))
		case "UIDNEXT":
			out = append(out, "UIDNEXT "+i64toa(next))
		case "UIDVALIDITY":
			out = append(out, "UIDVALIDITY "+i64toa(uidValidity(folder)))
		case "UNSEEN":
			out = append(out, "UNSEEN "+itoa(unseen))
		case "RECENT":
			out = append(out, "RECENT 0")
		}
	}
	_ = c.writeLine(`* STATUS %s (%s)`, quote(folder.Name), strings.Join(out, " "))
	c.ok(tag, "STATUS completed")
}

// uidValidity returns the folder's immutable IMAP UIDVALIDITY. Folders created
// before the column existed fall back to their id, which is equally stable.
func uidValidity(folder *store.Folder) int64 {
	if folder.UIDValidity > 0 {
		return folder.UIDValidity
	}
	return folder.ID
}

func (c *conn) selectMailbox(tag, rest string, readOnly bool) {
	folder, err := c.findFolder(unquote(strings.TrimSpace(rest)))
	if err != nil {
		c.no(tag, "no such mailbox")
		return
	}
	msgs, err := c.srv.Store.MessagesForIMAP(folder.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	next, _ := c.srv.Store.NextUID(folder.ID)
	unseen, _ := c.srv.Store.CountUnseen(folder.ID)

	c.selected = folder
	c.readOnly = readOnly
	_ = c.writeLine(`* FLAGS (\Answered \Flagged \Deleted \Seen \Draft)`)
	_ = c.writeLine("* %d EXISTS", len(msgs))
	_ = c.writeLine("* 0 RECENT")
	_ = c.writeLine("* OK [UIDVALIDITY %d] UIDs valid", uidValidity(folder))
	_ = c.writeLine("* OK [UIDNEXT %d] Predicted next UID", next)
	if unseen > 0 {
		for i, m := range msgs {
			if !m.IsRead {
				_ = c.writeLine("* OK [UNSEEN %d] First unseen", i+1)
				break
			}
		}
	}
	_ = c.writeLine(`* OK [PERMANENTFLAGS (\Seen \Flagged \Deleted \*)] Flags permitted`)
	if readOnly {
		c.ok(tag, "[READ-ONLY] EXAMINE completed")
	} else {
		c.ok(tag, "[READ-WRITE] SELECT completed")
	}
}

func (c *conn) createMailbox(tag, rest string) {
	name := unquote(strings.TrimSpace(rest))
	if name == "" {
		c.bad(tag, "CREATE expects a mailbox")
		return
	}
	if _, err := c.findFolder(name); err == nil {
		c.no(tag, "mailbox already exists")
		return
	}
	if _, err := c.srv.Store.CreateFolder(c.user.ID, 0, name, 1); err != nil {
		c.no(tag, "cannot create mailbox")
		return
	}
	c.ok(tag, "CREATE completed")
}

func (c *conn) deleteMailbox(tag, rest string) {
	folder, err := c.findFolder(unquote(strings.TrimSpace(rest)))
	if err != nil {
		c.no(tag, "no such mailbox")
		return
	}
	if folder.Type == 2 {
		c.no(tag, "cannot delete INBOX")
		return
	}
	if err := c.srv.Store.DeleteFolder(folder.ID); err != nil {
		c.no(tag, "cannot delete mailbox")
		return
	}
	if c.selected != nil && c.selected.ID == folder.ID {
		c.selected = nil
	}
	c.ok(tag, "DELETE completed")
}

func (c *conn) renameMailbox(tag, rest string) {
	tokens := splitTokens(rest)
	if len(tokens) < 2 {
		c.bad(tag, "RENAME expects two mailbox names")
		return
	}
	oldName, newName := unquote(tokens[0]), unquote(tokens[1])
	folder, err := c.findFolder(oldName)
	if err != nil {
		c.no(tag, "no such mailbox")
		return
	}
	if folder.Type == 2 {
		c.no(tag, "cannot rename INBOX")
		return
	}
	if err := c.srv.Store.RenameFolder(folder.ID, newName); err != nil {
		c.no(tag, "cannot rename mailbox")
		return
	}
	c.ok(tag, "RENAME completed")
}

func (c *conn) appendMessage(tag, rest string, literal []byte) {
	tokens := splitTokens(rest)
	if len(tokens) == 0 {
		c.bad(tag, "APPEND expects a mailbox")
		return
	}
	name := unquote(tokens[0])
	idx := 1
	var flags []string
	if idx < len(tokens) && strings.HasPrefix(tokens[idx], "(") {
		flags = parseFlagWords(tokens[idx])
		idx++
	}
	var received time.Time
	if idx < len(tokens) && strings.HasPrefix(tokens[idx], `"`) {
		received = parseIMAPDate(unquote(tokens[idx]))
	}

	folder, err := c.findFolder(name)
	if err != nil {
		c.no(tag, "[TRYCREATE] no such mailbox")
		return
	}
	parsed, _ := mailparse.Parse(literal)
	if parsed == nil {
		parsed = &mailparse.Message{}
	}
	if received.IsZero() {
		received = parsed.Date
	}
	if received.IsZero() {
		received = time.Now()
	}
	m := &store.Message{
		UserID:            c.user.ID,
		FolderID:          folder.ID,
		Subject:           parsed.Subject,
		FromAddr:          parsed.FromAddr,
		FromName:          parsed.FromName,
		ToAddr:            parsed.To,
		CcAddr:            parsed.Cc,
		DateReceived:      received.Unix(),
		Size:              int64(len(literal)),
		IsRead:            hasFlagWord(flags, `\seen`),
		IsFlagged:         hasFlagWord(flags, `\flagged`),
		Importance:        1,
		MessageClass:      "IPM.Note",
		InternetMessageID: parsed.MessageID,
		BodyText:          parsed.Text,
		BodyHTML:          parsed.HTML,
		Raw:               literal,
		HasAttachments:    len(parsed.Parts) > 0,
	}
	id, err := c.srv.Store.InsertMessage(m)
	if err != nil {
		c.no(tag, "cannot append message")
		return
	}
	for _, part := range parsed.Parts {
		_, _ = c.srv.Store.InsertAttachment(&store.Attachment{
			MessageID:   id,
			FileName:    part.FileName,
			ContentType: part.ContentType,
			ContentID:   part.ContentID,
			IsInline:    part.Inline,
			Data:        part.Data,
		})
	}
	_, _ = c.srv.Store.AddChange(folder.ID, "add", "email", id)
	c.ok(tag, fmt.Sprintf("[APPENDUID %d %d] APPEND completed", folder.ID, id))
}

func (c *conn) closeMailbox(tag string) {
	c.selected = nil
	c.ok(tag, "CLOSE completed")
}

func (c *conn) expunge(tag string) {
	if !c.requireSelected(tag) {
		return
	}
	// Messages marked \Deleted are already soft-deleted and hidden from view.
	c.ok(tag, "EXPUNGE completed")
}

func (c *conn) moveMessages(tag, rest string, uidMode bool) {
	if !c.requireSelected(tag) {
		return
	}
	tokens := splitTokens(rest)
	if len(tokens) < 2 {
		c.bad(tag, "MOVE expects a set and mailbox")
		return
	}
	set, err := parseSeqSet(tokens[0])
	if err != nil {
		c.bad(tag, "invalid sequence set")
		return
	}
	dst, err := c.findFolder(unquote(tokens[1]))
	if err != nil {
		c.no(tag, "[TRYCREATE] no such mailbox")
		return
	}
	msgs, err := c.srv.Store.MessagesForIMAP(c.selected.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	src := c.selected.ID
	lastUID := int64(0)
	if len(msgs) > 0 {
		lastUID = msgs[len(msgs)-1].ID
	}
	for i, m := range msgs {
		var match bool
		if uidMode {
			match = set.contains(m.ID, lastUID)
		} else {
			match = set.contains(int64(i+1), int64(len(msgs)))
		}
		if !match {
			continue
		}
		if err := c.srv.Store.MoveMessage(m.ID, dst.ID); err != nil {
			continue
		}
		_, _ = c.srv.Store.AddChange(src, "delete", "email", m.ID)
		_, _ = c.srv.Store.AddChange(dst.ID, "add", "email", m.ID)
	}
	c.ok(tag, "MOVE completed")
}

func parseFlagWords(value string) []string {
	value = strings.Trim(strings.TrimSpace(value), "()")
	fields := strings.Fields(value)
	for i := range fields {
		fields[i] = strings.ToLower(fields[i])
	}
	return fields
}

func hasFlagWord(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func parseIMAPDate(value string) time.Time {
	t, err := time.Parse("02-Jan-2006 15:04:05 -0700", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return t
}

func itoa(v int) string     { return fmt.Sprintf("%d", v) }
func i64toa(v int64) string { return fmt.Sprintf("%d", v) }
