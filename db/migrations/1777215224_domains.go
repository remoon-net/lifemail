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

		domains := core.NewBaseCollection(db.TableDomains, ID(db.TableDomains))
		domains.Fields.Add(
			&core.TextField{
				Name: "domain", Id: ID("domain"), System: true,
				Required: true, Presentable: true,
			},
		)
		addUpdatedFields(domains)
		try.To(app.Save(domains))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
