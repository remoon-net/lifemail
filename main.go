package main

import (
	"context"
	"net"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0/try"
	"golang.org/x/sync/errgroup"
	_ "remoon.net/lifemail/db/migrations"
	"remoon.net/lifemail/imap"
	"remoon.net/lifemail/smtp"
)

func main() {
	app := pocketbase.New()
	var (
		smtpMTA  string
		smtpMUA  string
		imapAddr string
	)
	{
		f := app.RootCmd.PersistentFlags()
		f.StringVar(&smtpMTA, "smtp-mta", "[::]:25", "smtp relay addr")
		f.StringVar(&smtpMUA, "smtp-mua", "[::]:587", "smtp submission addr")
		f.StringVar(&imapAddr, "imap", "[::]:143", "imap server addr")
	}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.InstallerFunc = func(app core.App, systemSuperuser *core.Record, baseURL string) error {
			su := core.NewRecord(systemSuperuser.Collection())
			su.SetEmail("lifemail@remoon.net")
			su.SetPassword("lifemail@remoon.net")
			return app.Save(su)
		}
		return e.Next()
	})
	try.To(app.Bootstrap())
	logger := app.Logger()
	ctx := context.Background()
	eg, ctx := errgroup.WithContext(ctx)
	if smtpMTA != "" {
		ln := try.To1(net.Listen("tcp", smtpMTA))
		defer ln.Close()
		app.OnServe().BindFunc(func(e *core.ServeEvent) error {
			eg.Go(func() error {
				srv, err := smtp.New(e.App)
				if err != nil {
					return err
				}
				logger.Warn("smtp server is running", "addr", ln.Addr().String())
				return srv.Serve(ln)
			})
			return e.Next()
		})
	}
	if smtpMUA != "" {
		ln := try.To1(net.Listen("tcp", smtpMUA))
		defer ln.Close()
		app.OnServe().BindFunc(func(e *core.ServeEvent) error {
			eg.Go(func() error {
				srv, err := smtp.New(e.App)
				if err != nil {
					return err
				}
				logger.Warn("smtp server is running", "addr", ln.Addr().String())
				return srv.Serve(ln)
			})
			return e.Next()
		})
	}
	if imapAddr != "" {
		ln := try.To1(net.Listen("tcp", imapAddr))
		defer ln.Close()
		imap.Bind(app)
		app.OnServe().BindFunc(func(e *core.ServeEvent) error {
			eg.Go(func() error {
				srv := imap.New(e.App)
				logger.Warn("imap server is running", "addr", ln.Addr().String())
				return srv.Serve(ln)
			})
			return e.Next()
		})
	}
	go func() {
		eg.Wait()
		ev := &core.TerminateEvent{App: app}
		app.OnTerminate().Trigger(ev)
	}()
	try.To(app.Start())
}
