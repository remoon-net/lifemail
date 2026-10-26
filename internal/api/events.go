package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"remoon.net/lifemail/internal/store"
)

// Server-sent events tuning. The keep-alive stays well below the 120s idle
// timeout of the HTTP server and the usual 60s of a reverse proxy.
const (
	sseKeepAlive      = 25 * time.Second
	sseEventBuffer    = 16
	maxStreamsPerUser = 16
)

// Event is one push sent to the account's open webmail tabs. Kind doubles as
// the SSE event name, so a listener subscribes to "mail" only.
type Event struct {
	Kind       string `json:"kind"`
	Action     string `json:"action,omitempty"`
	FolderID   int64  `json:"folderId,omitempty"`
	FolderType int    `json:"folderType,omitempty"`
	MessageID  int64  `json:"messageId,omitempty"`
	Subject    string `json:"subject,omitempty"`
	From       string `json:"from,omitempty"`
	At         int64  `json:"at"`
}

// NewMailEvent describes a message that just landed in an inbox. Headers are
// truncated because the payload is only a hint: the UI reloads the real thing.
func NewMailEvent(folderType int, subject, fromName, fromAddr string) Event {
	from := strings.TrimSpace(fromName)
	if from == "" {
		from = strings.TrimSpace(fromAddr)
	}
	return Event{
		Kind:       "mail",
		Action:     "new",
		FolderType: folderType,
		Subject:    truncateRunes(subject, 160),
		From:       truncateRunes(from, 120),
		At:         time.Now().Unix(),
	}
}

// EventHub fans events out to the event streams of each account. It is
// in-process on purpose: this server is a single binary, so a push only has to
// reach the tabs connected to this process. Losing an event is harmless (the UI
// refreshes whenever a stream (re)connects), therefore publishing never blocks.
type EventHub struct {
	mu     sync.Mutex
	subs   map[string]map[chan Event]struct{}
	closed bool
}

// NewEventHub returns an empty hub.
func NewEventHub() *EventHub {
	return &EventHub{subs: map[string]map[chan Event]struct{}{}}
}

// Publish queues ev for every stream the account has open. A tab that cannot
// keep up is skipped rather than stalling the delivery path.
func (h *EventHub) Publish(userID string, ev Event) {
	if h == nil || userID == "" {
		return
	}
	if ev.At == 0 {
		ev.At = time.Now().Unix()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for ch := range h.subs[userID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// EventStream is one open SSE connection. Close it when the request ends.
type EventStream struct {
	C <-chan Event

	ch   chan Event
	hub  *EventHub
	user string
	once sync.Once
}

// Close unregisters the stream and releases its buffer.
func (s *EventStream) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() { s.hub.unsubscribe(s.user, s.ch) })
}

// Subscribe registers a stream for one account. ok is false when the account
// already has too many streams open or the hub has been closed.
func (h *EventHub) Subscribe(userID string) (*EventStream, bool) {
	if h == nil || userID == "" {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	set := h.subs[userID]
	if len(set) >= maxStreamsPerUser {
		return nil, false
	}
	if set == nil {
		set = make(map[chan Event]struct{}, 2)
		h.subs[userID] = set
	}
	ch := make(chan Event, sseEventBuffer)
	set[ch] = struct{}{}
	return &EventStream{C: ch, ch: ch, hub: h, user: userID}, true
}

// Streams reports how many streams an account has open. Used by tests.
func (h *EventHub) Streams(userID string) int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[userID])
}

// Close ends every stream so graceful shutdown does not wait for browsers to
// disconnect. The hub stays closed afterwards.
func (h *EventHub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, set := range h.subs {
		for ch := range set {
			close(ch)
		}
	}
	h.subs = map[string]map[chan Event]struct{}{}
}

func (h *EventHub) unsubscribe(userID string, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[userID]
	if !ok {
		return
	}
	if _, ok := set[ch]; !ok {
		return
	}
	delete(set, ch)
	close(ch)
	if len(set) == 0 {
		delete(h.subs, userID)
	}
}

func truncateRunes(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// events serves GET /api/user/events: an SSE stream of pushes for the signed-in
// account. The first frame is always a "ready" event, so the client knows it
// has to reload its data (including after an automatic reconnect, when pushes
// may have been missed).
func (h *User) events(w http.ResponseWriter, r *http.Request, user *store.User) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.Events == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "realtime updates are not available")
		return
	}
	stream, ok := h.Events.Subscribe(user.ID)
	if !ok {
		w.Header().Set("Retry-After", "5")
		writeJSONError(w, http.StatusTooManyRequests, "too many open event streams")
		return
	}
	defer stream.Close()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	// Proxies must not buffer a stream, or pushes arrive in bursts.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// ResponseController reaches the real writer through middleware wrappers
	// (the request logger wraps ResponseWriter), which a plain type assertion
	// would miss.
	controller := http.NewResponseController(w)
	flush := func() {
		_ = controller.Flush()
	}
	send := func(name string, ev Event) error {
		payload, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload); err != nil {
			return err
		}
		flush()
		return nil
	}

	// Tell the browser to come back quickly after a dropped connection.
	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if err := send("ready", Event{Kind: "ready", At: time.Now().Unix()}); err != nil {
		return
	}

	ping := time.NewTicker(sseKeepAlive)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-stream.C:
			if !open {
				return // hub closed: the process is shutting down
			}
			if err := send(ev.Kind, ev); err != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flush()
		}
	}
}
