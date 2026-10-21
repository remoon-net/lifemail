package store

import "strings"

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
