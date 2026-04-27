package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		accs := try.To1(app.FindCachedCollectionByNameOrId(db.TableAccounts))
		msgs := try.To1(app.FindCachedCollectionByNameOrId(db.TableMessages))

		outbounds := core.NewBaseCollection(db.TableOutbounds, ID(db.TableOutbounds))
		outbounds.Fields.Add(
			&core.RelationField{
				Name: "from", Id: ID("from"), System: true,
				Required:     true,
				CollectionId: accs.Id, MaxSelect: 1, CascadeDelete: true,
			},
			&core.EmailField{
				Name: "to", Id: ID("to"), System: true,
				Required: true,
			},
			&core.RelationField{
				Name: "msg", Id: ID("msg"), System: true,
				Required:     true,
				CollectionId: msgs.Id, MaxSelect: 1, CascadeDelete: true,
			},
			&core.BoolField{
				Name: "delivered", Id: ID("delivered"), System: true,
				Required: false,
				Help:     "是否已送达. 送达后会被删除",
			},
			&core.DateField{
				Name: "next_retry_at", Id: ID("next_retry_at"), System: true,
				Required: false,
				Help:     "下次重试时间. 重试时间和创建时间相距过远时会停止重试并删除",
			},
		)
		addUpdatedFields(outbounds)
		outbounds.ListRule = nil
		outbounds.ViewRule = nil
		outbounds.CreateRule = nil
		outbounds.UpdateRule = nil
		outbounds.DeleteRule = nil
		try.To(app.Save(outbounds))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
