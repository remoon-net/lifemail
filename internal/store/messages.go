package store

import (
	"remoon.net/lifemail/internal/blobstore"
)

// Message is a stored email. The MIME source lives in the blob directory and
// is loaded on demand.
type Message struct {
	ID                int64
	UserID            string
	FolderID          int64
	Subject           string
	FromAddr          string
	FromName          string
	ToAddr            string
	CcAddr            string
	DateReceived      int64
	Size              int64
	IsRead            bool
	IsFlagged         bool
	Importance        int
	MessageClass      string
	InternetMessageID string
	BodyText          string
	BodyHTML          string
	Raw               []byte
	// RawPath is the blob key holding the MIME source.
	RawPath        string
	HasAttachments bool
	Deleted        bool

	// SenderAddr is the Sender header: set when the message was sent by
	// someone other than the From address (a delegate). Empty otherwise.
	SenderAddr string
	SenderName string
	// ReceivedAsBcc is set when this account got the message through Bcc, so
	// it does not appear in the To or Cc headers.
	ReceivedAsBcc bool
	// ConversationID is shared by every message of one thread.
	ConversationID string
}

type messageRow struct {
	ID                int64  `db:"id"`
	UserID            string `db:"user_id"`
	FolderID          int64  `db:"folder_id"`
	Subject           string `db:"subject"`
	FromAddr          string `db:"from_addr"`
	FromName          string `db:"from_name"`
	ToAddr            string `db:"to_addr"`
	CcAddr            string `db:"cc_addr"`
	DateReceived      int64  `db:"date_received"`
	Size              int64  `db:"size"`
	IsRead            int    `db:"is_read"`
	IsFlagged         int    `db:"is_flagged"`
	Importance        int    `db:"importance"`
	MessageClass      string `db:"message_class"`
	InternetMessageID string `db:"internet_message_id"`
	BodyText          string `db:"body_text"`
	BodyHTML          string `db:"body_html"`
	RawPath           string `db:"raw_path"`
	HasAttachments    int    `db:"has_attachments"`
	Deleted           int    `db:"deleted"`
	SenderAddr        string `db:"sender_addr"`
	SenderName        string `db:"sender_name"`
	ReceivedAsBcc     int    `db:"received_as_bcc"`
	ConversationID    string `db:"conversation_id"`
}

func (r messageRow) message() *Message {
	return &Message{
		ID: r.ID, UserID: r.UserID, FolderID: r.FolderID, Subject: r.Subject,
		FromAddr: r.FromAddr, FromName: r.FromName, ToAddr: r.ToAddr, CcAddr: r.CcAddr,
		DateReceived: r.DateReceived, Size: r.Size, IsRead: r.IsRead != 0,
		IsFlagged: r.IsFlagged != 0, Importance: r.Importance, MessageClass: r.MessageClass,
		InternetMessageID: r.InternetMessageID, BodyText: r.BodyText, BodyHTML: r.BodyHTML,
		RawPath: r.RawPath, HasAttachments: r.HasAttachments != 0, Deleted: r.Deleted != 0,
		SenderAddr: r.SenderAddr, SenderName: r.SenderName, ReceivedAsBcc: r.ReceivedAsBcc != 0,
		ConversationID: r.ConversationID,
	}
}

const messageColumns = `id, user_id, folder_id, subject, from_addr, from_name, to_addr, cc_addr,
	date_received, size, is_read, is_flagged, importance, message_class, internet_message_id,
	body_text, body_html, raw_path, has_attachments, deleted,
	sender_addr, sender_name, received_as_bcc, conversation_id`

const messageSelect = `SELECT ` + messageColumns + ` FROM messages`

func messageRows(rows []messageRow) []*Message {
	out := make([]*Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.message())
	}
	return out
}

// InsertMessage stores a message and returns its server id.
func (s *Store) InsertMessage(m *Message) (int64, error) {
	if m.MessageClass == "" {
		m.MessageClass = "IPM.Note"
	}
	if m.Importance == 0 {
		m.Importance = 1
	}
	if m.DateReceived == 0 {
		m.DateReceived = nowUnix()
	}
	if s.blobs == nil {
		return 0, errNoBlobDir
	}
	if err := s.get(&m.ID,
		`INSERT INTO messages (user_id, folder_id, subject, from_addr, from_name, to_addr,
		 cc_addr, date_received, size, is_read, is_flagged, importance, message_class,
		 internet_message_id, body_text, body_html, has_attachments, deleted,
		 sender_addr, sender_name, received_as_bcc, conversation_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		m.UserID, m.FolderID, m.Subject, m.FromAddr, m.FromName, m.ToAddr, m.CcAddr,
		m.DateReceived, m.Size, boolInt(m.IsRead), boolInt(m.IsFlagged), m.Importance,
		m.MessageClass, m.InternetMessageID, m.BodyText, m.BodyHTML,
		boolInt(m.HasAttachments), boolInt(m.Deleted),
		m.SenderAddr, m.SenderName, boolInt(m.ReceivedAsBcc), m.ConversationID); err != nil {
		return 0, err
	}
	if len(m.Raw) == 0 {
		return m.ID, nil
	}
	path := blobstore.Key("raw", m.ID, "mime.eml", m.Raw)
	if err := s.blobs.Put(path, m.Raw); err != nil {
		return m.ID, err
	}
	if _, err := s.exec(`UPDATE messages SET raw_path = ? WHERE id = ?`, path, m.ID); err != nil {
		return m.ID, err
	}
	m.RawPath = path
	return m.ID, nil
}

// UpdateMessage persists mutable message fields. The MIME source is never
// touched here; it lives in the blob directory.
func (s *Store) UpdateMessage(m *Message) error {
	_, err := s.exec(
		`UPDATE messages SET folder_id = ?, subject = ?, from_addr = ?, from_name = ?,
		 to_addr = ?, cc_addr = ?, date_received = ?, size = ?, is_read = ?, is_flagged = ?,
		 importance = ?, message_class = ?, internet_message_id = ?, body_text = ?,
		 body_html = ?, has_attachments = ?, deleted = ? WHERE id = ?`,
		m.FolderID, m.Subject, m.FromAddr, m.FromName, m.ToAddr, m.CcAddr,
		m.DateReceived, m.Size, boolInt(m.IsRead), boolInt(m.IsFlagged), m.Importance,
		m.MessageClass, m.InternetMessageID, m.BodyText, m.BodyHTML,
		boolInt(m.HasAttachments), boolInt(m.Deleted), m.ID)
	return err
}

// MessageByID fetches one message, including its MIME source.
func (s *Store) MessageByID(id int64) (*Message, error) {
	var row messageRow
	if err := s.get(&row, messageSelect+` WHERE id = ?`, id); err != nil {
		return nil, err
	}
	m := row.message()
	if _, err := s.LoadRaw(m); err != nil {
		return nil, err
	}
	return m, nil
}

// MessageMetaByID fetches one message without reading its MIME source.
func (s *Store) MessageMetaByID(id int64) (*Message, error) {
	var row messageRow
	if err := s.get(&row, messageSelect+` WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return row.message(), nil
}

// LoadRaw makes sure m.Raw holds the MIME source, reading it from the blob
// directory.
func (s *Store) LoadRaw(m *Message) ([]byte, error) {
	if m == nil {
		return nil, ErrNotFound
	}
	if len(m.Raw) > 0 || m.RawPath == "" {
		return m.Raw, nil
	}
	if s.blobs == nil {
		return nil, errNoBlobDir
	}
	raw, err := s.blobs.Get(m.RawPath)
	if err != nil {
		return nil, err
	}
	m.Raw = raw
	return raw, nil
}

// MessagesForIMAP returns live messages ordered by ascending id (UID order).
func (s *Store) MessagesForIMAP(folderID int64) ([]*Message, error) {
	var rows []messageRow
	if err := s.getAll(&rows,
		messageSelect+` WHERE folder_id = ? AND deleted = 0 ORDER BY id`, folderID); err != nil {
		return nil, err
	}
	return messageRows(rows), nil
}

// NextUID returns one greater than the largest message id ever used in a
// folder, including soft-deleted rows so UIDs are never reused.
func (s *Store) NextUID(folderID int64) (int64, error) {
	var max *int64
	if err := s.get(&max, `SELECT MAX(id) FROM messages WHERE folder_id = ?`, folderID); err != nil {
		return 0, err
	}
	if max == nil {
		return 1, nil
	}
	return *max + 1, nil
}

// CountUnseen counts live messages without the \Seen flag.
func (s *Store) CountUnseen(folderID int64) (int, error) {
	var n int
	err := s.get(&n,
		`SELECT count(*) FROM messages WHERE folder_id = ? AND deleted = 0 AND is_read = 0`, folderID)
	return n, err
}

// MessagesByFolder returns live messages in a folder, newest first.
func (s *Store) MessagesByFolder(folderID int64, limit int) ([]*Message, error) {
	q := messageSelect + ` WHERE folder_id = ? AND deleted = 0 ORDER BY date_received DESC, id DESC`
	args := []any{folderID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	var rows []messageRow
	if err := s.getAll(&rows, q, args...); err != nil {
		return nil, err
	}
	return messageRows(rows), nil
}

// CountMessagesInFolder counts live messages.
func (s *Store) CountMessagesInFolder(folderID int64) (int, error) {
	var n int
	err := s.get(&n, `SELECT count(*) FROM messages WHERE folder_id = ? AND deleted = 0`, folderID)
	return n, err
}

// MessageIDExists reports whether a user already has a message with the given
// RFC 5322 Message-ID.
func (s *Store) MessageIDExists(userID, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var n int
	if err := s.get(&n,
		`SELECT count(*) FROM messages WHERE user_id = ? AND internet_message_id = ?`,
		userID, messageID); err != nil {
		return false, err
	}
	return n > 0, nil
}

// SoftDeleteMessage marks a message deleted without dropping its data.
func (s *Store) SoftDeleteMessage(id int64) error {
	_, err := s.exec(`UPDATE messages SET deleted = 1 WHERE id = ?`, id)
	return err
}

// MoveMessage changes a message's folder.
func (s *Store) MoveMessage(id, folderID int64) error {
	_, err := s.exec(`UPDATE messages SET folder_id = ? WHERE id = ?`, folderID, id)
	return err
}

// MarkMessageRead updates the read flag.
func (s *Store) MarkMessageRead(id int64, read bool) error {
	_, err := s.exec(`UPDATE messages SET is_read = ? WHERE id = ?`, boolInt(read), id)
	return err
}

// Attachment is a MIME part stored alongside a message.
type Attachment struct {
	ID          int64
	MessageID   int64
	FileName    string
	ContentType string
	Size        int64
	ContentID   string
	IsInline    bool
	// Path is the blob key holding the payload.
	Path string
	Data []byte
}

type attachmentRow struct {
	ID          int64  `db:"id"`
	MessageID   int64  `db:"message_id"`
	FileName    string `db:"file_name"`
	ContentType string `db:"content_type"`
	Size        int64  `db:"size"`
	ContentID   string `db:"content_id"`
	IsInline    int    `db:"is_inline"`
	Path        string `db:"path"`
}

func (r attachmentRow) attachment() *Attachment {
	return &Attachment{
		ID: r.ID, MessageID: r.MessageID, FileName: r.FileName, ContentType: r.ContentType,
		Size: r.Size, ContentID: r.ContentID, IsInline: r.IsInline != 0, Path: r.Path,
	}
}

// InsertAttachment writes the payload to the blob directory and records its
// metadata.
func (s *Store) InsertAttachment(a *Attachment) (int64, error) {
	if s.blobs == nil {
		return 0, errNoBlobDir
	}
	path := blobstore.Key("att", a.MessageID, a.FileName, a.Data)
	if err := s.blobs.Put(path, a.Data); err != nil {
		return 0, err
	}
	if err := s.get(&a.ID,
		`INSERT INTO attachments (message_id, file_name, content_type, size, content_id, is_inline, path)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		a.MessageID, a.FileName, a.ContentType, int64(len(a.Data)), a.ContentID,
		boolInt(a.IsInline), path); err != nil {
		return 0, err
	}
	a.Path = path
	return a.ID, nil
}

// AttachmentsByMessage lists attachment metadata (without payloads).
func (s *Store) AttachmentsByMessage(messageID int64) ([]*Attachment, error) {
	var rows []attachmentRow
	if err := s.getAll(&rows,
		`SELECT id, message_id, file_name, content_type, size, content_id, is_inline, path
		 FROM attachments WHERE message_id = ? ORDER BY id`, messageID); err != nil {
		return nil, err
	}
	out := make([]*Attachment, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.attachment())
	}
	return out, nil
}

// AttachmentByID fetches an attachment including its payload.
func (s *Store) AttachmentByID(id int64) (*Attachment, error) {
	var row attachmentRow
	if err := s.get(&row,
		`SELECT id, message_id, file_name, content_type, size, content_id, is_inline, path
		 FROM attachments WHERE id = ?`, id); err != nil {
		return nil, err
	}
	a := row.attachment()
	if a.Path == "" {
		return a, nil
	}
	if s.blobs == nil {
		return nil, errNoBlobDir
	}
	stored, err := s.blobs.Get(a.Path)
	if err != nil {
		return nil, err
	}
	a.Data = stored
	return a, nil
}
