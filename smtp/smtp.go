package smtp

import (
	"crypto/tls"
	"io"
	"os"
	"strings"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func New(app core.App, tc *tls.Config, apply func(*smtp.Server)) (_ *smtp.Server) {
	be := &Backend{app: app}
	srv := smtp.NewServer(be)
	srv.AllowInsecureAuth = false
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

var _ smtp.Backend = (*Backend)(nil)

func (be *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	sess := &Session{app: be.app}
	sess.Reset()
	return sess, nil
}

type Session struct {
	app   core.App
	from  string
	inbox []string // 直接存入本机数据库
}

func (sess *Session) Reset() {
	sess.inbox = sess.inbox[:0]
}

var _ smtp.Session = (*Session)(nil)

func (sess *Session) Logout() error {
	return nil
}
func (sess *Session) Mail(from string, opts *smtp.MailOptions) error {
	sess.from = from
	// todo: 这里应该检查来源是否正确
	return nil
}
func (sess *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	user, domain, _ := strings.Cut(to, "@")
	_, err := sess.app.FindFirstRecordByData(db.TableDomains, "domain", domain)
	if err != nil {
		return ErrDomainNotFound
	}
	user = Alias2Account(user)
	_, err = sess.app.FindRecordById(db.TableAccounts, user)
	if err != nil {
		return ErrUserNotFound
	}
	sess.inbox = append(sess.inbox, user)
	return nil
}

func (sess *Session) Data(r io.Reader) (err error) {
	app := sess.app
	logger := app.Logger()
	defer err0.Then(&err, nil, func() {
		logger.Error("保存邮件消息出错", "error", err)
	})

	buf, err := io.ReadAll(r)
	try.To(err)
	extra := map[string]any{
		"from":  sess.from,
		"inbox": sess.inbox,
	}
	msg := try.To1(SaveMsg(app, buf, extra))

	mails := try.To1(app.FindCachedCollectionByNameOrId(db.TableMails))

	for _, acc := range sess.inbox {
		if acc == "" {
			continue
		}
		mailbox, _ := try.To2(GetMailboxOrCreate(app, acc, INBOX, nil))
		mail := core.NewRecord(mails)
		mail.Load(map[string]any{
			"to":      acc,
			"msg":     msg.Id,
			"mailbox": mailbox.Id,
			"uid":     0,
		})
		err := app.RunInTransaction(func(tx core.App) error {
			return SaveMail(tx, mail)
		})
		try.To(err)
	}

	return nil
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
