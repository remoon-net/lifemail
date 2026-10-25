package store

import (
	"strconv"
	"strings"
)

// escapeLike makes a literal string safe for use with LIKE ... ESCAPE '\'.
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// SearchMessages finds messages of a user whose subject, sender or plain body
// contains term. It returns the requested page plus the total match count.
func (s *Store) SearchMessages(userID, term string, limit, offset int) ([]*Message, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(term))) + "%"
	where := ` FROM messages WHERE user_id = ? AND deleted = 0 AND (` +
		`lower(subject) LIKE ? ESCAPE '\' OR lower(from_addr) LIKE ? ESCAPE '\'` +
		` OR lower(from_name) LIKE ? ESCAPE '\' OR lower(body_text) LIKE ? ESCAPE '\')`

	var rows []messageRow
	if err := s.getAll(&rows,
		`SELECT `+messageColumns+where+` ORDER BY date_received DESC LIMIT ? OFFSET ?`,
		userID, pattern, pattern, pattern, pattern, limit, offset); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.get(&total, `SELECT COUNT(*)`+where,
		userID, pattern, pattern, pattern, pattern); err != nil {
		return nil, 0, err
	}
	return messageRows(rows), total, nil
}

// MailSearchHit is one webmail search result: the message, the folder it lives
// in and the conversation key the reader opens.
type MailSearchHit struct {
	Message         *Message
	FolderName      string
	ConversationKey string
	Preview         string
}

// SearchMail searches one account's live messages. folderID limits the search
// to a single folder; 0 searches every folder. Recipients are matched as well
// as subject, sender and plain body.
func (s *Store) SearchMail(userID, term string, folderID int64, limit, offset int) ([]MailSearchHit, int, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	pattern := "%" + escapeLike(strings.ToLower(term)) + "%"
	where := ` FROM messages WHERE user_id = ? AND deleted = 0 AND (` +
		`lower(subject) LIKE ? ESCAPE '\' OR lower(from_addr) LIKE ? ESCAPE '\'` +
		` OR lower(from_name) LIKE ? ESCAPE '\' OR lower(to_addr) LIKE ? ESCAPE '\'` +
		` OR lower(cc_addr) LIKE ? ESCAPE '\' OR lower(body_text) LIKE ? ESCAPE '\')`
	args := []any{userID, pattern, pattern, pattern, pattern, pattern, pattern}
	if folderID > 0 {
		where += ` AND folder_id = ?`
		args = append(args, folderID)
	}

	var rows []messageRow
	pageArgs := append(append([]any{}, args...), limit, offset)
	if err := s.getAll(&rows,
		`SELECT `+messageColumns+where+` ORDER BY date_received DESC, id DESC LIMIT ? OFFSET ?`,
		pageArgs...); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.get(&total, `SELECT COUNT(*)`+where, args...); err != nil {
		return nil, 0, err
	}

	folders, err := s.FoldersByUser(userID)
	if err != nil {
		return nil, 0, err
	}
	names := make(map[int64]string, len(folders))
	for _, f := range folders {
		names[f.ID] = f.Name
	}
	out := make([]MailSearchHit, 0, len(rows))
	for _, row := range rows {
		m := row.message()
		key := m.ConversationID
		if key == "" {
			key = "m:" + strconv.FormatInt(m.ID, 10)
		}
		out = append(out, MailSearchHit{
			Message: m, FolderName: names[m.FolderID], ConversationKey: key,
			Preview: previewText(m.BodyText, 140),
		})
	}
	return out, total, nil
}

// SearchUsers matches accounts by name, primary address or display name, used
// by the GAL.
func (s *Store) SearchUsers(term string, limit int) ([]*User, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(term))) + "%"
	var rows []userRow
	if err := s.getAll(&rows, userSelect+`
		 WHERE u.name LIKE ? ESCAPE '\'
		    OR lower(u.display_name) LIKE ? ESCAPE '\'
		    OR lower(COALESCE(p.address, '')) LIKE ? ESCAPE '\'
		 ORDER BY u.name LIMIT ?`, pattern, pattern, pattern, limit); err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.user())
	}
	return out, nil
}
