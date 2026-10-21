package store

import (
	"path/filepath"
	"testing"
)

func revFixture(t *testing.T) (*Store, *Event) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "rev.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	user, err := db.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	folder, err := db.FolderByType(user.ID, FolderTypeCalendar)
	if err != nil {
		t.Fatalf("calendar: %v", err)
	}
	id, err := db.InsertEvent(&Event{
		UserID: user.ID, FolderID: folder.ID, UID: "series@x", Subject: "Weekly",
		StartTime: 1700000000, EndTime: 1700003600, DtStamp: 1700000000,
		Recurrence: Recurrence{Type: 1, Interval: 1, DayOfWeek: 8},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	event, err := db.EventByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return db, event
}

func TestUpdateEventBumpsRevision(t *testing.T) {
	db, event := revFixture(t)
	if event.Sequence != 0 {
		t.Fatalf("fresh event sequence = %d, want 0", event.Sequence)
	}
	if event.LastModified != event.DtStamp {
		t.Fatalf("last modified = %d, want the dtstamp %d",
			event.LastModified, event.DtStamp)
	}

	event.Subject = "Weekly (moved)"
	if err := db.UpdateEvent(event); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := db.EventByID(event.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Sequence != 1 {
		t.Fatalf("sequence after one edit = %d, want 1", after.Sequence)
	}
	if after.LastModified < event.LastModified {
		t.Fatalf("last modified went backwards: %d -> %d",
			event.LastModified, after.LastModified)
	}

	after.Subject = "Weekly (moved again)"
	if err := db.UpdateEvent(after); err != nil {
		t.Fatalf("second update: %v", err)
	}
	again, _ := db.EventByID(event.ID)
	if again.Sequence != 2 {
		t.Fatalf("sequence after two edits = %d, want 2", again.Sequence)
	}
}

func TestExceptionChangeBumpsRevision(t *testing.T) {
	db, event := revFixture(t)
	base := event.Sequence

	if err := db.ReplaceEventExceptions(event.ID, []Exception{
		{Occurrence: 1700604800, Deleted: true},
	}); err != nil {
		t.Fatalf("replace exceptions: %v", err)
	}
	after, err := db.EventByID(event.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Sequence != base+1 {
		t.Fatalf("sequence = %d, want %d after adding an override", after.Sequence, base+1)
	}

	if err := db.ReplaceEventExceptions(event.ID, nil); err != nil {
		t.Fatalf("clear exceptions: %v", err)
	}
	cleared, _ := db.EventByID(event.ID)
	if cleared.Sequence != base+2 {
		t.Fatalf("sequence = %d, want %d after clearing overrides", cleared.Sequence, base+2)
	}
}

func TestExceptionsByFolderSeparatesCalendars(t *testing.T) {
	db, event := revFixture(t)
	user, err := db.UserByAddress("bob@example.com")
	if err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	other, err := db.CreateFolder(user.ID, 0, "Work", FolderTypeCalendar)
	if err != nil {
		t.Fatalf("second calendar: %v", err)
	}
	otherID, err := db.InsertEvent(&Event{
		UserID: user.ID, FolderID: other.ID, UID: "other@x", Subject: "Other",
		StartTime: 1700000000, EndTime: 1700003600,
	})
	if err != nil {
		t.Fatalf("insert other: %v", err)
	}
	if err := db.ReplaceEventExceptions(event.ID, []Exception{{Occurrence: 1700604800}}); err != nil {
		t.Fatalf("exceptions: %v", err)
	}

	got, err := db.ExceptionsByFolder(event.FolderID)
	if err != nil {
		t.Fatalf("by folder: %v", err)
	}
	if len(got[event.ID]) != 1 {
		t.Fatalf("event overrides = %v, want one", got[event.ID])
	}
	if _, leaked := got[otherID]; leaked {
		t.Fatal("other calendar's event appeared in the result")
	}
}
