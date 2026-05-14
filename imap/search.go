package imap

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/emersion/go-message/textproto"

	"github.com/emersion/go-imap/v2"
	"github.com/pocketbase/pocketbase/core"
	"remoon.net/lifemail/db"
)

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
