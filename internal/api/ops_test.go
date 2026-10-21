package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remoon.net/lifemail/internal/store"
)

func (ts *usersTestServer) enqueue(t *testing.T, from string, rcpts []string, body string) int64 {
	t.Helper()
	id, err := ts.db.EnqueueOutbound(from, rcpts, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdminOutboxLifecycle(t *testing.T) {
	ts := newUsersTestServer(t)
	id := ts.enqueue(t, "alice@example.com", []string{"bob@example.com"}, "Subject: hi\r\n\r\nx")
	if err := ts.db.RetryOutbound(id, 20, 0, "550 mailbox unavailable", true); err != nil {
		t.Fatal(err)
	}

	// Unauthenticated access is rejected.
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/outbox", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	rec = ts.req(http.MethodGet, "/api/admin/outbox?failed=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Items []adminOutboxView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listing.Items))
	}
	item := listing.Items[0]
	if !item.Failed || item.Attempts != 20 || item.LastError == "" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if len(item.Recipients) != 1 || item.Recipients[0] != "bob@example.com" {
		t.Fatalf("recipients = %v", item.Recipients)
	}

	// Retrying clears the failed flag.
	if rec := ts.req(http.MethodPost, "/api/admin/outbox/1/retry", ""); rec.Code != http.StatusOK {
		t.Fatalf("retry status = %d: %s", rec.Code, rec.Body.String())
	}
	stored, err := ts.db.OutboundByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Failed || stored.Attempts != 0 {
		t.Fatalf("after retry: %+v", stored)
	}

	// Deleting removes it.
	if rec := ts.req(http.MethodDelete, "/api/admin/outbox/1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if remaining, err := ts.db.CountOutbound(false); err != nil || remaining != 0 {
		t.Fatalf("remaining = %d, %v", remaining, err)
	}
	if rec := ts.req(http.MethodDelete, "/api/admin/outbox/999", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete missing status = %d", rec.Code)
	}
}

func TestAdminDevicesAndWipe(t *testing.T) {
	ts := newUsersTestServer(t)
	created := ts.create(t, `{"name":"alice","address":"alice@example.com","display_name":"Alice"}`)
	if err := ts.db.UpsertDevice(created.User.ID, "dev-1", "Android", "14.1"); err != nil {
		t.Fatal(err)
	}

	rec := ts.req(http.MethodGet, "/api/admin/devices", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Items []adminDeviceView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 {
		t.Fatalf("devices = %d, want 1", len(listing.Items))
	}
	device := listing.Items[0]
	if device.DeviceID != "dev-1" || device.Address != "alice@example.com" || device.WipeRequested {
		t.Fatalf("unexpected device: %+v", device)
	}

	base := "/api/admin/devices/" + created.User.ID + "/dev-1"
	if rec := ts.req(http.MethodPost, base+"/wipe", ""); rec.Code != http.StatusOK {
		t.Fatalf("wipe status = %d: %s", rec.Code, rec.Body.String())
	}
	if wipe, err := ts.db.WipeRequested("dev-1"); err != nil || !wipe {
		t.Fatalf("wipe requested = %v, %v", wipe, err)
	}

	if rec := ts.req(http.MethodDelete, base, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if devices, err := ts.db.ListDevices(); err != nil || len(devices) != 0 {
		t.Fatalf("after delete: %d, %v", len(devices), err)
	}
}

func TestAdminMaintenancePrunesConsumedChanges(t *testing.T) {
	ts := newUsersTestServer(t)
	created := ts.create(t, `{"name":"alice","address":"alice@example.com","display_name":"Alice"}`)
	inbox, err := ts.db.FolderByType(created.User.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ts.db.AddChange(inbox.ID, "add", "email", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.AddChange(inbox.ID, "add", "email", 2); err != nil {
		t.Fatal(err)
	}

	// With no device position nothing is pruned.
	rec := ts.req(http.MethodPost, "/api/admin/maintenance", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d: %s", rec.Code, rec.Body.String())
	}
	var result maintenanceResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChangesPruned != 0 {
		t.Fatalf("pruned = %d, want 0 without devices", result.ChangesPruned)
	}

	// Once a device consumed the first change it can be dropped.
	if err := ts.db.SaveSyncState(&store.SyncState{
		UserID:   created.User.ID,
		DeviceID: "dev-1",
		FolderID: inbox.ID,
		SyncKey:  "k1",
		Seq:      first,
	}); err != nil {
		t.Fatal(err)
	}
	rec = ts.req(http.MethodPost, "/api/admin/maintenance", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChangesPruned != 1 {
		t.Fatalf("pruned = %d, want 1", result.ChangesPruned)
	}
	changes, err := ts.db.ChangesSince(inbox.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("remaining changes = %d, want 1", len(changes))
	}
}

func TestMetricsRequireToken(t *testing.T) {
	ts := newUsersTestServer(t)
	if _, err := ts.db.CreateUser("alice", "alice@example.com", "Alice", "hash"); err != nil {
		t.Fatal(err)
	}
	m := &Metrics{Store: ts.db, Token: "secret", Version: "v9.9.9"}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`lifemail_build_info{version="v9.9.9"} 1`,
		"lifemail_users 1",
		"lifemail_outbox_messages{state=\"pending\"}",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}

	// Without a token the endpoint behaves as if it does not exist.
	disabled := &Metrics{Store: ts.db}
	rec = httptest.NewRecorder()
	disabled.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", rec.Code)
	}
}
