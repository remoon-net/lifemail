package caldav

import (
	"context"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func Bind(app core.App) {
	h := &caldav.Handler{
		Backend: &Backend{app},
		Prefix:  "/dav",
	}
	dh := func(e *core.RequestEvent) error {
		h.ServeHTTP(e.Response, e.Request)
		return nil
	}
	authHandler := &hook.Handler[*core.RequestEvent]{
		Func: func(e *core.RequestEvent) (err error) {
			defer err0.Then(&err, nil, nil)
			r := e.Request
			user, pass, ok := r.BasicAuth()
			if !ok {
				e.Response.Header().Add("WWW-Authenticate", `Basic realm="Please provide your IMAP credentials", charset="UTF-8"`)
				return apis.NewUnauthorizedError("HTTP Basic auth is required", nil)
			}
			acc := try.To1(e.App.FindRecordById(db.TableAccounts, user))
			if ok := acc.ValidatePassword(pass); !ok {
				return apis.NewUnauthorizedError("Invalid username or password", nil)
			}
			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxAuthKey, acc)
			e.Request = r.WithContext(ctx)
			return e.Next()
		},
	}
	fixFinfo := func(e *core.RecordEvent) (err error) {
		defer err0.Then(&err, nil, nil)
		r := e.Record
		ff := r.GetUnsavedFiles("ics")
		if len(ff) == 0 {
			return e.Next()
		}
		f := ff[0]
		fr := try.To1(f.Reader.Open())
		defer fr.Close()
		cal := try.To1(ical.NewDecoder(fr).Decode())
		mod, _ := cal.Props.DateTime(ical.PropLastModified, nil)
		if mod.IsZero() {
			mod, _ = cal.Props.DateTime(ical.PropCreated, nil)
		}
		if mod.IsZero() {
			mod = time.Now()
		}
		mod2, _ := types.ParseDateTime(mod)
		r.Set("filename", f.OriginalName)
		r.Set("mod_time", mod2)
		r.Set("size", f.Size)
		etag := fmt.Sprintf("%x-%x", mod.UnixNano(), f.Size)
		r.Set("etag", etag)
		return e.Next()
	}
	app.OnRecordCreate(db.TableCalendarObjects).BindFunc(fixFinfo)
	app.OnRecordUpdate(db.TableCalendarObjects).BindFunc(fixFinfo)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Router.Any("/.well-known/caldav", dh).Bind(authHandler)
		g := e.Router.Group("/dav")
		g.Unbind(apis.DefaultCorsMiddlewareId)
		g.Bind(authHandler)
		g.Any("", dh)
		g.Any("/{path...}", dh)
		return e.Next()
	})
}
