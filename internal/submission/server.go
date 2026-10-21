// Package submission implements an implicit-TLS SMTP submission server for
// authenticated users. Mail for local accounts is delivered directly; mail for
// other domains is relayed through the configured Cloudflare SMTP endpoint.
package submission

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/outbound"
	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
)

const maxMessageSize = 5 * 1024 * 1024

// Server accepts SMTP submission connections on an already TLS-wrapped
// listener.
type Server struct {
	Store   *store.Store
	Sender  *smtp.Sender
	Log     *slog.Logger
	Version string

	// IsLocalDomain reports whether a domain is hosted here. Mail to such a
	// domain is never relayed; unknown mailboxes are rejected.
	IsLocalDomain func(domain string) bool

	// AuthLimiter throttles repeated bad passwords. Optional.
	AuthLimiter *authlimit.Limiter
}

// errUnknownLocalRecipient marks a recipient on a local domain that has no
// account or alias.
var errUnknownLocalRecipient = errors.New("unknown local recipient")

// errAuthLocked reports a temporary refusal after too many failures.
var errAuthLocked = errors.New("too many failed authentication attempts")

func (s *Server) version() string {
	if s.Version == "" {
		return "dev"
	}
	return s.Version
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	tp := textproto.NewConn(conn)
	if err := tp.PrintfLine("220 lifemail ESMTP %s ready", s.version()); err != nil {
		return
	}

	var (
		user  *store.User
		from  string
		rcpts []string
	)
	reset := func() {
		from = ""
		rcpts = nil
	}

	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd, arg := splitCommand(line)
		switch cmd {
		case "EHLO":
			_ = tp.PrintfLine("250-lifemail")
			_ = tp.PrintfLine("250-AUTH PLAIN LOGIN")
			_ = tp.PrintfLine("250-SIZE %d", maxMessageSize)
			_ = tp.PrintfLine("250-8BITMIME")
			_ = tp.PrintfLine("250 OK")
		case "HELO":
			_ = tp.PrintfLine("250 lifemail")
		case "AUTH":
			authenticated, err := s.authenticate(tp, arg, conn.RemoteAddr().String())
			if err != nil {
				if errors.Is(err, errAuthLocked) {
					_ = tp.PrintfLine("454 4.7.0 Too many failed authentication attempts, try again later")
				} else {
					_ = tp.PrintfLine("535 5.7.8 Authentication failed")
				}
				continue
			}
			user = authenticated
			_ = tp.PrintfLine("235 2.7.0 Authentication successful")
		case "MAIL":
			if user == nil {
				_ = tp.PrintfLine("530 5.7.0 Authentication required")
				continue
			}
			sender, ok := parsePath(arg, "FROM:")
			if !ok {
				_ = tp.PrintfLine("501 5.1.7 Bad sender address syntax")
				continue
			}
			if !s.maySendAs(user, sender) {
				_ = tp.PrintfLine("553 5.7.1 Sender must match the authenticated user")
				continue
			}
			from = sender
			rcpts = nil
			_ = tp.PrintfLine("250 2.1.0 Ok")
		case "RCPT":
			if user == nil || from == "" {
				_ = tp.PrintfLine("503 5.5.1 MAIL FROM first")
				continue
			}
			rcpt, ok := parsePath(arg, "TO:")
			if !ok {
				_ = tp.PrintfLine("501 5.1.3 Bad recipient address syntax")
				continue
			}
			if len(rcpts) >= 50 {
				_ = tp.PrintfLine("452 4.5.3 Too many recipients")
				continue
			}
			rcpts = append(rcpts, rcpt)
			_ = tp.PrintfLine("250 2.1.5 Ok")
		case "DATA":
			if user == nil || from == "" || len(rcpts) == 0 {
				_ = tp.PrintfLine("503 5.5.1 RCPT TO first")
				continue
			}
			if err := tp.PrintfLine("354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return
			}
			raw, err := io.ReadAll(io.LimitReader(tp.DotReader(), maxMessageSize+1))
			if err != nil {
				_ = tp.PrintfLine("451 4.3.0 Cannot read message")
				reset()
				continue
			}
			if len(raw) > maxMessageSize {
				_ = tp.PrintfLine("552 5.3.4 Message too big")
				reset()
				continue
			}

			parsed, parseErr := mailparse.Parse(raw)
			if parseErr != nil && parsed == nil {
				_ = tp.PrintfLine("550 5.6.0 Cannot parse message")
				reset()
				continue
			}

			external, err := s.deliverLocal(parsed, raw, rcpts)
			if err != nil {
				if s.Log != nil {
					s.Log.Error("local delivery failed", "err", err, "user", user.Name)
				}
				if errors.Is(err, errUnknownLocalRecipient) {
					_ = tp.PrintfLine("550 5.1.1 Unknown recipient")
					reset()
					continue
				}
				_ = tp.PrintfLine("451 4.3.0 Local delivery failed")
				reset()
				continue
			}
			if len(external) > 0 {
				if s.Sender == nil || !s.Sender.Enabled() {
					if s.Log != nil {
						s.Log.Error("cannot send mail: SMTP is not configured",
							"user", user.Name, "recipients", strings.Join(external, ","))
					}
					_ = tp.PrintfLine("451 4.3.0 Outbound SMTP is not configured")
					reset()
					continue
				}
				if err := s.Sender.Send(context.Background(), from, external, raw); err != nil {
					id, qerr := outbound.Enqueue(s.Store, from, external, raw)
					if qerr != nil {
						if s.Log != nil {
							s.Log.Error("cannot queue outbound mail", "err", qerr)
						}
						_ = tp.PrintfLine("451 4.3.0 Cannot queue message")
						reset()
						continue
					}
					if s.Log != nil {
						s.Log.Warn("queued outbound mail", "id", id,
							"recipients", strings.Join(external, ","), "reason", err)
					}
				}
			}
			_ = tp.PrintfLine("250 2.0.0 Ok: queued")
			reset()
		case "RSET":
			reset()
			_ = tp.PrintfLine("250 2.0.0 Ok")
		case "NOOP":
			_ = tp.PrintfLine("250 2.0.0 Ok")
		case "VRFY", "EXPN":
			_ = tp.PrintfLine("252 2.5.2 Cannot verify user")
		case "STARTTLS":
			_ = tp.PrintfLine("502 5.5.1 Already using TLS")
		case "QUIT":
			_ = tp.PrintfLine("221 2.0.0 Bye")
			return
		default:
			_ = tp.PrintfLine("500 5.5.1 Unknown command")
		}
	}
}

// deliverLocal stores the message for recipients hosted on this server and
// returns the addresses that must be relayed upstream. Duplicate Message-IDs
// from client retries are skipped.
func (s *Server) deliverLocal(parsed *mailparse.Message, raw []byte, rcpts []string) ([]string, error) {
	var external []string
	for _, rcpt := range rcpts {
		owner, err := s.Store.UserByAddress(rcpt)
		if err != nil {
			if s.IsLocalDomain != nil && s.IsLocalDomain(mailparse.Domain(rcpt)) {
				return nil, fmt.Errorf("%w: %s", errUnknownLocalRecipient, rcpt)
			}
			external = append(external, rcpt)
			continue
		}
		if parsed.MessageID != "" {
			if exists, err := s.Store.MessageIDExists(owner.ID, parsed.MessageID); err == nil && exists {
				continue
			}
		}
		if _, err := deliver.ToUser(s.Store, owner, deliver.FolderInbox, parsed, raw); err != nil {
			return nil, err
		}
		if s.Log != nil {
			s.Log.Info("delivered locally", "to", owner.Name)
		}
	}
	return external, nil
}

func splitCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	i := strings.IndexByte(line, ' ')
	if i < 0 {
		return strings.ToUpper(line), ""
	}
	return strings.ToUpper(line[:i]), strings.TrimSpace(line[i+1:])
}

func parsePath(arg, prefix string) (string, bool) {
	upper := strings.ToUpper(arg)
	if !strings.HasPrefix(upper, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	if i := strings.IndexByte(rest, '<'); i >= 0 {
		if j := strings.IndexByte(rest[i:], '>'); j > 0 {
			addr := strings.TrimSpace(rest[i+1 : i+j])
			return strings.ToLower(addr), addr != ""
		}
	}
	if i := strings.IndexByte(rest, ' '); i > 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	return strings.ToLower(rest), rest != ""
}

// maySendAs reports whether user is allowed to use sender as MAIL FROM: either
// their primary address or one of their aliases.
func (s *Server) maySendAs(user *store.User, sender string) bool {
	if user.Address != "" && strings.EqualFold(sender, user.Address) {
		return true
	}
	if s.Store == nil {
		return false
	}
	owner, err := s.Store.UserByAddress(sender)
	return err == nil && owner.ID == user.ID
}

func (s *Server) authenticate(tp *textproto.Conn, arg, remote string) (*store.User, error) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return nil, errors.New("missing auth mechanism")
	}
	switch strings.ToUpper(fields[0]) {
	case "PLAIN":
		payload := ""
		if len(fields) > 1 {
			payload = fields[1]
		} else {
			if err := tp.PrintfLine("334 "); err != nil {
				return nil, err
			}
			line, err := tp.ReadLine()
			if err != nil {
				return nil, err
			}
			payload = strings.TrimSpace(line)
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(string(raw), "\x00")
		if len(parts) != 3 {
			return nil, errors.New("invalid PLAIN payload")
		}
		return s.checkCredentials(parts[1], parts[2], remote)

	case "LOGIN":
		username, err := readBase64(tp, "Username:")
		if err != nil {
			return nil, err
		}
		password, err := readBase64(tp, "Password:")
		if err != nil {
			return nil, err
		}
		return s.checkCredentials(username, password, remote)
	}
	return nil, errors.New("unsupported auth mechanism")
}

func readBase64(tp *textproto.Conn, prompt string) (string, error) {
	if err := tp.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte(prompt))); err != nil {
		return "", err
	}
	line, err := tp.ReadLine()
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Server) checkCredentials(email, password, remote string) (*store.User, error) {
	limitKey := "smtps|" + httpx.Host(remote) + "|" + strings.ToLower(email)
	if locked, retry := s.AuthLimiter.Locked(limitKey); locked {
		s.Log.Warn("smtps auth throttled", "user", email, "remote", remote,
			"retry_after", retry.Round(time.Second))
		return nil, errAuthLocked
	}
	user, err := s.Store.UserByLogin(email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if s.AuthLimiter.Fail(limitKey) {
			s.Log.Warn("smtps auth locked", "user", email, "remote", remote)
		}
		return nil, errors.New("invalid credentials")
	}
	s.AuthLimiter.Reset(limitKey)
	return user, nil
}
