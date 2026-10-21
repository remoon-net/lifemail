-- Initial schema.
--
-- This project does not carry history from earlier releases: the first
-- migration describes the database as if it were designed today. Later
-- changes belong in new, numbered migration files.

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  -- Login name, stored lowercase. Accounts are not tied to a domain.
  name TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  -- Per-account counter used by FolderSync.
  folder_seq INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- 0 marks a root folder; SQLite cannot express "NULL or existing row".
  parent_id INTEGER NOT NULL DEFAULT 0,
  name TEXT NOT NULL,
  type INTEGER NOT NULL DEFAULT 1,
  class_name TEXT NOT NULL DEFAULT 'Email',
  seq INTEGER NOT NULL DEFAULT 0,
  -- IMAP UIDVALIDITY. Never changes for a folder once assigned.
  uid_validity INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  calendar_uuid TEXT NOT NULL DEFAULT '',
  calendar_public INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX idx_folders_unique ON folders(user_id, parent_id, name);
CREATE INDEX idx_folders_user ON folders(user_id, deleted);
CREATE UNIQUE INDEX idx_folders_calendar_uuid ON folders(calendar_uuid)
  WHERE calendar_uuid <> '';

CREATE TABLE messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  subject TEXT NOT NULL DEFAULT '',
  from_addr TEXT NOT NULL DEFAULT '',
  from_name TEXT NOT NULL DEFAULT '',
  to_addr TEXT NOT NULL DEFAULT '',
  cc_addr TEXT NOT NULL DEFAULT '',
  date_received INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0,
  is_read INTEGER NOT NULL DEFAULT 0,
  is_flagged INTEGER NOT NULL DEFAULT 0,
  importance INTEGER NOT NULL DEFAULT 1,
  message_class TEXT NOT NULL DEFAULT 'IPM.Note',
  internet_message_id TEXT NOT NULL DEFAULT '',
  body_text TEXT NOT NULL DEFAULT '',
  body_html TEXT NOT NULL DEFAULT '',
  -- Blob key of the MIME source. The payload never lives in the database.
  raw_path TEXT NOT NULL DEFAULT '',
  has_attachments INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  sender_addr TEXT NOT NULL DEFAULT '',
  sender_name TEXT NOT NULL DEFAULT '',
  received_as_bcc INTEGER NOT NULL DEFAULT 0,
  conversation_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_messages_folder ON messages(folder_id, deleted, date_received, id);
CREATE INDEX idx_messages_user ON messages(user_id, date_received);
CREATE INDEX idx_messages_msgid ON messages(user_id, internet_message_id);
CREATE INDEX idx_messages_conversation ON messages(user_id, conversation_id);

CREATE TABLE attachments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  file_name TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  content_id TEXT NOT NULL DEFAULT '',
  is_inline INTEGER NOT NULL DEFAULT 0,
  path TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_attachments_message ON attachments(message_id, id);

CREATE TABLE contacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  first_name TEXT NOT NULL DEFAULT '',
  last_name TEXT NOT NULL DEFAULT '',
  middle_name TEXT NOT NULL DEFAULT '',
  file_as TEXT NOT NULL DEFAULT '',
  company_name TEXT NOT NULL DEFAULT '',
  department TEXT NOT NULL DEFAULT '',
  job_title TEXT NOT NULL DEFAULT '',
  email1 TEXT NOT NULL DEFAULT '',
  email2 TEXT NOT NULL DEFAULT '',
  email3 TEXT NOT NULL DEFAULT '',
  home_phone TEXT NOT NULL DEFAULT '',
  home2_phone TEXT NOT NULL DEFAULT '',
  mobile_phone TEXT NOT NULL DEFAULT '',
  business_phone TEXT NOT NULL DEFAULT '',
  business_fax TEXT NOT NULL DEFAULT '',
  home_street TEXT NOT NULL DEFAULT '',
  home_city TEXT NOT NULL DEFAULT '',
  home_state TEXT NOT NULL DEFAULT '',
  home_postal_code TEXT NOT NULL DEFAULT '',
  home_country TEXT NOT NULL DEFAULT '',
  business_street TEXT NOT NULL DEFAULT '',
  business_city TEXT NOT NULL DEFAULT '',
  business_state TEXT NOT NULL DEFAULT '',
  business_postal_code TEXT NOT NULL DEFAULT '',
  business_country TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  deleted INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_contacts_folder ON contacts(folder_id, deleted, file_as);

CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  uid TEXT NOT NULL DEFAULT '',
  subject TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  start_time INTEGER NOT NULL DEFAULT 0,
  end_time INTEGER NOT NULL DEFAULT 0,
  dtstamp INTEGER NOT NULL DEFAULT 0,
  all_day INTEGER NOT NULL DEFAULT 0,
  busy_status INTEGER NOT NULL DEFAULT 2,
  sensitivity INTEGER NOT NULL DEFAULT 0,
  meeting_status INTEGER NOT NULL DEFAULT 0,
  reminder INTEGER NOT NULL DEFAULT 15,
  organizer_name TEXT NOT NULL DEFAULT '',
  organizer_email TEXT NOT NULL DEFAULT '',
  -- JSON array of Attendee.
  attendees TEXT NOT NULL DEFAULT '[]',
  deleted INTEGER NOT NULL DEFAULT 0,
  -- Repeat rule, mirrored from MS-ASCAL. interval = 0 means "does not repeat".
  recurrence_type INTEGER NOT NULL DEFAULT 0,
  recurrence_interval INTEGER NOT NULL DEFAULT 0,
  recurrence_until INTEGER NOT NULL DEFAULT 0,
  recurrence_occurrences INTEGER NOT NULL DEFAULT 0,
  recurrence_day_of_week INTEGER NOT NULL DEFAULT 0,
  recurrence_day_of_month INTEGER NOT NULL DEFAULT 0,
  recurrence_week_of_month INTEGER NOT NULL DEFAULT 0,
  recurrence_month_of_year INTEGER NOT NULL DEFAULT 0,
  -- Revision counters consumed by iCalendar subscribers.
  sequence INTEGER NOT NULL DEFAULT 0,
  last_modified INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_events_folder ON events(folder_id, deleted, start_time, id);
CREATE INDEX idx_events_uid ON events(user_id, uid);

-- One row per occurrence that differs from its series. Columns instead of a
-- JSON document so single-occurrence edits are a plain UPSERT.
CREATE TABLE event_exceptions (
  event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  -- Start of the overridden occurrence (the value clients send).
  occurrence INTEGER NOT NULL,
  deleted INTEGER NOT NULL DEFAULT 0,
  subject TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  start_time INTEGER NOT NULL DEFAULT 0,
  end_time INTEGER NOT NULL DEFAULT 0,
  all_day INTEGER NOT NULL DEFAULT 0,
  busy_status INTEGER NOT NULL DEFAULT 0,
  sensitivity INTEGER NOT NULL DEFAULT 0,
  reminder INTEGER NOT NULL DEFAULT 0,
  meeting_status INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (event_id, occurrence)
);

CREATE TABLE changes (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL,
  item_id INTEGER NOT NULL,
  kind TEXT NOT NULL
);

CREATE INDEX idx_changes_folder ON changes(folder_id, seq);

CREATE TABLE sync_states (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  sync_key TEXT NOT NULL,
  seq INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, device_id, folder_id)
);

CREATE TABLE sync_items (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL,
  PRIMARY KEY (user_id, device_id, folder_id, item_id)
);

CREATE TABLE devices (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL,
  device_type TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  policy_key TEXT NOT NULL DEFAULT '',
  last_seen INTEGER NOT NULL DEFAULT 0,
  wipe_requested INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  app_version TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, device_id)
);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE aliases (
  -- Stored lowercase; the column is the identity, so no lower() lookups.
  address TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  is_primary INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_aliases_user ON aliases(user_id);
CREATE UNIQUE INDEX idx_aliases_primary ON aliases(user_id) WHERE is_primary = 1;

CREATE TABLE outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  envelope_from TEXT NOT NULL DEFAULT '',
  recipients TEXT NOT NULL DEFAULT '',
  -- Blob key of the MIME source, like messages.raw_path.
  raw_path TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  next_attempt INTEGER NOT NULL DEFAULT 0,
  failed INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_outbox_due ON outbox(failed, next_attempt, id);
