package imap

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/httpx"
	"remoon.net/lifemail/internal/store"
)

const capabilities = "IMAP4rev1 AUTH=PLAIN AUTH=LOGIN IDLE UIDPLUS MOVE SPECIAL-USE QUOTA ID LITERAL+"

// Server serves IMAP over an already TLS-wrapped listener.
type Server struct {
	Store   *store.Store
	Log     *slog.Logger
	Version string

	// AuthLimiter throttles repeated bad passwords. Optional.
	AuthLimiter *authlimit.Limiter
}

func (s *Server) version() string {
	if s.Version == "" {
		return "dev"
	}
	return s.Version
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(nc)
	}
}

type conn struct {
	srv      *Server
	nc       net.Conn
	r        *bufio.Reader
	w        *bufio.Writer
	user     *store.User
	selected *store.Folder
	readOnly bool
}

func (s *Server) serveConn(nc net.Conn) {
	defer nc.Close()
	c := &conn{srv: s, nc: nc, r: bufio.NewReader(nc), w: bufio.NewWriter(nc)}
	if err := c.writeLine("* OK [CAPABILITY %s] lifemail %s ready", capabilities, s.version()); err != nil {
		return
	}
	for {
		_ = nc.SetReadDeadline(time.Now().Add(30 * time.Minute))
		line, literal, err := c.readCommand()
		if err != nil {
			return
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		tag, verb, rest := splitCommand(line)
		if tag == "" || verb == "" {
			_ = c.writeLine("* BAD empty command")
			continue
		}
		c.dispatch(tag, strings.ToUpper(verb), rest, literal)
	}
}

func splitCommand(line string) (tag, verb, rest string) {
	i := strings.IndexByte(line, ' ')
	if i < 0 {
		return line, "", ""
	}
	tag = line[:i]
	rest = strings.TrimLeft(line[i+1:], " ")
	j := strings.IndexByte(rest, ' ')
	if j < 0 {
		return tag, rest, ""
	}
	return tag, rest[:j], strings.TrimLeft(rest[j+1:], " ")
}

func (c *conn) dispatch(tag, verb, rest string, literal []byte) {
	switch verb {
	case "CAPABILITY":
		_ = c.writeLine("* CAPABILITY %s", capabilities)
		c.ok(tag, "CAPABILITY completed")
	case "NOOP":
		c.ok(tag, "NOOP completed")
	case "LOGOUT":
		_ = c.writeLine("* BYE lifemail logging out")
		c.ok(tag, "LOGOUT completed")
	case "ID":
		_ = c.writeLine(`* ID ("name" "lifemail" "version" %s)`, quote(c.srv.version()))
		c.ok(tag, "ID completed")
	case "LOGIN":
		c.login(tag, rest)
	case "AUTHENTICATE":
		c.authenticate(tag, rest)
	case "STARTTLS":
		c.no(tag, "STARTTLS not supported on this port")
	default:
		if !c.requireAuth(tag) {
			return
		}
		switch verb {
		case "LIST":
			c.listMailboxes(tag, rest, false)
		case "LSUB":
			c.listMailboxes(tag, rest, true)
		case "STATUS":
			c.status(tag, rest)
		case "SELECT":
			c.selectMailbox(tag, rest, false)
		case "EXAMINE":
			c.selectMailbox(tag, rest, true)
		case "CREATE":
			c.createMailbox(tag, rest)
		case "DELETE":
			c.deleteMailbox(tag, rest)
		case "RENAME":
			c.renameMailbox(tag, rest)
		case "SUBSCRIBE", "UNSUBSCRIBE":
			c.ok(tag, verb+" completed")
		case "APPEND":
			c.appendMessage(tag, rest, literal)
		case "CLOSE":
			c.closeMailbox(tag)
		case "EXPUNGE":
			c.expunge(tag)
		case "FETCH":
			c.fetch(tag, rest, false)
		case "STORE":
			c.storeFlags(tag, rest, false)
		case "SEARCH":
			c.search(tag, rest, false)
		case "MOVE":
			c.moveMessages(tag, rest, false)
		case "UID":
			sub, args := splitVerb(rest)
			switch strings.ToUpper(sub) {
			case "FETCH":
				c.fetch(tag, args, true)
			case "STORE":
				c.storeFlags(tag, args, true)
			case "SEARCH":
				c.search(tag, args, true)
			case "MOVE":
				c.moveMessages(tag, args, true)
			case "EXPUNGE":
				c.expunge(tag)
			default:
				c.bad(tag, "unsupported UID command")
			}
		case "IDLE":
			c.idle(tag)
		case "CHECK":
			c.ok(tag, "CHECK completed")
		default:
			c.bad(tag, "unknown command "+verb)
		}
	}
}

func (c *conn) requireAuth(tag string) bool {
	if c.user == nil {
		c.no(tag, "authenticate first")
		return false
	}
	return true
}

func (c *conn) requireSelected(tag string) bool {
	if !c.requireAuth(tag) {
		return false
	}
	if c.selected == nil {
		c.bad(tag, "no mailbox selected")
		return false
	}
	return true
}

func (c *conn) login(tag, rest string) {
	tokens := splitTokens(rest)
	if len(tokens) < 2 {
		c.bad(tag, "LOGIN expects username and password")
		return
	}
	c.authenticateCredentials(tag, unquote(tokens[0]), unquote(tokens[1]))
}

func (c *conn) authenticate(tag, rest string) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		c.bad(tag, "AUTHENTICATE expects a mechanism")
		return
	}
	switch strings.ToUpper(fields[0]) {
	case "PLAIN":
		payload := ""
		if len(fields) > 1 {
			payload = fields[1]
		} else {
			if err := c.writeLine("+ "); err != nil {
				return
			}
			line, err := c.r.ReadString('\n')
			if err != nil {
				return
			}
			payload = strings.TrimSpace(line)
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			c.no(tag, "[AUTHENTICATIONFAILED] invalid payload")
			return
		}
		parts := strings.Split(string(raw), "\x00")
		if len(parts) != 3 {
			c.no(tag, "[AUTHENTICATIONFAILED] invalid payload")
			return
		}
		c.authenticateCredentials(tag, parts[1], parts[2])
	case "LOGIN":
		username, ok := c.readBase64("Username:")
		if !ok {
			return
		}
		password, ok := c.readBase64("Password:")
		if !ok {
			return
		}
		c.authenticateCredentials(tag, username, password)
	default:
		c.no(tag, "[AUTHENTICATIONFAILED] unsupported mechanism")
	}
}

func (c *conn) readBase64(prompt string) (string, bool) {
	if err := c.writeLine("+ %s", base64.StdEncoding.EncodeToString([]byte(prompt))); err != nil {
		return "", false
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (c *conn) authenticateCredentials(tag, username, password string) {
	limitKey := "imap|" + httpx.Host(c.nc.RemoteAddr().String()) + "|" + strings.ToLower(username)
	if locked, retry := c.srv.AuthLimiter.Locked(limitKey); locked {
		c.srv.Log.Warn("imap login throttled", "user", username,
			"remote", c.nc.RemoteAddr().String(), "retry_after", retry.Round(time.Second))
		c.no(tag, "[UNAVAILABLE] too many failed login attempts, try again later")
		return
	}
	user, err := c.srv.Store.UserByLogin(username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if c.srv.AuthLimiter.Fail(limitKey) {
			c.srv.Log.Warn("imap login locked", "user", username,
				"remote", c.nc.RemoteAddr().String())
		}
		c.no(tag, "[AUTHENTICATIONFAILED] invalid credentials")
		return
	}
	c.srv.AuthLimiter.Reset(limitKey)
	c.user = user
	c.ok(tag, "[CAPABILITY "+capabilities+"] LOGIN completed")
}

func (c *conn) idle(tag string) {
	if !c.requireAuth(tag) {
		return
	}
	if err := c.writeLine("+ idling"); err != nil {
		return
	}
	last := int64(0)
	if c.selected != nil {
		last, _ = c.srv.Store.NextUID(c.selected.ID)
	}
	var pending []byte
	deadline := time.Now().Add(29 * time.Minute)
	for time.Now().Before(deadline) {
		_ = c.nc.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1024)
		n, err := c.r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if c.selected != nil {
					next, e := c.srv.Store.NextUID(c.selected.ID)
					if e == nil && next > last {
						if msgs, e := c.srv.Store.MessagesForIMAP(c.selected.ID); e == nil {
							_ = c.writeLine("* %d EXISTS", len(msgs))
						}
						last = next
					}
				}
				continue
			}
			return
		}
		for {
			idx := bytes.Index(pending, []byte("\r\n"))
			if idx < 0 {
				break
			}
			line := strings.TrimSpace(string(pending[:idx]))
			pending = pending[idx+2:]
			if strings.EqualFold(line, "DONE") {
				_ = c.nc.SetReadDeadline(time.Time{})
				c.ok(tag, "IDLE terminated")
				return
			}
		}
	}
	c.ok(tag, "IDLE terminated")
}
