package store

import (
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"remoon.net/lifemail/internal/mailparse"
)

// FolderCount is the unread/total tally shown next to a mail folder.
type FolderCount struct {
	FolderID int64
	Unread   int
	Total    int
}

// FolderCounts aggregates live messages per folder for one account. Folders
// without messages are absent from the result.
func (s *Store) FolderCounts(userID string) ([]FolderCount, error) {
	type row struct {
		FolderID int64 `db:"folder_id"`
		Unread   int   `db:"unread"`
		Total    int   `db:"total"`
	}
	var rows []row
	if err := s.getAll(&rows,
		`SELECT folder_id,
		        COALESCE(SUM(CASE WHEN is_read = 0 THEN 1 ELSE 0 END), 0) AS unread,
		        COUNT(*) AS total
		 FROM messages
		 WHERE user_id = ? AND deleted = 0
		 GROUP BY folder_id`, userID); err != nil {
		return nil, err
	}
	out := make([]FolderCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, FolderCount{FolderID: r.FolderID, Unread: r.Unread, Total: r.Total})
	}
	return out, nil
}

// conversationKeyExpr derives a grouping key for messages that predate the
// conversation_id column. The CAST keeps PostgreSQL's || operator happy.
const conversationKeyExpr = `CASE WHEN conversation_id = '' THEN 'm:' || CAST(id AS TEXT) ELSE conversation_id END`

// Conversation is one thread's summary. Inbox and Sent are aggregated
// together, so a reply shows up in the conversation it belongs to.
type Conversation struct {
	// Key is the raw conversation id, or "m:<id>" for messages without one.
	// HTTP handlers base64url it before putting it on the wire.
	Key            string
	LastAt         int64
	LatestID       int64
	Count          int
	Unread         int
	Subject        string
	FromName       string
	FromAddr       string
	ToAddr         string
	Preview        string
	HasAttachments bool
	HasInbox       bool
	HasSent        bool
}

// mailFolderIDs returns the live Inbox and Sent folder ids of an account.
func (s *Store) mailFolderIDs(userID string) (inbox, sent int64, err error) {
	type row struct {
		ID   int64 `db:"id"`
		Type int   `db:"type"`
	}
	var rows []row
	if err := s.getAll(&rows,
		`SELECT id, type FROM folders WHERE user_id = ? AND deleted = 0 AND type IN (2, 5)`,
		userID); err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		switch r.Type {
		case 2:
			inbox = r.ID
		case 5:
			sent = r.ID
		}
	}
	return inbox, sent, nil
}

// MailFolderIDs exposes the Inbox and Sent folder ids for the webmail reader.
func (s *Store) MailFolderIDs(userID string) (inbox, sent int64, err error) {
	return s.mailFolderIDs(userID)
}

// conversationScope returns the folders a thread is read from. Inbox and Sent
// form one scope; every other folder stays isolated.
func (s *Store) conversationScope(userID string, folderID int64) ([]int64, error) {
	inbox, sent, err := s.mailFolderIDs(userID)
	if err != nil {
		return nil, err
	}
	if inbox == 0 || (folderID != inbox && folderID != sent) {
		return []int64{folderID}, nil
	}
	scope := []int64{inbox}
	if sent != 0 && sent != inbox {
		scope = append(scope, sent)
	}
	return scope, nil
}

// ThreadForReply finds the conversation a reply belongs to when the client left
// out References/In-Reply-To. Some clients (MIUI Mail among them) only add a
// "Re:" prefix and quote the original in the body, which would otherwise start
// a new thread and hide the message being replied to.
//
// The match has to agree on the normalised subject and share a participant: the
// new sender must appear among the candidate's recipients, or the candidate's
// sender must appear among the new recipients. Subject alone is not enough,
// because unrelated senders often reuse a generic subject.
//
// It returns "" when nothing matches, so the caller keeps the message's own
// key.
func (s *Store) ThreadForReply(userID, subject, fromAddr string, toAddrs []string) (string, error) {
	want := mailparse.NormalizedSubject(subject)
	if want == "" {
		return "", nil
	}
	inbox, sent, err := s.mailFolderIDs(userID)
	if err != nil {
		return "", err
	}
	if inbox == 0 && sent == 0 {
		return "", nil
	}
	scope := make([]int64, 0, 2)
	if inbox != 0 {
		scope = append(scope, inbox)
	}
	if sent != 0 && sent != inbox {
		scope = append(scope, sent)
	}

	type row struct {
		Subject        string `db:"subject"`
		FromAddr       string `db:"from_addr"`
		ToAddr         string `db:"to_addr"`
		CcAddr         string `db:"cc_addr"`
		ConversationID string `db:"conversation_id"`
	}
	// Recent messages only: a reply belongs to a thread that was active
	// recently, and scanning the whole mailbox on every delivery is wasteful.
	q, args, err := sqlx.In(
		`SELECT subject, from_addr, to_addr, cc_addr, conversation_id
		 FROM messages
		 WHERE user_id = ? AND deleted = 0 AND folder_id IN (?)
		 ORDER BY date_received DESC, id DESC LIMIT 200`, userID, scope)
	if err != nil {
		return "", err
	}
	var rows []row
	if err := s.getAll(&rows, q, args...); err != nil {
		return "", err
	}
	newSender := strings.ToLower(strings.TrimSpace(fromAddr))
	newRecipients := lowerAddressSet(toAddrs)
	// Rows are newest first, so keep overwriting: the oldest match wins. The
	// oldest message of a thread carries the thread's own key; newer replies may
	// themselves be headerless strays with a key of their own.
	match := ""
	for _, r := range rows {
		if r.ConversationID == "" || mailparse.NormalizedSubject(r.Subject) != want {
			continue
		}
		if !replyParticipantsMatch(newSender, newRecipients, r.FromAddr, r.ToAddr, r.CcAddr) {
			continue
		}
		match = r.ConversationID
	}
	return match, nil
}

// lowerAddressSet normalises a recipient list for case-insensitive comparison.
func lowerAddressSet(addrs []string) map[string]bool {
	out := make(map[string]bool, len(addrs))
	for _, addr := range addrs {
		if addr = strings.ToLower(strings.TrimSpace(addr)); addr != "" {
			out[addr] = true
		}
	}
	return out
}

// replyParticipantsMatch reports whether the two sides of a conversation know
// each other: either the new sender wrote to the candidate, or the candidate's
// sender is one of the new recipients.
func replyParticipantsMatch(newSender string, newRecipients map[string]bool,
	rowFrom, rowTo, rowCc string) bool {
	sender := strings.ToLower(strings.TrimSpace(rowFrom))
	if sender != "" && newRecipients[sender] {
		return true
	}
	if newSender == "" {
		return false
	}
	for _, list := range []string{rowTo, rowCc} {
		for _, addr := range mailparse.Addresses(list) {
			if strings.EqualFold(addr, newSender) {
				return true
			}
		}
	}
	return false
}

// ConversationSummaries groups live messages by conversation, newest thread
// first. Inbox and Sent share one thread list; other folders stay separate.
func (s *Store) ConversationSummaries(userID string, folderID int64, limit int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	inbox, sent, err := s.mailFolderIDs(userID)
	if err != nil {
		return nil, err
	}
	scope, err := s.conversationScope(userID, folderID)
	if err != nil {
		return nil, err
	}
	crossFolder := len(scope) > 1 || (inbox != 0 && folderID == inbox)

	type row struct {
		Key      string `db:"cid"`
		LastAt   int64  `db:"last_at"`
		LatestID int64  `db:"latest_id"`
		Total    int    `db:"total"`
		Unread   int    `db:"unread"`
		HasInbox int    `db:"has_inbox"`
		HasSent  int    `db:"has_sent"`
	}

	query := `SELECT ` + conversationKeyExpr + ` AS cid,
		        MAX(date_received) AS last_at,
		        MAX(id) AS latest_id,
		        COUNT(*) AS total,
		        COALESCE(SUM(CASE WHEN is_read = 0 THEN 1 ELSE 0 END), 0) AS unread,
		        MAX(CASE WHEN folder_id = ? THEN 1 ELSE 0 END) AS has_inbox,
		        MAX(CASE WHEN folder_id = ? THEN 1 ELSE 0 END) AS has_sent
		 FROM messages
		 WHERE user_id = ? AND deleted = 0 AND folder_id IN (?)
		 GROUP BY ` + conversationKeyExpr
	args := []any{inbox, sent, userID, scope}
	if crossFolder && folderID == inbox {
		query += ` HAVING MAX(CASE WHEN folder_id = ? THEN 1 ELSE 0 END) = 1`
		args = append(args, inbox)
	}
	if crossFolder && folderID == sent {
		query += ` HAVING MAX(CASE WHEN folder_id = ? THEN 1 ELSE 0 END) = 1`
		args = append(args, sent)
	}
	query += ` ORDER BY last_at DESC, latest_id DESC LIMIT ?`
	args = append(args, limit)

	q, inArgs, err := sqlx.In(query, args...)
	if err != nil {
		return nil, err
	}
	var rows []row
	if err := s.getAll(&rows, q, inArgs...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.LatestID)
	}
	latest, err := s.messagesByID(ids)
	if err != nil {
		return nil, err
	}
	firstSubjects, err := s.conversationFirstSubjects(userID, scope)
	if err != nil {
		return nil, err
	}
	out := make([]*Conversation, 0, len(rows))
	for _, r := range rows {
		c := &Conversation{
			Key: r.Key, LastAt: r.LastAt, LatestID: r.LatestID,
			Count: r.Total, Unread: r.Unread,
			HasInbox: r.HasInbox != 0, HasSent: r.HasSent != 0,
		}
		if m := latest[r.LatestID]; m != nil {
			c.Subject = m.Subject
			c.FromName = m.FromName
			c.FromAddr = m.FromAddr
			c.ToAddr = m.ToAddr
			c.Preview = previewText(m.BodyText, 140)
			c.HasAttachments = m.HasAttachments
		}
		// Replies carry a "Re:" prefix; the thread keeps the subject of its
		// oldest message so the list does not churn on every reply.
		if subject := firstSubjects[r.Key]; subject != "" {
			c.Subject = subject
		}
		out = append(out, c)
	}
	return out, nil
}

// conversationFirstSubjects maps a conversation key to the subject of its
// oldest message, used as the thread title.
func (s *Store) conversationFirstSubjects(userID string, folderIDs []int64) (map[string]string, error) {
	query := `SELECT cid, subject FROM (
		SELECT ` + conversationKeyExpr + ` AS cid, subject,
		       ROW_NUMBER() OVER (PARTITION BY ` + conversationKeyExpr +
		` ORDER BY date_received ASC, id ASC) AS rn
		FROM messages
		WHERE user_id = ? AND deleted = 0 AND folder_id IN (?)
	) ranked WHERE rn = 1`
	q, inArgs, err := sqlx.In(query, userID, folderIDs)
	if err != nil {
		return nil, err
	}
	type row struct {
		Key     string `db:"cid"`
		Subject string `db:"subject"`
	}
	var rows []row
	if err := s.getAll(&rows, q, inArgs...); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Subject
	}
	return out, nil
}

// ConversationMessages returns the live messages of one thread, oldest first.
// When the folder is Inbox or Sent, both folders are read so a reply appears
// in the conversation.
func (s *Store) ConversationMessages(userID, key string, folderID int64, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
	}
	scope, err := s.conversationScope(userID, folderID)
	if err != nil {
		return nil, err
	}
	// "m:<id>" is the fallback key for messages without a conversation id.
	if rest, ok := strings.CutPrefix(key, "m:"); ok {
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || id <= 0 {
			return nil, ErrNotFound
		}
		m, err := s.MessageMetaByID(id)
		if err != nil {
			return nil, err
		}
		if m.UserID != userID || m.Deleted || !containsID(scope, m.FolderID) {
			return nil, ErrNotFound
		}
		return []*Message{m}, nil
	}
	q, args, err := sqlx.In(
		messageSelect+` WHERE user_id = ? AND deleted = 0 AND folder_id IN (?) AND conversation_id = ?
		 ORDER BY date_received, id LIMIT ?`, userID, scope, key, limit)
	if err != nil {
		return nil, err
	}
	var rows []messageRow
	if err := s.getAll(&rows, q, args...); err != nil {
		return nil, err
	}
	return messageRows(rows), nil
}

func containsID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// messagesByID loads message metadata for a set of ids.
func (s *Store) messagesByID(ids []int64) (map[int64]*Message, error) {
	out := make(map[int64]*Message, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q, args, err := sqlx.In(messageSelect+` WHERE id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	var rows []messageRow
	if err := s.getAll(&rows, q, args...); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.message()
	}
	return out, nil
}

// previewText collapses whitespace and truncates a plain-text body.
func previewText(text string, max int) string {
	fields := strings.Fields(text)
	joined := strings.Join(fields, " ")
	runes := []rune(joined)
	if len(runes) <= max {
		return joined
	}
	return string(runes[:max]) + "…"
}

// SearchContacts lists live address book entries for one account, filtered by
// a case-insensitive substring when q is set.
func (s *Store) SearchContacts(userID, q string, limit int) ([]*Contact, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := contactSelect + ` WHERE user_id = ? AND deleted = 0`
	args := []any{userID}
	if term := strings.ToLower(strings.TrimSpace(q)); term != "" {
		like := "%" + term + "%"
		query += ` AND (lower(file_as) LIKE ? OR lower(first_name) LIKE ?
			OR lower(last_name) LIKE ? OR lower(company_name) LIKE ?
			OR lower(email1) LIKE ? OR lower(email2) LIKE ? OR lower(email3) LIKE ?)`
		args = append(args, like, like, like, like, like, like, like)
	}
	query += ` ORDER BY file_as, id LIMIT ?`
	args = append(args, limit)
	var rows []contactRow
	if err := s.getAll(&rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*Contact, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.contact())
	}
	return out, nil
}

// EventsByFolderBetween lists live events that overlap [from, to] in unix
// seconds. A zero bound means "unbounded".
func (s *Store) EventsByFolderBetween(folderID, from, to int64) ([]*Event, error) {
	query := eventSelect + ` WHERE folder_id = ? AND deleted = 0`
	args := []any{folderID}
	if from > 0 {
		query += ` AND end_time >= ?`
		args = append(args, from)
	}
	if to > 0 {
		query += ` AND start_time <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY start_time, id`
	var rows []eventRow
	if err := s.getAll(&rows, query, args...); err != nil {
		return nil, err
	}
	return eventRows(rows), nil
}
