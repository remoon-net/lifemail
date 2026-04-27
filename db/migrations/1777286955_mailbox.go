package migrations

import (
	"github.com/docker/go-units"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		accs := try.To1(app.FindCollectionByNameOrId(db.TableAccounts))

		messages := core.NewBaseCollection(db.TableMessages, ID(db.TableMessages))
		messages.Fields.Add(
			&core.RelationField{
				Name: "account", Id: ID("account"), System: true,
				Required:     false,
				CollectionId: accs.Id, MaxSelect: 1,
				Help: "记录下发送时的登录账户",
			},
			&core.EmailField{
				Name: "from", Id: ID("from"), System: true,
				Required: false,
				Help:     "记录下from字段",
			},
			&core.RelationField{
				Name: "inbox", Id: ID("inbox"), System: true,
				Required: false, Hidden: true,
				CollectionId: accs.Id, MaxSelect: 99999,
				Help: "本机邮箱账户",
			},
			&core.JSONField{
				Name: "outbox", Id: ID("outbox"), System: true,
				Required: false, Hidden: true,
				MaxSize: 100 * units.KiB,
				Help:    "要发送外部的邮箱地址",
			},
			&core.FileField{
				Name: "raw", Id: ID("raw"), System: true,
				Required:  true,
				MaxSelect: 1, MaxSize: 100 * units.MiB, Protected: true,
				Help: "原始邮件信息",
			},
		)
		addUpdatedFields(messages)
		messages.ListRule = types.Pointer("inbox ?= @request.auth.id")
		messages.ViewRule = types.Pointer("inbox ?= @request.auth.id")
		messages.CreateRule = nil
		messages.UpdateRule = nil
		messages.DeleteRule = nil
		try.To(app.Save(messages))

		mailboxes := core.NewBaseCollection(db.TableMailboxes, ID(db.TableMailboxes))
		mailboxes.Fields.Add(
			&core.RelationField{
				Name: "account", Id: ID("account"), System: true,
				Required: true, Presentable: true,
				CollectionId: accs.Id, MaxSelect: 1, CascadeDelete: true,
				Help: "所属用户",
			},
			&core.TextField{
				Name: "name", Id: ID("name"), System: true,
				Required: true, Presentable: true,
				Min: 1, Max: 255,
				Help: "mailbox name",
			},
			&core.NumberField{
				Name: "uid_next", Id: ID("uid_next"), System: true,
				Required: false,
				OnlyInt:  true, Min: types.Pointer[float64](0),
				Help: "mailbox 自增uid",
			},
		)
		addUpdatedFields(mailboxes)
		mailboxes.ListRule = types.Pointer("account = @request.auth.id")
		mailboxes.ViewRule = types.Pointer("account = @request.auth.id")
		mailboxes.CreateRule = types.Pointer("account = @request.auth.id")
		mailboxes.UpdateRule = types.Pointer("account = @request.auth.id")
		mailboxes.DeleteRule = types.Pointer("account = @request.auth.id")
		try.To(app.Save(mailboxes))

		mails := core.NewBaseCollection(db.TableMails, ID(db.TableMails))
		mails.Fields.Add(
			&core.RelationField{
				Name: "to", Id: ID("to"), System: true,
				Required:     true,
				CollectionId: accs.Id, MaxSelect: 1, CascadeDelete: true,
				Help: "所属用户",
			},
			&core.RelationField{
				Name: "msg", Id: ID("msg"), System: true,
				Required:     true,
				CollectionId: messages.Id, MaxSelect: 1, CascadeDelete: true,
				Help: "信箱消息",
			},
			&core.RelationField{
				Name: "mailbox", Id: ID("mailbox"), System: true,
				Required:     true,
				CollectionId: mailboxes.Id, MaxSelect: 1, CascadeDelete: true,
				Help: "所属信箱",
			},
			&core.NumberField{
				Name: "uid", Id: ID("uid"), System: true,
				Required: false,
				OnlyInt:  true, Min: types.Pointer[float64](0),
				Help: "自增uid",
			},
		)
		addUpdatedFields(mails)
		mails.ListRule = types.Pointer("to = @request.auth.id")
		mails.ViewRule = types.Pointer("to = @request.auth.id")
		mails.CreateRule = nil
		mails.UpdateRule = types.Pointer("to = @request.auth.id")
		mails.DeleteRule = types.Pointer("to = @request.auth.id")
		try.To(app.Save(mails))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
