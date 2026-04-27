package migrations

import (
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

		accounts := core.NewAuthCollection(db.TableAccounts, ID(db.TableAccounts))
		idF := accounts.Fields.GetByName("id").(*core.TextField)
		idF.Min = 4
		idF.Max = 64
		idF.Pattern = "^[a-z0-9]+$"
		idF.AutogeneratePattern = ""
		addUpdatedFields(accounts)
		accounts.ListRule = types.Pointer("id = @request.auth.id")
		accounts.ViewRule = types.Pointer("id = @request.auth.id")
		accounts.CreateRule = types.Pointer("")
		accounts.UpdateRule = types.Pointer("id = @request.auth.id")
		accounts.DeleteRule = types.Pointer("id = @request.auth.id")
		try.To(app.Save(accounts))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
