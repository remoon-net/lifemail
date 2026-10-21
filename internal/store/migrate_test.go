package store_test

import (
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

func TestMigrateAppliesEveryEmbeddedFileOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	status, err := store.MigrationStatus(path)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, m := range status {
		if !m.Applied {
			t.Fatalf("migration %04d_%s was not applied", m.Version, m.Name)
		}
	}
	db.Close()

	// Reopening must not run anything twice.
	again, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	status, err = store.MigrationStatus(path)
	if err != nil {
		t.Fatalf("status after reopen: %v", err)
	}
	for _, m := range status {
		if !m.Applied {
			t.Fatalf("migration %04d_%s lost its record", m.Version, m.Name)
		}
	}
}

func TestSchemaUsesForeignKeys(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.DB().Query(`PRAGMA foreign_key_list(messages)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	targets := map[string]bool{}
	for rows.Next() {
		var (
			id, seq                   int
			table, from, to           string
			onUpdate, onDelete, match string
		)
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		targets[table] = onDelete == "CASCADE"
	}
	if !targets["users"] || !targets["folders"] {
		t.Fatalf("messages foreign keys = %+v, want cascading users and folders", targets)
	}
}

func TestDeleteUserCascades(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "cascade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, err := blobstore.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	msg := &store.Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "hello",
		InternetMessageID: "<1@example.com>", Raw: []byte("Subject: hello\r\n\r\nbody"),
	}
	if _, err := db.InsertMessage(msg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertAttachment(&store.Attachment{
		MessageID: msg.ID, FileName: "a.txt", ContentType: "text/plain", Data: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("alice@example.org", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		`SELECT COUNT(*) FROM users`,
		`SELECT COUNT(*) FROM folders`,
		`SELECT COUNT(*) FROM messages`,
		`SELECT COUNT(*) FROM attachments`,
		`SELECT COUNT(*) FROM aliases`,
	} {
		var n int
		if err := db.DB().Get(&n, query); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s = %d after deleting the account, want 0", query, n)
		}
	}
}
