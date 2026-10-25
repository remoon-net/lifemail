// Command lifemail runs the ActiveSync server, the mail listeners and the
// admin API.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/api"
	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/config"
	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/eas"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/ical"
	"remoon.net/lifemail/internal/imap"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/outbound"
	"remoon.net/lifemail/internal/settings"
	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/submission"
)

// version is set at build time with -X main.version=<git describe>.
var version = "dev"

// Login throttling: an account and client address pair is locked out after
// authMaxFailures bad passwords inside authFailureWindow.
const (
	authMaxFailures   = 5
	authFailureWindow = 10 * time.Minute
	authLockout       = 5 * time.Minute
)

func main() {
	cfg := config.Load()
	args := os.Args[1:]
	if len(args) == 0 {
		runServer(cfg, serveOptions{})
		return
	}
	switch args[0] {
	case "serve":
		runServer(cfg, parseServeFlags(args[1:]))
		return
	case "user":
		runUserCommand(cfg, args[1:])
		return
	case "db":
		runDBCommand(cfg, args[1:])
		return
	case "version", "--version", "-version":
		fmt.Printf("lifemail %s\n", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		if strings.HasPrefix(args[0], "-") {
			runServer(cfg, parseServeFlags(args))
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

type serveOptions struct {
	IMAPS string
	SMTPS string
}

func parseServeFlags(args []string) serveOptions {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var opts serveOptions
	fs.StringVar(&opts.IMAPS, "imaps", "", "start an IMAPS listener, e.g. imaps://:993")
	fs.StringVar(&opts.SMTPS, "smtps", "", "start an SMTPS submission listener, e.g. smtps://:465")
	_ = fs.Parse(args)
	return opts
}

func usage() {
	fmt.Fprint(os.Stderr, `lifemail - Exchange ActiveSync server for small teams

Usage:
  lifemail serve [-imaps imaps://:993] [-smtps smtps://:465]
  lifemail user add -name box [-address box@example.com] [-display "Box"] [-password y]
  lifemail user passwd -name box [-password y]
  lifemail user list
  lifemail db status
  lifemail db migrate
  lifemail version

Configuration is read from the environment:
  LIFEMAIL_ADDR (default :8080)        LIFEMAIL_DB_DSN (path or postgres:// URL)
  LIFEMAIL_INBOUND_TOKEN               LIFEMAIL_ADMIN_TOKEN
  LIFEMAIL_ATTACHMENT_DIR              LIFEMAIL_PUBLIC_BASE_URL
  LIFEMAIL_AUTO_MIGRATE (default 1)    LIFEMAIL_EAS_DUMP

Mail domains, TLS certificates and SMTP parameters are stored in the database
and can be changed at runtime via GET/PUT /api/admin/settings.

IMAPS/SMTPS use the per-domain certificate configured there and are only
started when the corresponding flag is given.

The admin API (/api/admin/*) shares the HTTP listener with the public endpoints.
Publish it on a separate domain and restrict it there; client certificates are
the reverse proxy's job. The console under /admin/ is static files.
`)
}

func runServer(cfg config.Config, opts serveOptions) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))

	imapsAddr, err := parseListenAddr(opts.IMAPS, "imaps")
	if err != nil {
		logger.Error("invalid -imaps address", "err", err)
		os.Exit(2)
	}
	smtpsAddr, err := parseListenAddr(opts.SMTPS, "smtps")
	if err != nil {
		logger.Error("invalid -smtps address", "err", err)
		os.Exit(2)
	}

	if cfg.AutoMigrate {
		if _, err := store.MigrateWithLog(cfg.DSN, func(version int, name string) {
			logger.Info("database migrated", "version", version, "name", name)
		}); err != nil {
			logger.Error("cannot migrate database", "err", err)
			os.Exit(1)
		}
	}
	st, err := store.OpenWithMigrations(cfg.DSN, false)
	if err != nil {
		logger.Error("cannot open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// MIME bodies and attachment payloads live outside the database; the
	// directory may be an rclone mount so it can be backed up independently.
	blobs, err := blobstore.Open(cfg.AttachmentDir)
	if err != nil {
		logger.Error("cannot open attachment directory", "dir", cfg.AttachmentDir, "err", err)
		os.Exit(1)
	}
	st.SetBlobs(blobs)
	logger.Info("payload storage ready", "dir", blobs.Root())

	runtimeSettings, err := settings.New(st)
	if err != nil {
		logger.Error("cannot load runtime settings", "err", err)
		os.Exit(1)
	}

	sender := &smtp.Sender{
		Provider: func() smtp.Config {
			rc := runtimeSettings.Snapshot()
			return smtp.Config{
				Host:     rc.SMTPHost,
				Port:     rc.SMTPPort,
				Username: rc.SMTPUser,
				Password: rc.SMTPPassword,
				From:     rc.SMTPFrom,
				Domains:  rc.DomainNames(),
			}
		},
	}
	if !sender.Enabled() {
		logger.Warn("SMTP password is not configured; mail to external domains will fail",
			"hint", "PUT smtp.password to /api/admin/settings")
	}

	isLocalDomain := func(domain string) bool {
		for _, d := range runtimeSettings.Snapshot().Domains {
			if strings.EqualFold(d.Name, domain) {
				return true
			}
		}
		return false
	}

	easServer := &eas.Server{
		Store: st, Sender: sender, Log: logger, IsLocalDomain: isLocalDomain,
		AuthLimiter: authlimit.New(authMaxFailures, authFailureWindow, authLockout),
		DumpDir:     cfg.EASDumpDir,
	}
	// Out-of-office notices and iMIP replies must fire for every delivery path
	// (EAS, IMAP submission, inbound API), so the hook lives in the delivery
	// layer.
	deliver.OnInboxDelivered = func(user *store.User, parsed *mailparse.Message, raw []byte) {
		easServer.HandleIMipReply(user, parsed, raw)
		easServer.OofReply(user, parsed, raw)
	}
	mailAuthLimiter := authlimit.New(authMaxFailures, authFailureWindow, authLockout)
	inbound := &api.Inbound{Store: st, Token: cfg.InboundToken, Log: logger,
		Settings: runtimeSettings, Sender: sender}
	if cfg.InboundToken == "" {
		logger.Warn("LIFEMAIL_INBOUND_TOKEN is empty; /api/inbound rejects all requests")
	}

	admin := &api.Admin{Store: st, Settings: runtimeSettings, Token: cfg.AdminToken,
		Version: version, Log: logger, BaseURL: cfg.PublicBaseURL,
		Sender:      sender,
		AuthLimiter: authlimit.New(authMaxFailures, authFailureWindow, authLockout)}
	if cfg.AdminToken == "" {
		logger.Warn("/api/admin is disabled: set LIFEMAIL_ADMIN_TOKEN (the inbound token is not reused)")
	}

	for _, problem := range runtimeSettings.CertificateErrors() {
		logger.Warn("tls certificate problem", "err", problem)
	}

	var mailListeners []net.Listener
	if imapsAddr != "" || smtpsAddr != "" {
		mailTLS := runtimeSettings.TLSConfig()
		if mailTLS == nil {
			logger.Error("mail listeners require at least one domain certificate",
				"hint", "set tls_cert and tls_key via /api/admin/settings")
			os.Exit(1)
		}
		if imapsAddr != "" {
			ln, listenErr := tls.Listen("tcp", imapsAddr, mailTLS)
			if listenErr != nil {
				logger.Error("cannot listen for IMAPS", "err", listenErr)
				os.Exit(1)
			}
			server := &imap.Server{Store: st, Log: logger, Version: version, AuthLimiter: mailAuthLimiter}
			mailListeners = append(mailListeners, ln)
			go func() {
				if serveErr := server.Serve(ln); serveErr != nil && !errors.Is(serveErr, net.ErrClosed) {
					logger.Error("IMAPS server stopped", "err", serveErr)
				}
			}()
			logger.Info("IMAPS listening", "addr", imapsAddr)
		}
		if smtpsAddr != "" {
			ln, listenErr := tls.Listen("tcp", smtpsAddr, mailTLS)
			if listenErr != nil {
				logger.Error("cannot listen for SMTPS", "err", listenErr)
				os.Exit(1)
			}
			server := &submission.Server{Store: st, Sender: sender, Log: logger, Version: version,
				IsLocalDomain: isLocalDomain, AuthLimiter: mailAuthLimiter}
			mailListeners = append(mailListeners, ln)
			go func() {
				if serveErr := server.Serve(ln); serveErr != nil && !errors.Is(serveErr, net.ErrClosed) {
					logger.Error("SMTPS server stopped", "err", serveErr)
				}
			}()
			logger.Info("SMTPS listening", "addr", smtpsAddr)
		}
	}

	handlers := routeDeps{
		Store:        st,
		EAS:          easServer,
		Autodiscover: eas.Autodiscover(),
		Inbound:      inbound,
		Admin:        admin,
		Me: &api.Me{
			Store:       st,
			Log:         logger,
			AuthLimiter: authlimit.New(authMaxFailures, authFailureWindow, authLockout),
			BaseURL:     cfg.PublicBaseURL,
		},
		// The account UI. It signs its own cookie with a key derived from the
		// admin token, so webmail login is unavailable when that token is
		// unset (the Basic-auth /api/me surface keeps working).
		User: &api.User{
			Store:       st,
			Log:         logger,
			Token:       cfg.AdminToken,
			AuthLimiter: authlimit.New(authMaxFailures, authFailureWindow, authLockout),
			BaseURL:     cfg.PublicBaseURL,
			Version:     version,
		},
		// Read-only iCalendar subscription feeds. The UUID in the path is the
		// credential; whether it resolves at all is the owner's per-calendar
		// choice.
		Calendar: ical.Handler{Store: st},
		Metrics:  &api.Metrics{Store: st, Token: cfg.AdminToken, Version: version},
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logRequests(logger, newHTTPHandler(handlers)),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	queueWorker := &outbound.Worker{Store: st, Sender: sender, Log: logger}
	go queueWorker.Run(ctx)
	if queued, err := st.CountOutbound(false); err == nil && queued > 0 {
		logger.Info("outbound queue has pending messages", "count", queued)
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		for _, ln := range mailListeners {
			_ = ln.Close()
		}
	}()

	runtimeCfg := runtimeSettings.Snapshot()
	logger.Info("lifemail listening", "version", version, "addr", cfg.Addr, "database", st.Dialect(),
		"domains", runtimeCfg.DomainNames(), "smtp_host", runtimeCfg.SMTPHost)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func parseListenAddr(raw, scheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(parsed.Scheme, scheme) {
			return "", fmt.Errorf("expected %s:// address, got %q", scheme, parsed.Scheme)
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return "", fmt.Errorf("unexpected path in %q", raw)
		}
		raw = parsed.Host
	}
	if raw == "" {
		return "", fmt.Errorf("missing address")
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return "", fmt.Errorf("%q: %w", raw, err)
	}
	return raw, nil
}

func runDBCommand(cfg config.Config, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "status":
		status, err := store.MigrationStatus(cfg.DSN)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot read migration status: %v\n", err)
			os.Exit(1)
		}
		for _, m := range status {
			state := "pending"
			if m.Applied {
				state = "applied"
			}
			applied := ""
			if m.AppliedAt > 0 {
				applied = time.Unix(m.AppliedAt, 0).UTC().Format(time.RFC3339)
			}
			fmt.Printf("%04d %-28s %-8s %s\n", m.Version, m.Name, state, applied)
		}
	case "migrate":
		count, err := store.MigrateWithLog(cfg.DSN, func(version int, name string) {
			fmt.Printf("applied %04d %s\n", version, name)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot migrate: %v\n", err)
			os.Exit(1)
		}
		if count == 0 {
			fmt.Println("database is up to date")
		}
	default:
		usage()
		os.Exit(2)
	}
}

func runUserCommand(cfg config.Config, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	st, err := store.Open(cfg.DSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("user add", flag.ExitOnError)
		name := fs.String("name", "", "login name (no domain)")
		address := fs.String("address", "", "primary address (optional)")
		display := fs.String("display", "", "display name")
		password := fs.String("password", "", "password (random when empty)")
		_ = fs.Parse(args[1:])
		if *name == "" {
			fmt.Fprintln(os.Stderr, "user add: -name is required")
			os.Exit(2)
		}
		pw := *password
		generated := false
		if pw == "" {
			pw = randomPassword()
			generated = true
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot hash password: %v\n", err)
			os.Exit(1)
		}
		user, err := st.CreateUser(*name, *address, *display, string(hash))
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot create user: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("created %s\n", user.Name)
		if user.Address != "" {
			fmt.Printf("address: %s\n", user.Address)
		}
		if generated {
			fmt.Printf("password: %s\n", pw)
		}
	case "passwd":
		fs := flag.NewFlagSet("user passwd", flag.ExitOnError)
		name := fs.String("name", "", "login name")
		password := fs.String("password", "", "password (random when empty)")
		_ = fs.Parse(args[1:])
		if *name == "" {
			fmt.Fprintln(os.Stderr, "user passwd: -name is required")
			os.Exit(2)
		}
		pw := *password
		generated := false
		if pw == "" {
			pw = randomPassword()
			generated = true
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot hash password: %v\n", err)
			os.Exit(1)
		}
		if err := st.SetPassword(*name, string(hash)); err != nil {
			fmt.Fprintf(os.Stderr, "cannot update password: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("updated %s\n", *name)
		if generated {
			fmt.Printf("password: %s\n", pw)
		}
	case "list":
		users, err := st.ListUsers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot list users: %v\n", err)
			os.Exit(1)
		}
		for _, u := range users {
			fmt.Printf("%s\t%s\t%s\n", u.Name, u.Address, u.DisplayName)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func randomPassword() string {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := httpx.RequestID(r)
		if requestID != "" {
			w.Header().Set("X-Request-Id", requestID)
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			ua := r.UserAgent()
			if len(ua) > 160 {
				ua = ua[:160]
			}
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).Round(time.Millisecond),
				"ip", httpx.ClientIP(r),
				"request_id", requestID,
				"ua", ua)
		}
	})
}
