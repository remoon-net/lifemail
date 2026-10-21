package api

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"remoon.net/lifemail/internal/store"
)

// Metrics exposes a small Prometheus text endpoint. Scraping requires the
// admin bearer token so the endpoint never leaks counts publicly.
type Metrics struct {
	Store   *store.Store
	Token   string
	Version string
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m.Token == "" {
		http.NotFound(w, r)
		return
	}
	if !tokenMatches(requestToken(r), m.Token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var b strings.Builder
	b.WriteString("# HELP lifemail_build_info Build information exposed as a constant 1.\n")
	b.WriteString("# TYPE lifemail_build_info gauge\n")
	fmt.Fprintf(&b, "lifemail_build_info{version=%q} 1\n", m.Version)

	b.WriteString("# HELP lifemail_users Number of mail accounts.\n")
	b.WriteString("# TYPE lifemail_users gauge\n")
	writeMetric(&b, "lifemail_users", m.Store.CountUsers)

	b.WriteString("# HELP lifemail_messages Number of live messages.\n")
	b.WriteString("# TYPE lifemail_messages gauge\n")
	writeMetric(&b, "lifemail_messages", m.Store.CountMessages)

	b.WriteString("# HELP lifemail_devices Number of known ActiveSync devices.\n")
	b.WriteString("# TYPE lifemail_devices gauge\n")
	writeMetric(&b, "lifemail_devices", m.Store.CountDevices)

	b.WriteString("# HELP lifemail_outbox_messages Queued outbound messages by state.\n")
	b.WriteString("# TYPE lifemail_outbox_messages gauge\n")
	writeOutboxMetric(&b, m.Store, "pending", false)
	writeOutboxMetric(&b, m.Store, "failed", true)

	b.WriteString("# HELP lifemail_goroutines Current number of goroutines.\n")
	b.WriteString("# TYPE lifemail_goroutines gauge\n")
	fmt.Fprintf(&b, "lifemail_goroutines %d\n", runtime.NumGoroutine())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

func writeMetric(b *strings.Builder, name string, count func() (int, error)) {
	if n, err := count(); err == nil {
		fmt.Fprintf(b, "%s %d\n", name, n)
		return
	}
	fmt.Fprintf(b, "%s 0\n", name)
}

func writeOutboxMetric(b *strings.Builder, st *store.Store, state string, failedOnly bool) {
	if n, err := st.CountOutbound(failedOnly); err == nil {
		fmt.Fprintf(b, "lifemail_outbox_messages{state=%q} %d\n", state, n)
		return
	}
	fmt.Fprintf(b, "lifemail_outbox_messages{state=%q} 0\n", state)
}
