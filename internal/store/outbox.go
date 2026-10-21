package store

import (
	"strings"

	"remoon.net/lifemail/internal/blobstore"
)

// Outbound is a message waiting for SMTP delivery.
type Outbound struct {
	ID           int64
	EnvelopeFrom string
	Recipients   []string
	Raw          []byte
	// RawPath is the blob key holding the MIME source.
	RawPath     string
	Attempts    int
	LastError   string
	NextAttempt int64
	Failed      bool
	CreatedAt   int64
}

type outboundRow struct {
	ID           int64  `db:"id"`
	EnvelopeFrom string `db:"envelope_from"`
	Recipients   string `db:"recipients"`
	RawPath      string `db:"raw_path"`
	Attempts     int    `db:"attempts"`
	LastError    string `db:"last_error"`
	NextAttempt  int64  `db:"next_attempt"`
	Failed       int    `db:"failed"`
	CreatedAt    int64  `db:"created_at"`
}

func (r outboundRow) outbound() *Outbound {
	return &Outbound{
		ID: r.ID, EnvelopeFrom: r.EnvelopeFrom, Recipients: splitOutboxRecipients(r.Recipients),
		RawPath: r.RawPath, Attempts: r.Attempts, LastError: r.LastError,
		NextAttempt: r.NextAttempt, Failed: r.Failed != 0, CreatedAt: r.CreatedAt,
	}
}

const outboxColumns = `id, envelope_from, recipients, raw_path, attempts, last_error,
	next_attempt, failed, created_at`

func outboundRows(rows []outboundRow) []*Outbound {
	out := make([]*Outbound, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.outbound())
	}
	return out
}

// EnqueueOutbound stores a message for later delivery. The MIME source goes to
// the blob directory, like every other payload.
func (s *Store) EnqueueOutbound(envelopeFrom string, recipients []string, raw []byte) (int64, error) {
	if s.blobs == nil {
		return 0, errNoBlobDir
	}
	var id int64
	if err := s.get(&id,
		`INSERT INTO outbox (envelope_from, recipients, raw_path, attempts, last_error,
		 next_attempt, failed, created_at)
		 VALUES (?, ?, '', 0, '', 0, 0, ?) RETURNING id`,
		envelopeFrom, strings.Join(recipients, ","), nowUnix()); err != nil {
		return 0, err
	}
	path := blobstore.Key("out", id, "mime.eml", raw)
	if err := s.blobs.Put(path, raw); err != nil {
		return id, err
	}
	if _, err := s.exec(`UPDATE outbox SET raw_path = ? WHERE id = ?`, path, id); err != nil {
		return id, err
	}
	return id, nil
}

// DueOutbound returns queued messages whose retry time has come, including the
// MIME source so the caller can deliver them.
func (s *Store) DueOutbound(now int64, limit int) ([]*Outbound, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows []outboundRow
	if err := s.getAll(&rows,
		`SELECT `+outboxColumns+` FROM outbox
		 WHERE failed = 0 AND next_attempt <= ? ORDER BY id LIMIT ?`, now, limit); err != nil {
		return nil, err
	}
	out := outboundRows(rows)
	for _, item := range out {
		if err := s.loadOutboundRaw(item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListOutbound returns queued messages, newest first, without their payloads.
func (s *Store) ListOutbound(failedOnly bool, limit int) ([]*Outbound, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + outboxColumns + ` FROM outbox`
	if failedOnly {
		query += ` WHERE failed = 1`
	}
	query += ` ORDER BY id DESC LIMIT ?`
	var rows []outboundRow
	if err := s.getAll(&rows, query, limit); err != nil {
		return nil, err
	}
	return outboundRows(rows), nil
}

// loadOutboundRaw fills Raw from the blob directory.
func (s *Store) loadOutboundRaw(item *Outbound) error {
	if item == nil || item.RawPath == "" || len(item.Raw) > 0 {
		return nil
	}
	if s.blobs == nil {
		return errNoBlobDir
	}
	raw, err := s.blobs.Get(item.RawPath)
	if err != nil {
		return err
	}
	item.Raw = raw
	return nil
}

// RetryOutboundNow re-queues a message for immediate delivery.
func (s *Store) RetryOutboundNow(id int64) error {
	_, err := s.exec(
		`UPDATE outbox SET failed = 0, attempts = 0, next_attempt = 0, last_error = '' WHERE id = ?`,
		id)
	return err
}

// OutboundByID loads one queued message including its payload.
func (s *Store) OutboundByID(id int64) (*Outbound, error) {
	var row outboundRow
	if err := s.get(&row, `SELECT `+outboxColumns+` FROM outbox WHERE id = ?`, id); err != nil {
		return nil, err
	}
	item := row.outbound()
	if err := s.loadOutboundRaw(item); err != nil {
		return nil, err
	}
	return item, nil
}

// RetryOutbound records a failed attempt and schedules the next try.
func (s *Store) RetryOutbound(id int64, attempts int, nextAttempt int64, lastError string, failed bool) error {
	_, err := s.exec(
		`UPDATE outbox SET attempts = ?, next_attempt = ?, last_error = ?, failed = ? WHERE id = ?`,
		attempts, nextAttempt, lastError, boolInt(failed), id)
	return err
}

// DeleteOutbound removes a delivered message and its payload.
func (s *Store) DeleteOutbound(id int64) error {
	var path string
	err := s.get(&path, `SELECT raw_path FROM outbox WHERE id = ?`, id)
	if err != nil && err != ErrNotFound {
		return err
	}
	if _, err := s.exec(`DELETE FROM outbox WHERE id = ?`, id); err != nil {
		return err
	}
	if path != "" && s.blobs != nil {
		_ = s.blobs.Remove(path)
	}
	return nil
}

// CountOutbound reports how many messages are queued (including failed ones).
func (s *Store) CountOutbound(failed bool) (int, error) {
	var n int
	err := s.get(&n, `SELECT COUNT(*) FROM outbox WHERE failed = ?`, boolInt(failed))
	return n, err
}

func splitOutboxRecipients(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
