package main

import (
	"context"
	"log"
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
	ctx := context.Background()
	eg, ctx := errgroup.WithContext(ctx)
	{
		ln := try.To1(net.Listen("tcp", "[::]:25"))
		defer ln.Close()
		app.OnServe().BindFunc(func(e *core.ServeEvent) error {
			eg.Go(func() error {
				srv, err := smtp.New(e.App)
				if err != nil {
					return err
				}
				log.Println("smtp server listen at:", ln.Addr().String())
				return srv.Serve(ln)
			})
			return e.Next()
		})
	}
	{
		ln := try.To1(net.Listen("tcp", "[::]:143"))
		defer ln.Close()
		app.OnServe().BindFunc(func(e *core.ServeEvent) error {
			eg.Go(func() error {
				srv := imap.New(e.App)
				log.Println("imap server listen at:", ln.Addr().String())
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
