package imap

import (
	"encoding/json"
	"log"
	"reflect"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

// todo
func (sess *Session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	mbox := sess.mailbox.Load()
	if mbox == nil {
		return nil, nil
	}

	var iterNumSet imap.NumSet = imap.SeqSet{imap.SeqRange{Start: 1, Stop: 0}}
	var requireMsgsTable bool
	if q := CanSQLSearch(&dbx.HashExp{"mailbox": mbox.Id}, criteria, &requireMsgsTable); q != nil {
		numSet := imap.UIDSetNum()
		sq := sess.app.DB().
			Select("uid").
			From(db.TableMails).
			Where(q)
		if requireMsgsTable {
			sq = sq.LeftJoin(db.TableMessages, dbx.NewExp("messages.id = mails.msg"))
		}
		rows, err := sq.Rows()
		try.To(err)
		for rows.Next() {
			var uid uint32
			try.To(rows.Scan(&uid))
			numSet.AddNum(imap.UID(uid))
		}
		iterNumSet = numSet
	}

	var (
		d      imap.SearchData
		seqSet imap.SeqSet
		uidSet imap.UIDSet
	)
	for seqNum, m := range mbox.Iter(false, iterNumSet) {
		if !m.search(mbox.app, seqNum, criteria) {
			continue
		}

		uidSet.AddNum(m.UID())
		var num uint32
		switch kind {
		case imapserver.NumKindSeq:
			num = seqNum
			seqSet.AddNum(seqNum)
		case imapserver.NumKindUID:
			num = uint32(m.UID())
		}
		if d.Min == 0 || num < d.Min {
			d.Min = num
		}
		if d.Max == 0 || num > d.Min {
			d.Max = num
		}
		d.Count++
		// 最多搜索1000个, 多了就不再返回
		if d.Count > 1000 {
			break
		}
	}
	switch kind {
	case imapserver.NumKindSeq:
		d.All = seqSet
	case imapserver.NumKindUID:
		d.All = uidSet
	}
	if options.ReturnSave {
		mbox.searchRes = uidSet
	}
	log.Println("ddddddddddddddddd", d)
	return &d, nil
}

func CanSQLSearch(q dbx.Expression, c *imap.SearchCriteria, requireMsgsTable *bool) dbx.Expression {
	isSame := func(q1 dbx.Expression) func(q2 dbx.Expression) bool {
		p1 := reflect.ValueOf(q).Pointer()
		return func(q2 dbx.Expression) bool {
			p2 := reflect.ValueOf(q).Pointer()
			return p1 == p2
		}
	}(q)
	switch {
	case
		len(c.SeqNum) > 0 && c.SeqNum[0].String() != "1:*",
		len(c.UID) > 0 && c.UID[0].String() != "1:*",
		false:
		return nil
	}

	if !c.Since.IsZero() {
		q2 := dbx.NewExp("created >= {:t}", dbx.Params{"t": c.Since})
		q = dbx.And(q, q2)
	}
	if !c.Before.IsZero() {
		q2 := dbx.NewExp("created < {:t}", dbx.Params{"t": c.Since})
		q = dbx.And(q, q2)
	}
	if !c.SentSince.IsZero() {
		*requireMsgsTable = true
		q2 := dbx.NewExp("messages.header_date >= {:t}", dbx.Params{"t": c.SentSince})
		q = dbx.And(q, q2)
	}
	if !c.SentBefore.IsZero() {
		*requireMsgsTable = true
		q2 := dbx.NewExp("messages.header_date < {:t}", dbx.Params{"t": c.SentSince})
		q = dbx.And(q, q2)
	}

	switch {
	case
		len(c.Header) > 0,
		len(c.Body) > 0,
		len(c.Text) > 0,
		false:
		return nil
	}

	if len(c.Flag) > 0 {
		var ff db.MailFlag
		for _, f := range c.Flag {
			f := db.ToMailFlag(f)
			ff |= f
		}
		q2 := dbx.NewExp("flags & {:f} = {:f}", dbx.Params{"f": ff.ToInt()})
		q = dbx.And(q, q2)
	}
	if len(c.NotFlag) > 0 {
		var ff db.MailFlag
		for _, f := range c.NotFlag {
			f := db.ToMailFlag(f)
			ff |= f
		}
		q2 := dbx.NewExp("flags & {:f} = 0", dbx.Params{"f": ff.ToInt()})
		q = dbx.And(q, q2)
	}

	if c.Larger > 0 {
		*requireMsgsTable = true
		q2 := dbx.NewExp("messages.size >= {:t}", dbx.Params{"t": c.Larger})
		q = dbx.And(q, q2)
	}
	if c.Smaller > 0 {
		*requireMsgsTable = true
		q2 := dbx.NewExp("messages.size < {:t}", dbx.Params{"t": c.Smaller})
		q = dbx.And(q, q2)
	}

	switch {
	case
		len(c.Not) > 0,
		len(c.Or) > 0,
		false:
		return nil
	}
	if isSame(q) {
		return nil
	}
	return q
}

func (m *Mail) search(app core.App, seqNum uint32, criteria *imap.SearchCriteria) bool {
	var ss func(criteria *imap.SearchCriteria) bool
	matched := true
	uid := m.UID()
	created := m.GetDateTime("created").Time()
	flags := m.Flags()

	msg, err := app.FindRecordById(db.TableMessages, m.GetString("msg"))
	if err != nil {
		matched = false
	}
	sent := msg.GetDateTime("header_date").Time()
	headerMap := map[string][]string{}
	headerStr := msg.GetString("header")
	if err := json.Unmarshal([]byte(headerStr), &headerMap); err != nil {
		// do nothing
	}
	header := textproto.HeaderFromMap(headerMap)

	ss = func(c *imap.SearchCriteria) (v bool) {
		if !matched {
			return false
		}
		defer func() {
			matched = v
		}()

		for _, s := range c.SeqNum {
			if !s.Contains(seqNum) {
				return false
			}
		}

		for _, s := range c.UID {
			if !s.Contains(uid) {
				return false
			}
		}

		if !c.Since.IsZero() && created.Before(c.Since) {
			return false
		}
		if !c.Before.IsZero() && created.After(c.Before) {
			return false
		}
		if !c.SentSince.IsZero() && sent.Before(c.SentSince) {
			return false
		}
		if !c.SentBefore.IsZero() && sent.After(c.SentBefore) {
			return false
		}

		for _, h := range c.Header {
			if !matchHeader(header, h) {
				return false
			}
		}
		if false {
			for range c.Body {
				// 无视
			}
			for range c.Text {
				// 无视
			}
		}

		for _, f := range c.Flag {
			if !slices.Contains(flags, f) {
				return false
			}
		}
		for _, f := range c.NotFlag {
			if slices.Contains(flags, f) {
				return false
			}
		}

		for _, c := range c.Not {
			if ss(&c) {
				return false
			}
		}
		for _, cc := range c.Or {
			c1 := ss(&cc[0])
			c2 := ss(&cc[1])
			if !c1 && !c2 {
				return false
			}
		}

		return true
	}
	return ss(criteria)
}

func matchHeader(header textproto.Header, h imap.SearchCriteriaHeaderField) bool {
	vals := header.Values(h.Key)
	if h.Value == "" {
		return len(vals) > 0
	}
	for _, val := range vals {
		if strings.Contains(val, h.Value) {
			return true
		}
	}
	return false
}
