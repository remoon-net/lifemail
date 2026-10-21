package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/store"
)

func newMeFixture(t *testing.T) (*Me, *store.Store, store.User, *store.Folder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "me.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	user, err := st.CreateUser("alice", "alice@example.com", "Alice", string(hash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	cal, err := st.FolderByType(user.ID, store.FolderTypeCalendar)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}
	return &Me{Store: st, BaseURL: "https://mail.example.com/"}, st, *user, cal
}

func doMe(t *testing.T, h *Me, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		req.SetBasicAuth("alice@example.com", "secret")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMeRequiresBasicAuth(t *testing.T) {
	h, _, _, _ := newMeFixture(t)

	rec := doMe(t, h, http.MethodGet, "/api/me/calendars", "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("401 must carry a WWW-Authenticate challenge")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me/calendars", nil)
	req.SetBasicAuth("alice@example.com", "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password status = %d, want 401", rec.Code)
	}
}

func TestMeListsCalendarsWithSubscribeURL(t *testing.T) {
	h, _, _, cal := newMeFixture(t)

	rec := doMe(t, h, http.MethodGet, "/api/me/calendars", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Calendars []struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			Public       bool   `json:"public"`
			SubscribeURL string `json:"subscribeUrl"`
		} `json:"calendars"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Calendars) != 1 {
		t.Fatalf("calendars = %+v, want exactly one", payload.Calendars)
	}
	got := payload.Calendars[0]
	if got.ID != cal.ID || got.Public {
		t.Fatalf("calendar = %+v, want private id %d", got, cal.ID)
	}
	if want := "https://mail.example.com/calendar/" + cal.UUID + ".ics"; got.SubscribeURL != want {
		t.Fatalf("subscribe url = %q, want %q", got.SubscribeURL, want)
	}
}

func TestMeTogglesPublicFlag(t *testing.T) {
	h, st, _, cal := newMeFixture(t)
	path := "/api/me/calendars/" + itoa(cal.ID) + "/public"

	rec := doMe(t, h, http.MethodPut, path, `{"public":true}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status = %d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := st.FolderByID(cal.ID)
	if err != nil || !stored.Public {
		t.Fatalf("folder public = %v (%v)", stored.Public, err)
	}

	rec = doMe(t, h, http.MethodPut, path, `{"public":false}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("unpublish status = %d body=%s", rec.Code, rec.Body.String())
	}
	if stored, _ := st.FolderByID(cal.ID); stored.Public {
		t.Fatal("calendar should be private again")
	}
}

func TestMeRejectsMalformedToggle(t *testing.T) {
	h, _, _, cal := newMeFixture(t)
	path := "/api/me/calendars/" + itoa(cal.ID) + "/public"
	for _, body := range []string{`{}`, `{"public":"yes"}`, `not json`} {
		if rec := doMe(t, h, http.MethodPut, path, body, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want 400", body, rec.Code)
		}
	}
	if rec := doMe(t, h, http.MethodPut, "/api/me/calendars/abc/public", `{"public":true}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric id status = %d, want 400", rec.Code)
	}
}

func TestMeCannotTouchAnotherUsersCalendar(t *testing.T) {
	h, st, _, _ := newMeFixture(t)
	other, err := st.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	bobCal, err := st.FolderByType(other.ID, store.FolderTypeCalendar)
	if err != nil {
		t.Fatalf("bob calendar: %v", err)
	}

	rec := doMe(t, h, http.MethodPut,
		"/api/me/calendars/"+itoa(bobCal.ID)+"/public", `{"public":true}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if stored, _ := st.FolderByID(bobCal.ID); stored.Public {
		t.Fatal("another user's calendar was published")
	}
}

func TestMeRejectsMailFolder(t *testing.T) {
	h, st, user, _ := newMeFixture(t)
	inbox, err := st.FolderByName(user.ID, "Inbox")
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	rec := doMe(t, h, http.MethodPut,
		"/api/me/calendars/"+itoa(inbox.ID)+"/public", `{"public":true}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
