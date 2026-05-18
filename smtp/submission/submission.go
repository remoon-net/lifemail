package submission

import (
	"crypto/tls"
	"io"
	"os"
	"strings"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"
	smtp2 "github.com/emersion/go-smtp"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
	"remoon.net/lifemail/smtp"
)

func New(app core.App, tc *tls.Config, apply func(srv *smtp2.Server)) (_ *smtp2.Server) {
	be := &Backend{app: app}
	srv := smtp2.NewServer(be)
	srv.AllowInsecureAuth = tc == nil
	srv.TLSConfig = tc
	if apply != nil {
		apply(srv)
	}
	if app.IsDev() {
		srv.Debug = os.Stderr
	}
	return srv
}

type Backend struct {
	app  core.App
	auth sasl.Server
}

var _ smtp2.Backend = (*Backend)(nil)

func (be *Backend) NewSession(c *smtp2.Conn) (smtp2.Session, error) {
	sess := &Session{app: be.app}
	sess.Reset()
	return sess, nil
}

type Session struct {
	app     core.App
	authSrv sasl.Server
	auth    *core.Record
	from    string
	inbox   []string // 直接存入本机数据库
	outbox  []string // 需要转发出去
}

var _ smtp2.AuthSession = (*Session)(nil)

func (sess *Session) Reset() {
	sess.outbox = sess.outbox[:0]
	sess.inbox = sess.inbox[:0]
	sess.auth = nil
	sess.authSrv = sasl.NewPlainServer(func(identity, username, password string) error {
		username, _, _ = strings.Cut(username, "@")
		username = smtp.Alias2Account(username)
		ac, err := sess.app.FindRecordById(db.TableAccounts, username)
		if err != nil {
			return smtp2.ErrAuthFailed
		}
		if !ac.ValidatePassword(password) {
			return smtp2.ErrAuthFailed
		}
		sess.auth = ac
		return nil
	})
}

func (sess *Session) AuthMechanisms() []string {
	return []string{sasl.Plain}
}
func (sess *Session) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, smtp2.ErrAuthUnknownMechanism
	}
	return sess.authSrv, nil
}

var _ smtp2.Session = (*Session)(nil)

func (sess *Session) Logout() error {
	return nil
}
func (sess *Session) Mail(from string, opts *smtp2.MailOptions) error {
	if sess.auth == nil {
		return smtp2.ErrAuthRequired
	}
	sess.from = from
	return nil
}
func (sess *Session) Rcpt(to string, opts *smtp2.RcptOptions) error {
	if sess.auth == nil {
		return smtp2.ErrAuthRequired
	}
	localUser, remoteEmail, err := sess.Target(to)
	if err != nil {
		return err
	}
	if remoteEmail != "" {
		sess.outbox = append(sess.outbox, remoteEmail)
		return nil
	}
	_, err = sess.app.FindRecordById(db.TableAccounts, localUser)
	if err != nil {
		return ErrUserNotFound
	}
	sess.inbox = append(sess.inbox, localUser)
	return nil
}

func (sess *Session) Target(to string) (local, remote string, err error) {
	user, domain, _ := strings.Cut(to, "@")
	if domain == "" {
		return user, "", nil
	}
	_, err = sess.app.FindFirstRecordByData(db.TableDomains, "domain", domain)
	if err == nil {
		user = smtp.Alias2Account(user)
		return user, "", nil
	}
	return "", to, nil
}

func (sess *Session) Data(r io.Reader) (err error) {
	if sess.auth == nil {
		return smtp2.ErrAuthRequired
	}

	app := sess.app
	logger := app.Logger()
	defer err0.Then(&err, nil, func() {
		logger.Error("保存邮件消息出错", "error", err)
	})

	buf, err := io.ReadAll(r)
	try.To(err)
	extra := map[string]any{
		"from":   sess.from,
		"inbox":  sess.inbox,
		"outbox": types.JSONArray[string](sess.outbox),
	}
	if sess.auth != nil {
		extra["account"] = sess.auth.Id
	}
	msg := try.To1(smtp.SaveMsg(app, buf, extra))

	mails := try.To1(app.FindCachedCollectionByNameOrId(db.TableMails))

	for _, acc := range sess.inbox {
		if acc == "" {
			continue
		}
		mailbox, _ := try.To2(smtp.GetMailboxOrCreate(app, acc, smtp.INBOX, nil))
		mail := core.NewRecord(mails)
		mail.Load(map[string]any{
			"to":      acc,
			"msg":     msg.Id,
			"mailbox": mailbox.Id,
			"uid":     0,
		})
		err := app.RunInTransaction(func(tx core.App) error {
			return smtp.SaveMail(tx, mail)
		})
		try.To(err)
	}

	for _, to := range sess.outbox {
		outbounds := try.To1(app.FindCachedCollectionByNameOrId(db.TableOutbounds))
		outbound := core.NewRecord(outbounds)
		outbound.Load(map[string]any{
			"from": sess.auth.Id,
			"to":   to,
			"msg":  msg.Id,
		})
		try.To(app.Save(outbound))
	}

	return nil
}

var (
	ErrDomainNotFound = &smtp2.SMTPError{
		Code:         550,
		EnhancedCode: smtp2.EnhancedCode{5, 1, 2},
		Message:      "domain not found",
	}
	ErrUserNotFound = &smtp2.SMTPError{
		Code:         550,
		EnhancedCode: smtp2.EnhancedCode{5, 1, 1},
		Message:      "user unknown",
	}
)
