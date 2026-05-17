package imap

import (
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
	"remoon.net/lifemail/smtp"
)

// Authenticated state
func (sess *Session) Select(mailbox string, options *imap.SelectOptions) (_ *imap.SelectData, err error) {
	sess.app.Logger().Debug("Select")
	defer err0.Then(&err, nil, nil)
	mbox := try.To1(sess.getMailbox(mailbox))
	if options != nil {
		mbox.ReadOnly = options.ReadOnly
	}
	sess.mailbox.Store(mbox)

	list := try.To1(mbox.ListData())
	status := list.Status

	d := &imap.SelectData{
		Flags:             db.AllMailFlags.Flags(),
		PermanentFlags:    db.PermanentFlags.Flags(),
		NumMessages:       dPtr(status.NumMessages),
		FirstUnseenSeqNum: 0,
		NumRecent:         dPtr(status.NumRecent),
		UIDNext:           status.UIDNext,
		UIDValidity:       status.UIDValidity,
		List:              list,
		HighestModSeq:     status.HighestModSeq,
	}
	return d, nil
}
func (sess *Session) getMailbox(mailbox string) (*Mailbox, error) {
	record, err := sess.getMailboxRecord(mailbox)
	if err != nil {
		return nil, err
	}
	mbox := NewMailbox(sess.app, record)
	mbox.uids, err = sess.getUIDs(mbox.Id)
	if err != nil {
		return mbox, err
	}
	return mbox, nil
}
func (sess *Session) getMailboxRecord(mailbox string) (*core.Record, error) {
	q := "account = {:account} && name = {:name}"
	p := dbx.Params{"account": sess.auth.Id, "name": mailbox}
	record, err := sess.app.FindFirstRecordByFilter(db.TableMailboxes, q, p)
	return record, err
}
func (sess *Session) getUIDs(mailbox string) (uids []MailUID, err error) {
	rows, err := sess.app.DB().Select("id", "uid").From(db.TableMails).Where(dbx.HashExp{"mailbox": mailbox}).OrderBy("uid").Rows()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var mu MailUID
		if err := rows.Scan(&mu.ID, &mu.UID); err != nil {
			return uids, err
		}
		uids = append(uids, mu)
	}
	return uids, nil
}
func (sess *Session) Create(mailbox string, options *imap.CreateOptions) (err error) {
	_, created, err := smtp.GetMailboxOrCreate(sess.app, sess.auth.Id, mailbox, options)
	if err != nil {
		return err
	}
	if !created {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Text: fmt.Sprintf("Mailbox %s already exists", mailbox),
			Code: imap.ResponseCodeAlreadyExists,
		}
	}
	return nil
}

var errINBOX = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeCannot,
	Text: "can't delete/rename INBOX",
}

func (sess *Session) Delete(mailbox string) error {
	if strings.EqualFold(mailbox, smtp.INBOX) {
		return errINBOX
	}
	mbox, err := sess.getMailbox(mailbox)
	if err != nil {
		return err
	}
	return sess.app.Delete(mbox)
}
func (sess *Session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	if strings.EqualFold(mailbox, smtp.INBOX) {
		return errINBOX
	}
	mbox, err := sess.getMailbox(mailbox)
	if err != nil {
		return err
	}
	mbox.Set("name", newName)
	mbox.Set("old_name", mailbox)
	return sess.app.Save(mbox)
}
func (sess *Session) Subscribe(mailbox string) error {
	mbox, err := sess.getMailbox(mailbox)
	if err != nil {
		return err
	}
	mbox.Set("subscribed", true)
	return sess.app.Save(mbox)
}
func (sess *Session) Unsubscribe(mailbox string) error {
	mbox, err := sess.getMailbox(mailbox)
	if err != nil {
		return err
	}
	mbox.Set("subscribed", false)
	return sess.app.Save(mbox)
}

const mailboxDelim rune = '/'

func (sess *Session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	app := sess.app
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{
			Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect},
			Delim: mailboxDelim,
		})
	}
	var q dbx.Expression = dbx.HashExp{"account": sess.auth.Id}
	if options.SelectSubscribed {
		q2 := dbx.HashExp{"subscribed": true}
		q = dbx.And(q, q2)
	}
	if options.SelectRemote {
		// 看起来没什么用
	}
	if options.SelectRecursiveMatch {
		// 看起来没什么用
	}
	if options.SelectSpecialUse {
		q2 := dbx.NewExp("attrs & {:a} != 0", dbx.Params{"a": db.MailboxAttrSpecialUse})
		q = dbx.And(q, q2)
	}
	mboxes, err := app.FindAllRecords(db.TableMailboxes, q)
	if err != nil {
		return err
	}
	for _, mbox := range mboxes {
		mbox := NewMailbox(sess.app, mbox)
		matched := func() bool {
			for _, pattern := range patterns {
				if imapserver.MatchList(mbox.Name(), mailboxDelim, ref, pattern) {
					return true
				}
			}
			return false
		}()
		if !matched {
			continue
		}
		ld, err := mbox.ListData()
		if err != nil {
			return err
		}
		if err := w.WriteList(ld); err != nil {
			return err
		}
	}
	return nil
}
func (sess *Session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	mbox, err := sess.getMailbox(mailbox)
	if err != nil {
		return nil, err
	}
	return mbox.Status()
}

func (sess *Session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (_ *imap.AppendData, err error) {
	app := sess.app
	defer err0.Then(&err, nil, nil)

	if mbox := sess.mailbox.Load(); mbox != nil && mbox.ReadOnly && mbox.Name() == mailbox {
		return nil, fmt.Errorf("readonly")
	}

	mbox := try.To1(sess.getMailbox(mailbox))

	buf := try.To1(io.ReadAll(r))
	acc := sess.auth.Id
	extra := map[string]any{
		"account": acc,
		"inbox":   []string{acc},
	}
	msg := try.To1(smtp.SaveMsg(app, buf, extra))

	mails := try.To1(app.FindCachedCollectionByNameOrId(db.TableMails))
	mail := NewMail(core.NewRecord(mails))
	mail.Load(map[string]any{
		"to":      sess.auth.Id,
		"msg":     msg.Id,
		"mailbox": mbox.Id,
		"uid":     0,
	})
	if options != nil {
		mail.AddFlags(options.Flags...)
	}
	err = app.RunInTransaction(func(tx core.App) error {
		return smtp.SaveMail(tx, mail.ProxyRecord())
	})
	try.To(err)

	return &imap.AppendData{
		UID:         mail.UID(),
		UIDValidity: mbox.UIDValidity(),
	}, nil
}

// todo
func (sess *Session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	return mbox.Poll(w, allowExpunge)
}

func (sess *Session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	return mbox.Idle(w, stop)
}
