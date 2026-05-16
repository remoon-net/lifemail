package imap

import (
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
	"remoon.net/lifemail/smtp"
)

// Selected state
func (sess *Session) Unselect() error {
	if mbox := sess.mailbox.Swap(nil); mbox != nil {
		mbox.Close()
	}
	return nil
}
func (sess *Session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	sess.app.Logger().Debug("Expunge")
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	if mbox.ReadOnly {
		return fmt.Errorf("readonly")
	}
	if uids == nil {
		return nil
	}
	idList := []any{}
	for _, mail := range mbox.Iter(true, uids) {
		idList = append(idList, mail.Id)
	}
	if len(idList) == 0 {
		return nil
	}
	q := dbx.In("id", idList...)
	_, err := sess.app.DB().Delete(db.TableMails, q).Execute()
	return err
}

func (sess *Session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) (err error) {
	sess.app.Logger().Debug("Fetch")
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	defer err0.Then(&err, nil, nil)

	maekSeen := false
	for _, bs := range options.BodySection {
		if !bs.Peek {
			maekSeen = true
			break
		}
	}

	for seqNum, m := range mbox.Iter(false, numSet) {
		if maekSeen {
			m.AddFlags(imap.FlagSeen)
			if err := sess.app.RunInTransaction(func(tx core.App) error {
				return smtp.SaveMail(tx, m.Record)
			}); err != nil {
				return err
			}
		}
		wr := w.CreateMessage(seqNum)
		log.Println("111111111111111", seqNum)
		err := m.Fetch(sess.app, wr, options)
		if err != nil {
			return err
		}
	}

	return nil
}

func (sess *Session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	sess.app.Logger().Debug("Store")
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	if mbox.ReadOnly {
		return fmt.Errorf("readonly")
	}
	for _, mail := range mbox.Iter(false, numSet) {
		switch flags.Op {
		case imap.StoreFlagsAdd:
			mail.AddFlags(flags.Flags...)
		case imap.StoreFlagsSet:
			mail.SetFlags(flags.Flags...)
		case imap.StoreFlagsDel:
			mail.DelFlags(flags.Flags...)
		default:
			return fmt.Errorf("unknown STORE flag operation: %v", flags.Op)
		}
		if err := sess.app.RunInTransaction(func(tx core.App) error {
			return smtp.SaveMail(tx, mail.Record)
		}); err != nil {
			return err
		}
	}
	if !flags.Silent {
		return sess.Fetch(w, numSet, &imap.FetchOptions{Flags: true})
	}
	return nil
}
func (sess *Session) Copy(numSet imap.NumSet, dest string) (cd *imap.CopyData, err error) {
	sess.app.Logger().Debug("Copy")
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil, nil
	}
	mboxDst, err := sess.getMailbox(dest)
	if err != nil {
		return nil, err
	}
	mails, err := sess.app.FindCachedCollectionByNameOrId(db.TableMails)
	if err != nil {
		return nil, err
	}
	cd = &imap.CopyData{
		UIDValidity: mboxDst.UIDValidity(),
		SourceUIDs:  imap.UIDSetNum(),
		DestUIDs:    imap.UIDSetNum(),
	}
	for _, m := range mbox.Iter(false, numSet) {
		data, err := m.DBExport(sess.app)
		if err != nil {
			return cd, err
		}
		delete(data, "id")
		nm := core.NewRecord(mails)
		nm.Load(data)
		nm.Set("mailbox", mboxDst.Id)
		err = sess.app.RunInTransaction(func(tx core.App) error {
			return smtp.SaveMail(tx, nm)
		})
		if err != nil {
			return cd, err
		}
		dst := NewMail(nm)
		cd.SourceUIDs.AddNum(m.UID())
		cd.DestUIDs.AddNum(dst.UID())
	}
	return cd, nil
}

func (sess *Session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) (err error) {
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil
	}
	app := mbox.app
	defer err0.Then(&err, nil, nil)
	t := NewMailbox(app, try.To1(sess.getMailboxRecord(dest)))
	for seqNum, m := range mbox.Iter(false, numSet) {
		m2 := NewMail(core.NewRecord(m.Collection()))
		d := try.To1(m.DBExport(app))
		delete(d, "id")
		m2.Load(d)
		m2.Set("mailbox", t.Id)
		err := app.RunInTransaction(func(tx core.App) error {
			m.AddFlags(imap.FlagDeleted)
			if err := smtp.SaveMail(tx, m.ProxyRecord()); err != nil {
				return err
			}
			if err := smtp.SaveMail(tx, m2.ProxyRecord()); err != nil {
				return nil
			}
			if err := tx.Delete(m); err != nil {
				return err
			}
			return nil
		})
		try.To(err)
		cd := &imap.CopyData{
			UIDValidity: t.UIDValidity(),
			SourceUIDs:  imap.UIDSetNum(m.UID()),
			DestUIDs:    imap.UIDSetNum(m2.UID()),
		}
		try.To(w.WriteCopyData(cd))
		try.To(w.WriteExpunge(seqNum))
	}
	return nil
}

type Mail struct {
	core.BaseRecordProxy
}

func NewMail(r *core.Record) *Mail {
	m := &Mail{}
	m.SetProxyRecord(r)
	return m
}

func (m *Mail) Fetch(app core.App, w *imapserver.FetchResponseWriter, options *imap.FetchOptions) (err error) {
	app.Logger().Debug("Mail Fetch")
	defer err0.Then(&err, nil, nil)

	msg := try.To1(app.FindRecordById(db.TableMessages, m.GetString("msg")))

	w.WriteUID(m.UID())

	if options.Flags {
		w.WriteFlags(m.Flags())
	}

	if options.InternalDate {
		w.WriteInternalDate(m.GetDateTime("created").Time())
	}

	fs := try.To1(app.NewFilesystem())
	defer fs.Close()
	fk := msg.BaseFilesPath() + "/" + msg.GetString("raw")
	r := try.To1(fs.GetReader(fk))
	defer r.Close()

	if options.RFC822Size {
		attr := try.To1(fs.Attributes(fk))
		w.WriteRFC822Size(attr.Size)
	}

	if options.Envelope {
		raw := msg.GetString("envelope")
		var envelope imap.Envelope
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			w.WriteEnvelope(nil)
		} else {
			w.WriteEnvelope(&envelope)
		}
	}

	if options.BodyStructure != nil {
		func() {
			try.To1(r.Seek(0, io.SeekStart))
			bs := imapserver.ExtractBodyStructure(r)
			w.WriteBodyStructure(bs)
		}()
	}

	for _, bs := range options.BodySection {
		func() {
			try.To1(r.Seek(0, io.SeekStart))
			buf := imapserver.ExtractBodySection(r, bs)
			wc := w.WriteBodySection(bs, int64(len(buf)))
			defer func() {
				try.To(wc.Close())
			}()
			try.To1(wc.Write(buf))
		}()
	}

	for _, bs := range options.BinarySection {
		func() {
			try.To1(r.Seek(0, io.SeekStart))

			buf := imapserver.ExtractBinarySection(r, bs)
			wc := w.WriteBinarySection(bs, int64(len(buf)))
			defer func() {
				try.To(wc.Close())
			}()
			try.To1(wc.Write(buf))
		}()
	}

	for _, bss := range options.BinarySectionSize {
		func() {
			try.To1(r.Seek(0, io.SeekStart))

			n := imapserver.ExtractBinarySectionSize(r, bss)
			w.WriteBinarySectionSize(bss, n)
		}()
	}

	return w.Close()
}

func (m *Mail) UID() imap.UID {
	n := m.GetInt("uid")
	return imap.UID(n)
}

func (m *Mail) Flags() (flags []imap.Flag) {
	ff := db.MailFlag(m.GetInt("flags"))
	return ff.Flags()
}

func (m *Mail) AddFlags(flags ...imap.Flag) {
	ff := db.MailFlag(m.GetInt("flags"))
	for _, f := range flags {
		f := db.ToMailFlag(f)
		ff |= f
	}
	m.Set("flags", ff)
}
func (m *Mail) SetFlags(flags ...imap.Flag) {
	ff := db.MailFlag(0)
	for _, f := range flags {
		f := db.ToMailFlag(f)
		ff |= f
	}
	m.Set("flags", ff)
}
func (m *Mail) DelFlags(flags ...imap.Flag) {
	ff := db.MailFlag(m.GetInt("flags"))
	for _, f := range flags {
		f := db.ToMailFlag(f)
		ff &= ^f
	}
	m.Set("flags", ff)
}
