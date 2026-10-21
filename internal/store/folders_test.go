package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCreateUserProvisionsPresetFolders(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "folders.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	junk, err := db.FolderByType(user.ID, 12)
	if err != nil {
		t.Fatalf("junk folder: %v", err)
	}
	if junk.Name != "Spam" || junk.Class != "Email" {
		t.Fatalf("junk folder = %+v", junk)
	}
	if !IsSystemFolder(12) {
		t.Fatal("Spam must be treated as a system folder")
	}
	folders, err := db.FoldersByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 8 {
		t.Fatalf("preset folders = %d, want 8", len(folders))
	}
}

func TestCalendarFolderGetsStableUUID(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "uuid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	cal, err := db.FolderByType(user.ID, FolderTypeCalendar)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}
	if len(cal.UUID) != 36 {
		t.Fatalf("calendar uuid = %q, want a 36-character uuid", cal.UUID)
	}
	if cal.Public {
		t.Fatal("calendars must start private")
	}

	// Renaming must not change the subscription URL.
	if err := db.RenameFolder(cal.ID, "工作"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed, err := db.FolderByUUID(cal.UUID)
	if err != nil || renamed.ID != cal.ID || renamed.Name != "工作" {
		t.Fatalf("lookup after rename = %+v (%v)", renamed, err)
	}

	// Non-calendar folders get no feed identifier at all.
	inbox, err := db.FolderByName(user.ID, "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if inbox.UUID != "" {
		t.Fatalf("inbox uuid = %q, want empty", inbox.UUID)
	}
}

func TestSetFolderPublicRejectsNonCalendars(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "public.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.FolderByName(user.ID, "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetFolderPublic(inbox.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publishing a mail folder = %v, want ErrNotFound", err)
	}
}
