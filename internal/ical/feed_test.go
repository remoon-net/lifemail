package ical

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"remoon.net/lifemail/internal/store"
)

func newFeedFixture(t *testing.T) (*store.Store, store.User, *store.Folder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	user, err := st.CreateUser("feed", "feed@example.com", "Feed", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	folder, err := st.FolderByType(user.ID, store.FolderTypeCalendar)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}
	if folder.UUID == "" {
		t.Fatal("calendar folder has no feed uuid")
	}
	return st, *user, folder
}

func fetchFeed(t *testing.T, h Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFeedHiddenUntilPublic(t *testing.T) {
	st, _, folder := newFeedFixture(t)
	h := Handler{Store: st}

	rec := fetchFeed(t, h, "/calendar/"+folder.UUID+".ics")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private feed status = %d, want 404", rec.Code)
	}

	if err := st.SetFolderPublic(folder.ID, true); err != nil {
		t.Fatalf("publish: %v", err)
	}
	rec = fetchFeed(t, h, "/calendar/"+folder.UUID+".ics")
	if rec.Code != http.StatusOK {
		t.Fatalf("public feed status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Fatalf("content type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
		t.Fatalf("unexpected envelope:\n%s", body)
	}
}

func TestFeedUnknownUUIDIs404(t *testing.T) {
	st, _, _ := newFeedFixture(t)
	h := Handler{Store: st}
	for _, path := range []string{
		"/calendar/nope.ics",
		"/calendar/.ics",
		"/calendar/abc",
		"/calendar/",
	} {
		if rec := fetchFeed(t, h, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestFeedOnlyContainsItsOwnCalendar(t *testing.T) {
	st, user, first := newFeedFixture(t)
	second, err := st.CreateFolder(user.ID, 0, "工作", store.FolderTypeCalendar)
	if err != nil {
		t.Fatalf("create second calendar: %v", err)
	}
	if second.UUID == "" || second.UUID == first.UUID {
		t.Fatalf("second calendar uuid = %q", second.UUID)
	}

	if _, err := st.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: first.ID, UID: "a@x", Subject: "in first",
		StartTime: 1700000000, EndTime: 1700000600,
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := st.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: second.ID, UID: "b@x", Subject: "in second",
		StartTime: 1700000000, EndTime: 1700000600,
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := st.SetFolderPublic(first.ID, true); err != nil {
		t.Fatalf("publish: %v", err)
	}

	body := fetchFeed(t, Handler{Store: st}, "/calendar/"+first.UUID+".ics").Body.String()
	if !strings.Contains(body, "SUMMARY:in first") {
		t.Fatalf("first calendar missing its event:\n%s", body)
	}
	if strings.Contains(body, "SUMMARY:in second") {
		t.Fatalf("feed leaked the other calendar:\n%s", body)
	}
}

func TestFeedIncludesOccurrenceOverrides(t *testing.T) {
	st, user, folder := newFeedFixture(t)
	id, err := st.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: folder.ID, UID: "series@x", Subject: "Weekly",
		StartTime: 1700000000, EndTime: 1700003600,
		Recurrence: store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 8},
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := st.ReplaceEventExceptions(id, []store.Exception{
		{Occurrence: 1700604800, Deleted: true},
		{Occurrence: 1701209600, Subject: "Weekly (moved)", StartTime: 1701213200, EndTime: 1701216800},
	}); err != nil {
		t.Fatalf("exceptions: %v", err)
	}
	if err := st.SetFolderPublic(folder.ID, true); err != nil {
		t.Fatalf("publish: %v", err)
	}

	body := fetchFeed(t, Handler{Store: st}, "/calendar/"+folder.UUID+".ics").Body.String()
	if !strings.Contains(body, "EXDATE:") {
		t.Fatalf("cancelled occurrence not excluded:\n%s", body)
	}
	if !strings.Contains(body, "RECURRENCE-ID:") {
		t.Fatalf("rescheduled occurrence not overridden:\n%s", body)
	}
	if !strings.Contains(body, "SUMMARY:Weekly (moved)") {
		t.Fatalf("override subject missing:\n%s", body)
	}
}
