package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"remoon.net/lifemail/internal/deliver"
)

func TestEventHubFansOutAndUnsubscribes(t *testing.T) {
	hub := NewEventHub()
	first, ok := hub.Subscribe("alice")
	if !ok {
		t.Fatal("first subscription rejected")
	}
	second, ok := hub.Subscribe("alice")
	if !ok {
		t.Fatal("second subscription rejected")
	}
	other, ok := hub.Subscribe("bob")
	if !ok {
		t.Fatal("subscription for another account rejected")
	}

	hub.Publish("alice", Event{Kind: "mail", Subject: "one"})
	for i, stream := range []*EventStream{first, second} {
		select {
		case ev := <-stream.C:
			if ev.Subject != "one" {
				t.Fatalf("stream %d got %+v", i, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("stream %d got no event", i)
		}
	}
	select {
	case ev := <-other.C:
		t.Fatalf("another account received %+v", ev)
	default:
	}

	first.Close()
	first.Close() // closing twice must not panic
	if got := hub.Streams("alice"); got != 1 {
		t.Fatalf("alice streams = %d, want 1", got)
	}
	second.Close()
	if got := hub.Streams("alice"); got != 0 {
		t.Fatalf("alice streams = %d, want 0", got)
	}
}

func TestEventHubDropsSlowStreams(t *testing.T) {
	hub := NewEventHub()
	stream, _ := hub.Subscribe("alice")
	defer stream.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < sseEventBuffer*4; i++ {
			hub.Publish("alice", Event{Kind: "mail", Subject: "burst"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a stream that is not drained")
	}
	select {
	case <-stream.C:
	default:
		t.Fatal("events were dropped before the buffer was full")
	}
}

func TestEventHubCloseEndsStreams(t *testing.T) {
	hub := NewEventHub()
	stream, _ := hub.Subscribe("alice")
	hub.Close()

	select {
	case _, open := <-stream.C:
		if open {
			t.Fatal("closed hub left the stream open")
		}
	case <-time.After(time.Second):
		t.Fatal("stream was not closed")
	}
	hub.Publish("alice", Event{Kind: "mail"}) // must not panic
	if _, ok := hub.Subscribe("alice"); ok {
		t.Fatal("closed hub accepted a subscription")
	}
	stream.Close()
}

func TestNewMailEventTruncatesHeaders(t *testing.T) {
	ev := NewMailEvent(deliver.FolderInbox, strings.Repeat("主", 300), "", "bob@example.com")
	if ev.From != "bob@example.com" {
		t.Fatalf("from = %q", ev.From)
	}
	if runes := []rune(ev.Subject); len(runes) != 161 || runes[160] != '…' {
		t.Fatalf("subject was not truncated: %d runes", len(runes))
	}
}

// readEvent reads one SSE frame (name + JSON data) from the stream.
func readEvent(t *testing.T, reader *bufio.Reader) (string, Event) {
	t.Helper()
	var name, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if data == "" {
				continue // "retry:" line or a keep-alive comment
			}
			var ev Event
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("decode event %q: %v", data, err)
			}
			return name, ev
		case strings.HasPrefix(line, ":"):
			// keep-alive comment
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestUserEventsStreamPushesNewMail(t *testing.T) {
	h, _, user := newUserAPI(t)
	h.Events = NewEventHub()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Log in against the test server so the stream carries a real session
	// cookie, exactly like the browser does.
	resp, err := srv.Client().Post(srv.URL+"/api/user/session", "application/json",
		strings.NewReader(`{"login":"alice","password":"secret"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/user/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		req.AddCookie(c)
	}
	stream, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", stream.StatusCode)
	}
	if ct := stream.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}

	reader := bufio.NewReader(stream.Body)
	name, ev := readEvent(t, reader)
	if name != "ready" || ev.Kind != "ready" {
		t.Fatalf("first frame = %q %+v", name, ev)
	}

	h.Events.Publish(user.ID, NewMailEvent(deliver.FolderInbox, "hi there", "Bob", "bob@example.com"))
	name, ev = readEvent(t, reader)
	if name != "mail" {
		t.Fatalf("event name = %q", name)
	}
	if ev.Subject != "hi there" || ev.From != "Bob" || ev.FolderType != deliver.FolderInbox {
		t.Fatalf("payload = %+v", ev)
	}
}

func TestUserEventsRequiresSession(t *testing.T) {
	h, _, _ := newUserAPI(t)
	h.Events = NewEventHub()
	rec := userDo(h, http.MethodGet, "/api/user/events", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserEventsWithoutHub(t *testing.T) {
	h, _, _ := newUserAPI(t)
	cookie := loginUser(t, h)
	rec := userDo(h, http.MethodGet, "/api/user/events", "", cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}
