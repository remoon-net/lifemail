package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/settings"
	"remoon.net/lifemail/internal/store"
)

type usersTestServer struct {
	h   *Admin
	db  *store.Store
	req func(method, path, body string) *httptest.ResponseRecorder
}

func newUsersTestServer(t *testing.T) *usersTestServer {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, blobErr := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	db.SetBlobs(blobs)
	runtime, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	h := &Admin{
		Settings: runtime,
		Store:    db,
		Token:    "secret",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	return &usersTestServer{h: h, db: db, req: do}
}

func (ts *usersTestServer) create(t *testing.T, body string) adminUserResponse {
	t.Helper()
	rec := ts.req(http.MethodPost, "/api/admin/users", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var out adminUserResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("create response: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestAdminUsersLifecycle(t *testing.T) {
	ts := newUsersTestServer(t)

	// Unauthenticated requests are rejected.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	// Creation generates a password when none is supplied.
	created := ts.create(t, `{"name":"Bob","address":"Bob@Example.com","display_name":"Bob"}`)
	if created.User.Name != "bob" || created.User.Address != "bob@example.com" || created.User.DisplayName != "Bob" {
		t.Fatalf("created user = %+v", created.User)
	}
	if created.Password == "" {
		t.Fatal("expected a generated password")
	}
	user, err := ts.db.UserByName("bob")
	if err != nil {
		t.Fatalf("user not persisted: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(created.Password)) != nil {
		t.Fatal("generated password does not match stored hash")
	}
	folders, err := ts.db.FoldersByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 8 {
		t.Fatalf("default folders = %d, want 8", len(folders))
	}

	// Duplicate names and addresses conflict.
	rec = ts.req(http.MethodPost, "/api/admin/users", `{"name":"bob"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = ts.req(http.MethodPost, "/api/admin/users", `{"name":"carol","address":"bob@example.com"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate address status = %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid addresses are rejected.
	rec = ts.req(http.MethodPost, "/api/admin/users", `{"name":"dave","address":"not-an-email"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid address status = %d: %s", rec.Code, rec.Body.String())
	}

	// Listing returns the account.
	rec = ts.req(http.MethodGet, "/api/admin/users", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bob@example.com"`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// Renaming and resetting the password.
	rec = ts.req(http.MethodPatch, "/api/admin/users/"+user.ID, `{"display_name":"Robert"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"Robert"`) {
		t.Fatalf("rename = %d: %s", rec.Code, rec.Body.String())
	}
	rec = ts.req(http.MethodPatch, "/api/admin/users/"+user.ID, `{"password":"explicit-pass"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("password = %d: %s", rec.Code, rec.Body.String())
	}
	updated, err := ts.db.UserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("explicit-pass")) != nil {
		t.Fatal("explicit password was not stored")
	}

	// Unknown ids and empty patches.
	rec = ts.req(http.MethodPatch, "/api/admin/users/nope", `{"display_name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user status = %d", rec.Code)
	}
	rec = ts.req(http.MethodPatch, "/api/admin/users/"+user.ID, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d", rec.Code)
	}

	// Deletion removes the account and its folders.
	rec = ts.req(http.MethodDelete, "/api/admin/users/"+user.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := ts.db.UserByID(user.ID); err != store.ErrNotFound {
		t.Fatalf("user still present: %v", err)
	}
	if folders, err := ts.db.FoldersByUser(user.ID); err != nil || len(folders) != 0 {
		t.Fatalf("folders left behind: %d, %v", len(folders), err)
	}
	rec = ts.req(http.MethodDelete, "/api/admin/users/"+user.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
}

func TestAdminUsersRequireJSONAndSameOrigin(t *testing.T) {
	ts := newUsersTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"name":"a","address":"a@example.com"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content-type status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"name":"a","address":"a@example.com"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	ts.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", rec.Code)
	}
}
