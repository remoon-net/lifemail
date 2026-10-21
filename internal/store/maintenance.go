package store

// PruneChanges deletes change records that every device has already consumed.
// For each folder the watermark is the lowest consumed sequence, so nothing a
// client may still need is removed.
func (s *Store) PruneChanges() (int64, error) {
	res, err := s.exec(
		`DELETE FROM changes
		 WHERE seq <= (SELECT MIN(ss.seq) FROM sync_states ss WHERE ss.folder_id = changes.folder_id)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum reclaims space. It rewrites the whole database file, so callers
// should treat it as a maintenance-only operation.
func (s *Store) Vacuum() error {
	if s.dialect == "postgres" {
		_, err := s.exec(`VACUUM (ANALYZE)`)
		return err
	}
	_, err := s.exec(`VACUUM`)
	return err
}
