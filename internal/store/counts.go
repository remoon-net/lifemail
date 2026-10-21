package store

// CountUsers returns the number of accounts.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.get(&n, `SELECT COUNT(*) FROM users`)
	return n, err
}

// CountMessages returns the number of live (not deleted) messages.
func (s *Store) CountMessages() (int, error) {
	var n int
	err := s.get(&n, `SELECT COUNT(*) FROM messages WHERE deleted = 0`)
	return n, err
}

// CountDevices returns the number of known ActiveSync devices.
func (s *Store) CountDevices() (int, error) {
	var n int
	err := s.get(&n, `SELECT COUNT(*) FROM devices`)
	return n, err
}
