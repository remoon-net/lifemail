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
			&core.TextField{
				Name: "subject", Id: ID("subject"), System: true,
				Required: false,
				Max:      99999,
				Help:     "邮件字段: Subject",
			},
			&core.DateField{
				Name: "header_date", Id: ID("header_date"), System: true,
				Required: false,
				Help:     "邮件字段: DATE",
			},
			&core.JSONField{
				Name: "envelope", Id: ID("envelope"), System: true,
				Required: false, Hidden: true,
				MaxSize: 500 * units.KiB,
				Help:    "邮件信息: Envelope",
			},
			&core.JSONField{
				Name: "header", Id: ID("header"), System: true,
				Required: false,
				MaxSize:  500 * units.KiB,
				Help:     "邮件头部, 以便SEARCH使用",
			},
			&core.NumberField{
				Name: "size", Id: ID("size"), System: true,
				OnlyInt: true,
				Help:    "原始邮件大小",
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
			&core.TextField{
				Name: "old_name", Id: ID("old_name"), System: true,
				Required: false, Hidden: true,
				Max:  255,
				Help: "",
			},
			&core.BoolField{
				Name: "subscribed", Id: ID("subscribed"), System: true,
				Required: false,
				Help:     "",
			},
			&core.NumberField{
				Name: "uid_validity", Id: ID("uid_validity"), System: true,
				Required: false, Hidden: true,
				OnlyInt: true, Min: types.Pointer[float64](0),
				Help: "邮箱文件夹的自增实例id",
			},
			&core.NumberField{
				Name: "uid_next", Id: ID("uid_next"), System: true,
				Required: false, Hidden: true,
				OnlyInt: true, Min: types.Pointer[float64](0),
				Help: "mail 自增uid",
			},
			&core.NumberField{
				Name: "highest_modseq", Id: ID("highest_modseq"), System: true,
				Required: false, Hidden: true,
				OnlyInt: true, Min: types.Pointer[float64](0),
				Help: "mailbox HighestModSeq 只增不减",
			},
			&core.NumberField{
				Name: "attrs", Id: ID("attrs"), System: true,
				Required: false,
				Min:      types.Pointer[float64](0),
				Help:     "MailboxAttrs mask",
			},
		)
		addUpdatedFields(mailboxes)
		mailboxes.ListRule = types.Pointer("account = @request.auth.id")
		mailboxes.ViewRule = types.Pointer("account = @request.auth.id")
		mailboxes.CreateRule = nil // 不允许通过 rest api 接口创建, 只能通过邮箱 api 创建
		mailboxes.UpdateRule = types.Pointer("account = @request.auth.id")
		mailboxes.DeleteRule = types.Pointer("account = @request.auth.id")
		mailboxes.AddIndex("mailbox_name", true, "account,name", "") // 邮箱文件夹名在同一个用户下是唯一的
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
				Name: "flags", Id: ID("flags"), System: true,
				Required: false,
				Min:      types.Pointer[float64](0),
				Help:     "mail flags mask",
			},
			&core.NumberField{
				Name: "uid", Id: ID("uid"), System: true,
				Required: false,
				OnlyInt:  true, Min: types.Pointer[float64](0),
				Help: "自增uid",
			},
			&core.NumberField{
				Name: "modseq", Id: ID("modseq"), System: true,
				Required: false,
				OnlyInt:  true, Min: types.Pointer[float64](0),
				Help: "",
			},
		)
		addUpdatedFields(mails)
		mails.ListRule = types.Pointer("to = @request.auth.id")
		mails.ViewRule = types.Pointer("to = @request.auth.id")
		// 只允许通过imap协议进行修改
		mails.CreateRule = nil
		mails.UpdateRule = nil
		mails.DeleteRule = nil
		try.To(app.Save(mails))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
