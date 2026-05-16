package imap

import (
	"os"
	"strings"
	"sync/atomic"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/pocketbase/pocketbase/core"
	"remoon.net/lifemail/db"
	"remoon.net/lifemail/smtp"
)

func New(app core.App) *imapserver.Server {
	opts := &imapserver.Options{
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return NewSession(app, c), nil, nil
		},
	}
	if app.IsDev() {
		opts.DebugWriter = os.Stderr
	}
	srv := imapserver.New(opts)
	return srv
}

type Session struct {
	app     core.App
	conn    *imapserver.Conn
	auth    *core.Record
	mailbox atomic.Pointer[Mailbox]
}

var _ imapserver.Session = (*Session)(nil)
var _ imapserver.SessionIMAP4rev2 = (*Session)(nil)

func NewSession(app core.App, conn *imapserver.Conn) *Session {
	return &Session{
		app:  app,
		conn: conn,
	}
}

func (sess *Session) Close() error {
	if mbox := sess.mailbox.Load(); mbox == nil {
		mbox.Close()
	}
	return nil
}

// Not authenticated state
func (sess *Session) Login(username, password string) error {
	username, _, _ = strings.Cut(username, "@")
	acc, err := sess.app.FindRecordById(db.TableAccounts, username)
	if err != nil {
		return imapserver.ErrAuthFailed
	}
	if !acc.ValidatePassword(password) {
		return imapserver.ErrAuthFailed
	}
	sess.auth = acc
	if _, _, err := smtp.GetMailboxOrCreate(sess.app, acc.Id, smtp.INBOX, nil); err != nil {
		return err
	}
	return nil
}

func (sess *Session) Namespace() (*imap.NamespaceData, error) {
	sess.app.Logger().Debug("Namespace")
	return &imap.NamespaceData{}, nil
}
