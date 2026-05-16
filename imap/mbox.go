package imap

import (
	"log"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/emersion/go-imap/v2"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

type Mailbox struct {
	core.BaseRecordProxy
	app           core.App
	rw            sync.RWMutex
	ReadOnly      bool
	uids          []MailUID
	highestModseq atomic.Uint64 // cached highest modseq
	searchRes     imap.UIDSet
}

type MailUID struct {
	UID imap.UID
	ID  string
}

func NewMailbox(app core.App, r *core.Record) *Mailbox {
	m := &Mailbox{
		app: app,
	}
	m.SetProxyRecord(r)
	if r != nil {
		m.highestModseq.Store(m.HighestModseq())
	}
	return m
}

func (mbox *Mailbox) Close() error {
	return nil
}

func (mbox *Mailbox) UIDNext() imap.UID {
	n := mbox.GetInt("uid_next")
	return imap.UID(n)
}

func (mbox *Mailbox) UIDValidity() uint32 {
	v := mbox.GetInt("uid_validity")
	return uint32(v)
}
func (mbox *Mailbox) HighestModseq() uint64 {
	v := mbox.GetInt("highest_modseq")
	return uint64(v)
}

func (mbox *Mailbox) Attrs() []imap.MailboxAttr {
	attrs := db.MailboxAttr(mbox.GetInt("attrs"))
	return attrs.Attrs()
}
func (mbox *Mailbox) Name() string { return mbox.GetString("name") }
func (mbox *Mailbox) Status() (_ *imap.StatusData, err error) {
	app := mbox.app
	defer err0.Then(&err, nil, nil)

	all := func() uint32 {
		all := try.To1(app.CountRecords(db.TableMails, dbx.HashExp{"mailbox": mbox.Id}))
		return uint32(all)
	}()
	msgLimit := func() uint32 {
		msgs := try.To1(app.FindCollectionByNameOrId(db.TableMessages))
		raw := msgs.Fields.GetByName("raw").(*core.FileField)
		return uint32(raw.MaxSize)
	}()
	unseen := func() uint32 {
		q := dbx.NewExp("flags & {:f} = 0", dbx.Params{"f": db.FlagSeen.ToInt()})
		c := try.To1(app.CountRecords(db.TableMails, q))
		return uint32(c)
	}()
	deleted := func() uint32 {
		q := dbx.NewExp("flags & {:f} != 0", dbx.Params{"f": db.FlagDeleted.ToInt()})
		c := try.To1(app.CountRecords(db.TableMails, q))
		return uint32(c)
	}()
	mh := mbox.HighestModseq()

	d := &imap.StatusData{
		Mailbox: mbox.Name(),

		NumMessages: &all,
		NumRecent:   types.Pointer[uint32](0),
		UIDNext:     mbox.UIDNext(),
		UIDValidity: mbox.UIDValidity(),
		NumUnseen:   &unseen,
		NumDeleted:  &deleted,
		Size:        types.Pointer[int64](0), // 无需实现

		AppendLimit:    &msgLimit,
		DeletedStorage: types.Pointer[int64](0),
		HighestModSeq:  mh,
	}
	return d, nil
}

func (mbox *Mailbox) ListData() (ld *imap.ListData, err error) {
	defer err0.Then(&err, nil, nil)
	status := try.To1(mbox.Status())
	ld = &imap.ListData{
		Attrs:     mbox.Attrs(),
		Delim:     mailboxDelim,
		Mailbox:   status.Mailbox,
		Status:    status,
		ChildInfo: nil, // 目前不返回子文件夹的订阅状态
		OldName:   mbox.GetString("old_name"),
	}
	return ld, nil
}

// 逆向迭代, 最新的先返回
func (mbox *Mailbox) Iter(uidOnly bool, numSet ...imap.NumSet) func(func(seqNum uint32, mail *Mail) bool) {
	mbox.rw.RLock()
	uids := mbox.uids
	mbox.rw.RUnlock()
	maxSeqNum := len(uids)
	maxUIDNum := mbox.UIDNext() - 1
	if len(numSet) == 0 {
		s := imap.SeqSetNum()
		s.AddRange(1, 0)
		numSet = []imap.NumSet{
			s,
		}
	}
	log.Println("888888888888888888888888", numSet)
	for i, ss := range numSet {
		if imap.IsSearchRes(ss) {
			numSet[i] = mbox.searchRes
		}
		switch ss := ss.(type) {
		case imap.SeqSet:
			for i := range ss {
				r := &ss[i]
				staticNumRange(&r.Start, &r.Stop, uint32(maxSeqNum))
			}
		case imap.UIDSet:
			for i := range ss {
				r := &ss[i]
				staticNumRange((*uint32)(&r.Start), (*uint32)(&r.Stop), uint32(maxUIDNum))
			}
		}
	}
	log.Println("77777777777777777", numSet)
	return func(yield func(uint32, *Mail) bool) {
		for seqNum, mu := range slices.Backward(uids) {
			seqNum := seqNum + 1
			contains := func() bool {
				for _, s := range numSet {
					switch s := s.(type) {
					case imap.SeqSet:
						if s.Contains(uint32(seqNum)) {
							return true
						}
					case *imap.SeqSet:
						if s.Contains(uint32(seqNum)) {
							return true
						}
					case *imap.UIDSet:
						if s.Contains(mu.UID) {
							return true
						}
					case imap.UIDSet:
						if s.Contains(mu.UID) {
							return true
						}
					}
				}
				return false
			}()
			if !contains {
				continue
			}
			var m *Mail
			if uidOnly {
				mm, err := mbox.app.FindCollectionByNameOrId(db.TableMails)
				if err != nil {
					continue
				}
				r := core.NewRecord(mm)
				m = NewMail(r)
				m.Id = mu.ID
				m.Set("uid", mu.UID)
			} else {
				r, err := mbox.app.FindRecordById(db.TableMails, mu.ID)
				if err != nil {
					continue
				}
				m = NewMail(r)
			}
			if !yield(uint32(seqNum), m) {
				return
			}
		}
	}
}

func staticNumRange(start, stop *uint32, max uint32) {
	dyn := false
	if *start == 0 {
		*start = max
		dyn = true
	}
	if *stop == 0 {
		*stop = max
		dyn = true
	}
	if dyn && *start > *stop {
		*start, *stop = *stop, *start
	}
}

func dPtr[T any](v *T) (d T) {
	if v == nil {
		return
	}
	return *v
}
