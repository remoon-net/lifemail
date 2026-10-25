package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
	webui "remoon.net/lifemail/web"
)

// routeDeps holds the handlers the HTTP server exposes. Store is only used by
// the health endpoints and may be nil in tests; the rest are plain handlers so
// routing can be tested without a database.
type routeDeps struct {
	Store        *store.Store
	EAS          http.Handler
	Autodiscover http.Handler
	Inbound      http.Handler
	Admin        http.Handler
	Me           http.Handler
	User         http.Handler
	Calendar     http.Handler
	Metrics      http.Handler
}

// newHTTPHandler mounts both surfaces on one listener. Only the API is split
// by path; the console and the account UI are static files served from the
// same build. A reverse proxy can therefore publish the two surfaces on
// separate domains:
//
//	public  /Microsoft-Server-ActiveSync, /Autodiscover/*, /api/inbound,
//	        /api/me/*, /api/user/*, /calendar/*, /healthz, /readyz, /app/*
//	admin   /api/admin/* (including /api/admin/metrics), /admin/*
//
// Certificates are not handled here: put /api/admin/* behind
// client-certificate verification in the proxy (see the Caddyfile example).
func newHTTPHandler(d routeDeps) http.Handler {
	mux := http.NewServeMux()

	// Public surface: mail clients, calendar subscribers, the inbound worker
	// and account owners.
	mux.Handle("/Microsoft-Server-ActiveSync", d.EAS)
	mux.Handle("/Microsoft-Server-ActiveSync/", d.EAS)
	mux.Handle("/api/inbound", d.Inbound)
	mux.Handle("/api/me/", d.Me)
	mux.Handle("/api/user/", d.User)
	mux.Handle("/calendar/", d.Calendar)
	mux.Handle("/autodiscover/autodiscover.xml", d.Autodiscover)
	mux.Handle("/Autodiscover/Autodiscover.xml", d.Autodiscover)

	// Admin surface: the JSON API only; the console below is plain static
	// files and carries no privileges of its own.
	mux.Handle("/api/admin/metrics", d.Metrics)
	mux.Handle("/api/admin/", d.Admin)
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})

	registerHealth(mux, d.Store)

	// Everything else is the embedded UI: /admin/* is the operator console and
	// /app/* is the account UI; both are prerendered directories in the same
	// build.
	console := webConsole()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.EqualFold(r.URL.Path, "/Microsoft-Server-ActiveSync"),
			strings.EqualFold(r.URL.Path, "/Microsoft-Server-ActiveSync/"):
			d.EAS.ServeHTTP(w, r)
		case strings.EqualFold(r.URL.Path, "/autodiscover/autodiscover.xml"):
			d.Autodiscover.ServeHTTP(w, r)
		case r.URL.Path == "/":
			http.Redirect(w, r, "/app/", http.StatusFound)
		default:
			console.ServeHTTP(w, r)
		}
	})
	return mux
}

// webConsole serves the embedded SvelteKit build. It contains /admin/*, the
// operator console, and /app/*, the account UI.
func webConsole() http.Handler {
	return webui.Handler()
}

func registerHealth(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	// readyz additionally proves the database is reachable, so a load balancer
	// does not send traffic to a process that lost its storage.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.DB().PingContext(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
}
