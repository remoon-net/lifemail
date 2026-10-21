package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remoon.net/lifemail/internal/settings"
)

func (ts *usersTestServer) addAlias(t *testing.T, address, userID string) adminAliasView {
	t.Helper()
	body := `{"address":"` + address + `","user_id":"` + userID + `"}`
	rec := ts.req(http.MethodPost, "/api/admin/aliases", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create alias status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Alias adminAliasView `json:"alias"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("create alias response: %v", err)
	}
	return out.Alias
}

func TestAdminAliasesLifecycle(t *testing.T) {
	ts := newUsersTestServer(t)
	runtime, err := settings.New(ts.db)
	if err != nil {
		t.Fatal(err)
	}
	domains := []settings.Domain{{Name: "example.com"}}
	if _, err := runtime.Update(settings.Patch{Domains: &domains}); err != nil {
		t.Fatal(err)
	}
	ts.h.Settings = runtime

	created := ts.create(t, `{"name":"alice","address":"alice@example.com","display_name":"Alice"}`)

	// Unauthenticated requests are rejected.
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/aliases", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	alias := ts.addAlias(t, "Info@Example.com", created.User.ID)
	if alias.Address != "info@example.com" || alias.UserID != created.User.ID {
		t.Fatalf("alias = %+v", alias)
	}

	// Aliases resolve to their owner, including case differences.
	owner, err := ts.db.UserByAddress("INFO@example.com")
	if err != nil || owner.ID != created.User.ID {
		t.Fatalf("UserByAddress = %+v, %v", owner, err)
	}

	// Duplicates conflict.
	rec = ts.req(http.MethodPost, "/api/admin/aliases", `{"address":"info@example.com","user_id":"`+created.User.ID+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate alias status = %d: %s", rec.Code, rec.Body.String())
	}

	// A real account cannot also be an alias.
	rec = ts.req(http.MethodPost, "/api/admin/aliases", `{"address":"alice@example.com","user_id":"`+created.User.ID+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("address collision status = %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown users and unknown domains are rejected.
	rec = ts.req(http.MethodPost, "/api/admin/aliases", `{"address":"x@example.com","user_id":"nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = ts.req(http.MethodPost, "/api/admin/aliases", `{"address":"x@other.test","user_id":"`+created.User.ID+`"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("unknown domain status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = ts.req(http.MethodPost, "/api/admin/aliases", `{"address":"bad","user_id":"`+created.User.ID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid address status = %d: %s", rec.Code, rec.Body.String())
	}

	// Listing includes the alias.
	rec = ts.req(http.MethodGet, "/api/admin/aliases", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"info@example.com"`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// Deleting is idempotent only once, and removes resolution.
	rec = ts.req(http.MethodDelete, "/api/admin/aliases/info%40example.com", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	rec = ts.req(http.MethodDelete, "/api/admin/aliases/info%40example.com", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
	if _, err := ts.db.UserByAddress("info@example.com"); err == nil {
		t.Fatal("alias still resolves after delete")
	}

	// Removing the account removes its aliases.
	ts.addAlias(t, "sales@example.com", created.User.ID)
	rec = ts.req(http.MethodDelete, "/api/admin/users/"+created.User.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete user = %d: %s", rec.Code, rec.Body.String())
	}
	list, err := ts.db.ListAliases()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("aliases left behind: %+v", list)
	}
}
