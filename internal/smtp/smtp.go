// Package smtp sends messages through Cloudflare Email Sending's SMTP
// submission endpoint (smtp.mx.cloudflare.net:465, implicit TLS).
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config is a snapshot of the SMTP settings.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Domains  []string
}

// Sender submits outbound mail using the configuration returned by Provider.
// Provider is called for every operation, so updates take effect without a
// restart.
type Sender struct {
	Provider func() Config
	Timeout  time.Duration
}

func (s *Sender) current() Config {
	if s == nil || s.Provider == nil {
		return Config{}
	}
	return s.Provider()
}

// Enabled reports whether outbound delivery is configured.
func (s *Sender) Enabled() bool {
	cfg := s.current()
	return cfg.Host != "" && cfg.Password != ""
}

// EnvelopeFrom picks the SMTP MAIL FROM address. Cloudflare only accepts
// senders on an onboarded domain, so addresses outside the configured domains
// fall back to the default sender.
func (s *Sender) EnvelopeFrom(userEmail string) string {
	cfg := s.current()
	if userEmail != "" && (len(cfg.Domains) == 0 || ownsDomain(cfg.Domains, userEmail)) {
		return userEmail
	}
	if cfg.From != "" {
		return cfg.From
	}
	return userEmail
}

func ownsDomain(domains []string, email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range domains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

// Send delivers raw MIME to the listed recipients.
func (s *Sender) Send(ctx context.Context, from string, to []string, raw []byte) error {
	cfg := s.current()
	if cfg.Host == "" || cfg.Password == "" {
		return errors.New("smtp: not configured")
	}
	if len(to) == 0 {
		return errors.New("smtp: no recipients")
	}
	port := cfg.Port
	if port == 0 {
		port = 465
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if port == 465 {
		td := &tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				ServerName: cfg.Host,
				MinVersion: tls.VersionTLS12,
			},
		}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	defer client.Close()

	if port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := client.Auth(plainAuth{user: cfg.Username, pass: cfg.Password}); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp: rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		w.Close()
		return fmt.Errorf("smtp: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: finish: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp: quit: %w", err)
	}
	return nil
}

// plainAuth implements AUTH PLAIN without net/smtp's TLS state check, because
// implicit TLS connections are not flagged as TLS by smtp.Client.
type plainAuth struct {
	user string
	pass string
}

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }
