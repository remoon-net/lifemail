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

		accs := core.NewAuthCollection(db.TableAccounts, ID(db.TableAccounts))
		idF := accs.Fields.GetByName("id").(*core.TextField)
		idF.Min = 4
		idF.Max = 64
		idF.Pattern = "^[a-z0-9]+$"
		idF.AutogeneratePattern = ""
		accs.Fields.Add(
			&core.NumberField{
				Name: "uid_validity_next", Id: ID("uid_validity_next"), System: true,
				Required: false, Hidden: true,
				OnlyInt: true, Min: types.Pointer[float64](0),
				Help: "邮箱文件夹的自增实例id",
			},
		)
		addUpdatedFields(accs)
		accs.ListRule = types.Pointer("id = @request.auth.id")
		accs.ViewRule = types.Pointer("id = @request.auth.id")
		accs.CreateRule = types.Pointer("")
		accs.UpdateRule = types.Pointer("id = @request.auth.id")
		accs.DeleteRule = types.Pointer("id = @request.auth.id")
		try.To(app.Save(accs))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
