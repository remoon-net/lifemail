package smtp

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func New(app core.App) (_ *smtp.Server, err error) {
	be := &Backend{app: app}
	srv := smtp.NewServer(be)
	msgs := try.To1(app.FindCollectionByNameOrId(db.TableMessages))
	srv.MaxMessageBytes = msgs.Fields.GetByName("raw").(*core.FileField).MaxSize
	srv.AllowInsecureAuth = true
	return srv, nil
}

type Backend struct {
	app  core.App
	auth sasl.Server
}

var _ smtp.Backend = (*Backend)(nil)

func (be *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
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

var _ smtp.AuthSession = (*Session)(nil)

func (sess *Session) Reset() {
	clear(sess.outbox)
	clear(sess.inbox)
	sess.auth = nil
	sess.authSrv = sasl.NewPlainServer(func(identity, username, password string) error {
		username, _, _ = strings.Cut(username, "@")
		ac, err := sess.app.FindRecordById(db.TableAccounts, username)
		if err != nil {
			return smtp.ErrAuthFailed
		}
		if !ac.ValidatePassword(password) {
			return smtp.ErrAuthFailed
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
		return nil, smtp.ErrAuthUnknownMechanism
	}
	return sess.authSrv, nil
}

var _ smtp.Session = (*Session)(nil)

func (sess *Session) Logout() error {
	return nil
}
func (sess *Session) Mail(from string, opts *smtp.MailOptions) error {
	sess.from = from
	return nil
}
func (sess *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
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
		return user, "", nil
	}
	if sess.auth == nil {
		return "", "", smtp.ErrAuthRequired
	}
	return "", to, nil
}

func (sess *Session) Data(r io.Reader) (err error) {
	app := sess.app
	logger := app.Logger()
	defer err0.Then(&err, nil, func() {
		logger.Error("保存邮件消息出错", "error", err)
	})

	buf := try.To1(io.ReadAll(r))
	fn := fmt.Sprintf("%d.mail", time.Now().Unix())
	f := try.To1(filesystem.NewFileFromBytes(buf, fn))
	msgs := try.To1(app.FindCachedCollectionByNameOrId(db.TableMessages))
	msg := core.NewRecord(msgs)
	msg.Load(map[string]any{
		"account": "",
		"from":    sess.from,
		"inbox":   sess.inbox,
		"outbox":  types.JSONArray[string](sess.outbox),
		"raw":     f,
	})
	if sess.auth != nil {
		msg.Set("account", sess.auth.Id)
	}
	try.To(app.Save(msg))

	for _, acc := range sess.inbox {
		mailbox := sess.initMailboxTry(acc, "INBOX")
		mails := try.To1(app.FindCachedCollectionByNameOrId(db.TableMails))
		mail := core.NewRecord(mails)
		mail.Load(map[string]any{
			"to":      acc,
			"msg":     msg.Id,
			"mailbox": mailbox.Id,
			"uid":     0,
		})
		err := app.RunInTransaction(func(tx core.App) error {
			mailbox, err := tx.FindRecordById(db.TableMailboxes, mail.GetString("mailbox"))
			if err != nil {
				return err
			}
			uidNext := mailbox.GetInt("uid_next")
			uidNext += 1
			mailbox.Set("uid_next", uidNext)
			if err := tx.Save(mailbox); err != nil {
				return err
			}
			mail.Set("uid", uidNext)
			return tx.Save(mail)
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

func (sess *Session) initMailboxTry(acc, name string) (mailbox *core.Record) {
	app := sess.app
	q := "account = {:account} && name = {:name}"
	p := dbx.Params{
		"account": acc,
		"name":    name,
	}
	mailbox, err := app.FindFirstRecordByFilter(db.TableMailboxes, q, p)
	if err == nil {
		return mailbox
	}
	if !errors.Is(err, sql.ErrNoRows) {
		err0.Throw(err)
	}
	mailboxes := try.To1(app.FindCachedCollectionByNameOrId(db.TableMailboxes))
	mailbox = core.NewRecord(mailboxes)
	mailbox.Load(p)
	err = app.RunInTransaction(func(tx core.App) error {
		_, err = tx.FindFirstRecordByFilter(db.TableMailboxes, q, p)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return tx.Save(mailbox)
	})
	try.To(err)
	return mailbox
}

var (
	ErrDomainNotFound = &smtp.SMTPError{
		Code:         550,
		EnhancedCode: smtp.EnhancedCode{5, 1, 2},
		Message:      "domain not found",
	}
	ErrUserNotFound = &smtp.SMTPError{
		Code:         550,
		EnhancedCode: smtp.EnhancedCode{5, 1, 1},
		Message:      "user unknown",
	}
)
