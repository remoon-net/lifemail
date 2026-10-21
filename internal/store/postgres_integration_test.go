//go:build integration

package store_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

// TestPostgresStore runs the paths that are otherwise only covered by SQLite
// against a real PostgreSQL server. It is excluded from the normal build so a
// developer machine does not need a database.
//
// Point it at a scratch database; the test applies migrations and removes the
// rows it creates:
//
//	LIFEMAIL_TEST_PG_DSN='postgres://lifemail:secret@localhost:5432/lifemail_test?sslmode=disable' \
//	  go test -tags integration ./internal/store/ -run Postgres -v
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("LIFEMAIL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LIFEMAIL_TEST_PG_DSN is not set")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if db.Dialect() != "postgres" {
		t.Fatalf("dialect = %q, want postgres", db.Dialect())
	}
	blobs, err := blobstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	status, err := db.MigrationStatus()
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	for _, m := range status {
		if !m.Applied {
			t.Fatalf("migration %04d_%s is pending after Open", m.Version, m.Name)
		}
	}

	name := fmt.Sprintf("pgtest%d", time.Now().UnixNano())
	user, err := db.CreateUser(name, name+"@example.test", "Postgres Test", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteUser(user.ID) })

	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	msg := &store.Message{
		UserID:            user.ID,
		FolderID:          inbox.ID,
		Subject:           "postgres",
		InternetMessageID: "<" + name + "@example.test>",
		Raw:               []byte("Subject: postgres\r\n\r\nbody"),
	}
	if _, err := db.InsertMessage(msg); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := db.InsertAttachment(&store.Attachment{
		MessageID: msg.ID, FileName: "a.txt", ContentType: "text/plain", Data: []byte("payload"),
	}); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	loaded, err := db.MessageByID(msg.ID)
	if err != nil {
		t.Fatalf("load message: %v", err)
	}
	if string(loaded.Raw) != "Subject: postgres\r\n\r\nbody" {
		t.Fatalf("raw = %q", loaded.Raw)
	}
	attachments, err := db.AttachmentsByMessage(msg.ID)
	if err != nil || len(attachments) != 1 {
		t.Fatalf("attachments = %d (%v)", len(attachments), err)
	}

	if _, err := db.CreateAlias(name+"@example.org", user.ID); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	resolved, err := db.UserByAddress(name + "+tag@example.org")
	if err != nil || resolved.ID != user.ID {
		t.Fatalf("plus addressing: %v (%v)", resolved, err)
	}

	if err := db.RecordDevice(user.ID, "device-1", "Test", "16.1", "127.0.0.1"); err != nil {
		t.Fatalf("record device: %v", err)
	}
	devices, err := db.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	found := false
	for _, device := range devices {
		if device.UserID == user.ID && device.DeviceID == "device-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("device was not listed")
	}

	calendar, err := db.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}
	if calendar.UIDValidity == 0 {
		t.Fatal("calendar has no UIDVALIDITY")
	}
	start := time.Now().Unix()
	event := &store.Event{
		UserID: user.ID, FolderID: calendar.ID, UID: "uid-" + name,
		Subject: "event", StartTime: start, EndTime: start + 3600,
	}
	if _, err := db.InsertEvent(event); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := db.ReplaceEventExceptions(event.ID, []store.Exception{{
		Occurrence: start, Subject: "moved", StartTime: start + 7200,
	}}); err != nil {
		t.Fatalf("replace exceptions: %v", err)
	}
	exceptions, err := db.EventExceptions(event.ID)
	if err != nil || len(exceptions) != 1 || exceptions[0].Subject != "moved" {
		t.Fatalf("exceptions = %+v (%v)", exceptions, err)
	}

	if err := db.SetSetting("test.key", "value"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	value, err := db.Setting("test.key")
	if err != nil || value != "value" {
		t.Fatalf("setting = %q (%v)", value, err)
	}

	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM users WHERE id = ?`,
		`SELECT COUNT(*) FROM messages WHERE user_id = ?`,
		`SELECT COUNT(*) FROM folders WHERE user_id = ?`,
	} {
		var n int
		if err := db.DB().Get(&n, sqlx.Rebind(sqlx.DOLLAR, query), user.ID); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if n != 0 {
			t.Fatalf("%s left %d rows behind", query, n)
		}
	}
}
