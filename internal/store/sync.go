package store

// Change is one entry in a folder's change feed.
type Change struct {
	Seq      int64  `db:"seq"`
	FolderID int64  `db:"folder_id"`
	ItemType string `db:"item_type"`
	ItemID   int64  `db:"item_id"`
	Kind     string `db:"kind"` // add, change, delete
}

// AddChange appends a change record for a folder and returns its sequence.
func (s *Store) AddChange(folderID int64, kind, itemType string, itemID int64) (int64, error) {
	var seq int64
	err := s.get(&seq,
		`INSERT INTO changes (folder_id, item_type, item_id, kind) VALUES (?, ?, ?, ?) RETURNING seq`,
		folderID, itemType, itemID, kind)
	return seq, err
}

// ChangesSince returns up to limit changes with seq greater than after.
func (s *Store) ChangesSince(folderID, after int64, limit int) ([]Change, error) {
	q := `SELECT seq, folder_id, item_type, item_id, kind FROM changes
		WHERE folder_id = ? AND seq > ? ORDER BY seq`
	args := []any{folderID, after}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	var out []Change
	err := s.getAll(&out, q, args...)
	return out, err
}

// HasChangesAfter reports whether newer changes exist.
func (s *Store) HasChangesAfter(folderID, after int64) (bool, error) {
	var n int
	if err := s.get(&n,
		`SELECT count(*) FROM changes WHERE folder_id = ? AND seq > ?`, folderID, after); err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountChangesAfter counts pending changes (used by GetItemEstimate).
func (s *Store) CountChangesAfter(folderID, after int64) (int, error) {
	var n int
	err := s.get(&n, `SELECT count(*) FROM changes WHERE folder_id = ? AND seq > ?`, folderID, after)
	return n, err
}

// SyncState tracks how far a device has consumed a folder's change feed.
type SyncState struct {
	UserID   string `db:"user_id"`
	DeviceID string `db:"device_id"`
	FolderID int64  `db:"folder_id"`
	SyncKey  string `db:"sync_key"`
	Seq      int64  `db:"seq"`
}

// GetSyncState returns nil, nil when the device has never synced the folder.
func (s *Store) GetSyncState(userID, deviceID string, folderID int64) (*SyncState, error) {
	var st SyncState
	err := s.get(&st,
		`SELECT user_id, device_id, folder_id, sync_key, seq FROM sync_states
		 WHERE user_id = ? AND device_id = ? AND folder_id = ?`,
		userID, deviceID, folderID)
	if err == ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// SaveSyncState inserts or updates a device sync state.
func (s *Store) SaveSyncState(st *SyncState) error {
	_, err := s.exec(
		`INSERT INTO sync_states (user_id, device_id, folder_id, sync_key, seq)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, device_id, folder_id)
		 DO UPDATE SET sync_key = excluded.sync_key, seq = excluded.seq`,
		st.UserID, st.DeviceID, st.FolderID, st.SyncKey, st.Seq)
	return err
}

// ResetSyncState drops a device's sync state and item knowledge for a folder.
func (s *Store) ResetSyncState(userID, deviceID string, folderID int64) error {
	if _, err := s.exec(
		`DELETE FROM sync_states WHERE user_id = ? AND device_id = ? AND folder_id = ?`,
		userID, deviceID, folderID); err != nil {
		return err
	}
	return s.ForgetAllItems(userID, deviceID, folderID)
}

// KnownItems returns the server ids a device already knows in a folder.
func (s *Store) KnownItems(userID, deviceID string, folderID int64) (map[int64]bool, error) {
	var ids []int64
	if err := s.getAll(&ids,
		`SELECT item_id FROM sync_items WHERE user_id = ? AND device_id = ? AND folder_id = ?`,
		userID, deviceID, folderID); err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// MarkItemKnown records that a device has received an item.
func (s *Store) MarkItemKnown(userID, deviceID string, folderID, itemID int64) error {
	_, err := s.exec(
		`INSERT INTO sync_items (user_id, device_id, folder_id, item_id) VALUES (?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`,
		userID, deviceID, folderID, itemID)
	return err
}

// ForgetItem removes a single item from a device's known set.
func (s *Store) ForgetItem(userID, deviceID string, folderID, itemID int64) error {
	_, err := s.exec(
		`DELETE FROM sync_items WHERE user_id = ? AND device_id = ? AND folder_id = ? AND item_id = ?`,
		userID, deviceID, folderID, itemID)
	return err
}

// ForgetAllItems clears a device's known items for a folder.
func (s *Store) ForgetAllItems(userID, deviceID string, folderID int64) error {
	_, err := s.exec(
		`DELETE FROM sync_items WHERE user_id = ? AND device_id = ? AND folder_id = ?`,
		userID, deviceID, folderID)
	return err
}
