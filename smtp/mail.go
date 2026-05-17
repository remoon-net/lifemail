package smtp

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

const (
	INBOX   = "INBOX"
	Drafts  = "Drafts"
	Sent    = "Sent"
	Trash   = "Trash"
	Archive = "Archive"
	Junk    = "Junk"
)

var constBaseMailboxes = []string{
	INBOX,
	Drafts,
	Sent,
	Trash,
	Archive,
	Junk,
}

func IsBaseMailboxes(name string) bool {
	return slices.ContainsFunc(constBaseMailboxes, func(s string) bool {
		return strings.EqualFold(s, name)
	})
}

func CreateBaseMailboxes(app core.App, acc string) error {
	if _, _, err := GetMailboxOrCreate(app, acc, INBOX, nil); err != nil {
		return err
	}
	if _, _, err := GetMailboxOrCreate(app, acc, Sent, &imap.CreateOptions{
		SpecialUse: []imap.MailboxAttr{
			imap.MailboxAttrSent,
		},
	}); err != nil {
		return err
	}
	if _, _, err := GetMailboxOrCreate(app, acc, Drafts, &imap.CreateOptions{
		SpecialUse: []imap.MailboxAttr{
			imap.MailboxAttrDrafts,
		},
	}); err != nil {
		return err
	}
	if _, _, err := GetMailboxOrCreate(app, acc, Trash, &imap.CreateOptions{
		SpecialUse: []imap.MailboxAttr{
			imap.MailboxAttrTrash,
		},
	}); err != nil {
		return err
	}
	if _, _, err := GetMailboxOrCreate(app, acc, Archive, &imap.CreateOptions{
		SpecialUse: []imap.MailboxAttr{
			imap.MailboxAttrArchive,
		},
	}); err != nil {
		return err
	}
	if _, _, err := GetMailboxOrCreate(app, acc, Junk, &imap.CreateOptions{
		SpecialUse: []imap.MailboxAttr{
			imap.MailboxAttrJunk,
		},
	}); err != nil {
		return err
	}
	return nil
}

// if not exists, will create it
func GetMailboxOrCreate(app core.App, acc, name string, options *imap.CreateOptions) (_ *core.Record, created bool, err error) {
	q := "account = {:account} && name = {:name}"
	p := dbx.Params{
		"account": acc,
		"name":    name,
	}
	mbox, err := app.FindFirstRecordByFilter(db.TableMailboxes, q, p)
	if err == nil {
		return mbox, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	attrs := db.MailboxAttr(0)
	if options != nil {
		for _, attr := range options.SpecialUse {
			attrs |= db.ToMailboxAttr(attr)
		}
	}
	mboxes := try.To1(app.FindCachedCollectionByNameOrId(db.TableMailboxes))
	mbox = core.NewRecord(mboxes)
	mbox.Load(map[string]any{
		"account":      acc,
		"name":         name,
		"attrs":        attrs,
		"uid_validity": 0,
		"uid_next":     1,
		"subscribed":   true,
	})
	err = app.RunInTransaction(func(tx core.App) error {
		_, err := tx.FindFirstRecordByFilter(db.TableMailboxes, q, p)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		acc, err := tx.FindRecordById(db.TableAccounts, mbox.GetString("account"))
		if err != nil {
			return err
		}
		uvn := acc.GetInt("uid_validity_next")
		uvn = max(uvn, 1) // 从1开始
		mbox.Set("uid_validity", uvn)
		uvn += 1
		acc.Set("uid_validity_next", uvn)
		if err := tx.Save(acc); err != nil {
			return err
		}
		if err := tx.Save(mbox); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return mbox, created, nil
}

func SaveMail(tx core.App, mail *core.Record) error {
	mailbox, err := tx.FindRecordById(db.TableMailboxes, mail.GetString("mailbox"))
	if err != nil {
		return err
	}
	if mail.IsNew() {
		uidNext := mailbox.GetInt("uid_next")
		uidNext = max(uidNext, 1) // 从1开始
		mail.Set("uid", uidNext)
		uidNext += 1
		mailbox.Set("uid_next", uidNext)
	} else {
		h := mail.GetInt("modseq")
		h += 1
		mail.Set("modseq", h)
		mh := mailbox.GetInt("highest_modseq")
		mh = max(h, mh)
		mailbox.Set("highest_modseq", mh)
	}
	if err := tx.Save(mailbox); err != nil {
		return err
	}
	return tx.Save(mail)
}

func SaveMsg(app core.App, buf []byte, extra map[string]any) (msg *core.Record, err error) {
	defer err0.Then(&err, nil, nil)

	fn := fmt.Sprintf("%d.mail", time.Now().Unix())
	f := try.To1(filesystem.NewFileFromBytes(buf, fn))

	br := bufio.NewReader(bytes.NewReader(buf))
	header := try.To1(textproto.ReadHeader(br))
	envelope := imapserver.ExtractEnvelope(header)
	envelopeBytes := try.To1(json.Marshal(envelope))
	subject := envelope.Subject
	hd := try.To1(types.ParseDateTime(envelope.Date))

	headerBytes := try.To1(json.Marshal(header.Map()))

	msgs := try.To1(app.FindCachedCollectionByNameOrId(db.TableMessages))
	msg = core.NewRecord(msgs)
	msg.Load(map[string]any{
		"account":     "",
		"subject":     subject,
		"header_date": hd,
		"envelope":    types.JSONRaw(envelopeBytes),
		"header":      types.JSONRaw(headerBytes),
		"size":        len(buf),
		"raw":         f,
	})
	if extra != nil {
		msg.Load(extra)
	}

	try.To(app.Save(msg))

	return msg, nil
}
