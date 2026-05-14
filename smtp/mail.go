package smtp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func SaveMail(tx core.App, mail *core.Record) error {
	mailbox, err := tx.FindRecordById(db.TableMailboxes, mail.GetString("mailbox"))
	if err != nil {
		return err
	}
	if mail.IsNew() {
		uidNext := mailbox.GetInt("uid_next")
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
