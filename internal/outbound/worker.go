// Package outbound delivers queued messages through the configured SMTP relay,
// retrying with exponential backoff when it is unavailable.
package outbound

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
)

const (
	// MaxAttempts bounds how often a message is retried before it is marked
	// failed. With the backoff below this spans roughly half a day.
	MaxAttempts = 20
	// Interval is how often the queue is checked.
	Interval = 30 * time.Second
	// maxBackoff caps the delay between attempts.
	maxBackoff = 60 * time.Minute
)

// Backoff returns the delay before the next attempt (attempts >= 1).
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Minute << uint(attempts-1)
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}

// Enqueue stores a message for later delivery.
func Enqueue(st *store.Store, envelopeFrom string, recipients []string, raw []byte) (int64, error) {
	return st.EnqueueOutbound(envelopeFrom, recipients, raw)
}

// Worker delivers queued messages.
type Worker struct {
	Store  *store.Store
	Sender *smtp.Sender
	Log    *slog.Logger
}

// Run checks the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Drain(ctx)
		}
	}
}

// Drain sends every due message once. Attempts are only counted when SMTP is
// configured, so mail waits indefinitely while setup is incomplete.
func (w *Worker) Drain(ctx context.Context) {
	if w.Sender == nil || !w.Sender.Enabled() {
		return
	}
	items, err := w.Store.DueOutbound(time.Now().Unix(), 20)
	if err != nil {
		w.Log.Error("cannot read outbound queue", "err", err)
		return
	}
	for _, item := range items {
		if err := w.Sender.Send(ctx, item.EnvelopeFrom, item.Recipients, item.Raw); err != nil {
			w.fail(item, err)
			continue
		}
		if err := w.Store.DeleteOutbound(item.ID); err != nil {
			w.Log.Error("cannot remove delivered message", "id", item.ID, "err", err)
			continue
		}
		w.Log.Info("queued message delivered", "id", item.ID,
			"recipients", strings.Join(item.Recipients, ","))
	}
}

func (w *Worker) fail(item *store.Outbound, err error) {
	attempts := item.Attempts + 1
	if attempts >= MaxAttempts {
		if uerr := w.Store.RetryOutbound(item.ID, attempts, 0, err.Error(), true); uerr != nil {
			w.Log.Error("cannot mark message failed", "id", item.ID, "err", uerr)
		}
		w.Log.Error("queued message failed permanently", "id", item.ID,
			"attempts", attempts, "recipients", strings.Join(item.Recipients, ","), "err", err)
		return
	}
	delay := Backoff(attempts)
	if uerr := w.Store.RetryOutbound(item.ID, attempts, time.Now().Add(delay).Unix(), err.Error(), false); uerr != nil {
		w.Log.Error("cannot schedule retry", "id", item.ID, "err", uerr)
		return
	}
	w.Log.Warn("queued message retry scheduled", "id", item.ID, "attempts", attempts,
		"retry_in", delay, "recipients", strings.Join(item.Recipients, ","), "err", err)
}
