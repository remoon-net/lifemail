package imap

import (
	"slices"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

type MailUpdate struct {
	mail    *Mail
	deleted bool
	created bool
	updated bool
}

func (mbox *Mailbox) Poll(w *imapserver.UpdateWriter, allowExpunge bool) (err error) {
	defer err0.Then(&err, nil, nil)
	mbox.rw.Lock()
	defer mbox.rw.Unlock()
	updates := mbox.updates
	defer func() {
		mbox.updates = updates
	}()
	for i, u := range mbox.updates {
		m := u.mail
		if u.deleted && allowExpunge {
			uids2 := mbox.uids[:0]
			var seqNumDeleted uint32
			for seqNum, mu := range mbox.uids {
				seqNum := seqNum + 1
				if mu.ID == m.Id {
					seqNumDeleted = uint32(seqNum)
					continue
				}
				uids2 = append(uids2, mu)
			}
			try.To(w.WriteExpunge(seqNumDeleted))
			mbox.uids = uids2
			n := len(mbox.uids)
			try.To(w.WriteNumMessages(uint32(n)))
		}
		if u.updated {
			seqNum := slices.IndexFunc(mbox.uids, func(mu MailUID) bool {
				return mu.ID == m.Id
			})
			seqNum += 1
			if seqNum > 0 {
				try.To(w.WriteMessageFlags(uint32(seqNum), m.UID(), m.Flags()))
			}
		}
		if u.created {
			mu := MailUID{
				UID: m.UID(),
				ID:  m.Id,
			}
			mbox.uids = append(mbox.uids, mu)
			n := len(mbox.uids)
			try.To(w.WriteNumMessages(uint32(n)))
		}
		updates = mbox.updates[i+1:]
	}
	return nil
}

type Subscriber struct {
	unsubs []func()
}

func (s *Subscriber) Unsubscribe() {
	for _, unsub := range s.unsubs {
		if unsub != nil {
			unsub()
		}
	}
}

func (mbox *Mailbox) subscribeDB() {
	app := mbox.app
	b := &Subscriber{}
	{
		h := app.OnRecordAfterCreateSuccess(db.TableMails)
		c := h.BindFunc(func(e *core.RecordEvent) error {
			if e.Record.GetString("mailbox") == mbox.Id {
				m := NewMail(e.Record)
				mbox.rw.Lock()
				mbox.updates = append(mbox.updates, MailUpdate{
					mail:    m,
					created: true,
				})
				mbox.rw.Unlock()
			}
			return e.Next()
		})
		b.unsubs = append(b.unsubs, func() { h.Unbind(c) })
	}
	{
		h := app.OnRecordAfterUpdateSuccess(db.TableMails)
		c := h.BindFunc(func(e *core.RecordEvent) error {
			if e.Record.GetString("mailbox") == mbox.Id {
				m := NewMail(e.Record)
				mbox.rw.Lock()
				mbox.updates = append(mbox.updates, MailUpdate{
					mail:    m,
					updated: true,
				})
				mbox.rw.Unlock()
			}
			return e.Next()
		})
		b.unsubs = append(b.unsubs, func() { h.Unbind(c) })
	}
	{
		h := app.OnRecordAfterDeleteSuccess(db.TableMails)
		c := h.BindFunc(func(e *core.RecordEvent) error {
			if e.Record.GetString("mailbox") == mbox.Id {
				m := NewMail(e.Record)
				mbox.rw.Lock()
				mbox.updates = append(mbox.updates, MailUpdate{
					mail:    m,
					deleted: true,
				})
				mbox.rw.Unlock()
			}
			return e.Next()
		})
		b.unsubs = append(b.unsubs, func() { h.Unbind(c) })
	}
	mbox.subscriber.Store(b)
}

func (mbox *Mailbox) Close() error {
	if l := mbox.subscriber.Swap(nil); l != nil {
		l.Unsubscribe()
	}
	return nil
}

func (mbox *Mailbox) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	app := mbox.app
	poll := func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		go mbox.Poll(w, true)
		return nil
	}
	{
		h := app.OnRecordAfterCreateSuccess(db.TableMails)
		c := h.BindFunc(poll)
		defer h.Unbind(c)
	}
	{
		h := app.OnRecordAfterUpdateSuccess(db.TableMails)
		c := h.BindFunc(poll)
		defer h.Unbind(c)
	}
	{
		h := app.OnRecordAfterDeleteSuccess(db.TableMails)
		c := h.BindFunc(poll)
		defer h.Unbind(c)
	}
	<-stop
	return nil
}

type UpdateMsg struct {
}
