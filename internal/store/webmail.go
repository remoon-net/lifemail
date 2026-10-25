package store

import (
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
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

// Conversation is one thread's summary inside a single folder.
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
}

// ConversationSummaries groups a folder's live messages by conversation,
// newest thread first. The latest message of each thread supplies the display
// fields, so opening the list costs two queries instead of one per row.
func (s *Store) ConversationSummaries(folderID int64, limit int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	type row struct {
		Key      string `db:"cid"`
		LastAt   int64  `db:"last_at"`
		LatestID int64  `db:"latest_id"`
		Total    int    `db:"total"`
		Unread   int    `db:"unread"`
	}
	var rows []row
	if err := s.getAll(&rows,
		`SELECT `+conversationKeyExpr+` AS cid,
		        MAX(date_received) AS last_at,
		        MAX(id) AS latest_id,
		        COUNT(*) AS total,
		        COALESCE(SUM(CASE WHEN is_read = 0 THEN 1 ELSE 0 END), 0) AS unread
		 FROM messages
		 WHERE folder_id = ? AND deleted = 0
		 GROUP BY `+conversationKeyExpr+`
		 ORDER BY last_at DESC, latest_id DESC
		 LIMIT ?`, folderID, limit); err != nil {
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
	out := make([]*Conversation, 0, len(rows))
	for _, r := range rows {
		c := &Conversation{
			Key: r.Key, LastAt: r.LastAt, LatestID: r.LatestID,
			Count: r.Total, Unread: r.Unread,
		}
		if m := latest[r.LatestID]; m != nil {
			c.Subject = m.Subject
			c.FromName = m.FromName
			c.FromAddr = m.FromAddr
			c.ToAddr = m.ToAddr
			c.Preview = previewText(m.BodyText, 140)
			c.HasAttachments = m.HasAttachments
		}
		out = append(out, c)
	}
	return out, nil
}

// ConversationMessages returns the live messages of one thread in a folder,
// oldest first, so the reader can expand it without another list round trip.
func (s *Store) ConversationMessages(folderID int64, key string, limit int) ([]*Message, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
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
		if m.FolderID != folderID || m.Deleted {
			return nil, ErrNotFound
		}
		return []*Message{m}, nil
	}
	var rows []messageRow
	if err := s.getAll(&rows,
		messageSelect+` WHERE folder_id = ? AND deleted = 0 AND conversation_id = ?
		 ORDER BY date_received, id LIMIT ?`, folderID, key, limit); err != nil {
		return nil, err
	}
	return messageRows(rows), nil
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
