package store

import "github.com/jmoiron/sqlx"

// Setting returns one setting value, or "" when it is not set.
func (s *Store) Setting(key string) (string, error) {
	var value string
	err := s.get(&value, `SELECT value FROM settings WHERE key = ?`, key)
	if err == ErrNotFound {
		return "", nil
	}
	return value, err
}

// Settings returns every key/value pair in the settings table.
func (s *Store) Settings() (map[string]string, error) {
	type item struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}
	var rows []item
	if err := s.getAll(&rows, `SELECT key, value FROM settings`); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// SetSetting writes a single setting.
func (s *Store) SetSetting(key, value string) error {
	return s.SetSettings(map[string]string{key: value})
}

// SetSettings writes several settings in one transaction.
func (s *Store) SetSettings(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	return s.inTx(func(tx *sqlx.Tx) error {
		for key, value := range values {
			if _, err := tx.Exec(s.rebind(
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT (key) DO UPDATE SET value = excluded.value`),
				key, value); err != nil {
				return err
			}
		}
		return nil
	})
}
