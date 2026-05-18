package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"golang.org/x/sync/errgroup"
	_ "remoon.net/lifemail/db/migrations"
	"remoon.net/lifemail/imap"
	"remoon.net/lifemail/smtp"
)

func main() {
	app := pocketbase.New()
	var (
		listens []string
		tlsGet  string
	)
	{
		f := app.RootCmd.PersistentFlags()
		f.StringArrayVarP(&listens, "listen", "l", []string{
			"smtp://[::]:25",
			"smtp://[::]:587",
			"imap://[::]:143",
		}, "监听哪些协议(smtp://, smtps://, imap://, imaps://)")
		f.StringVar(&tlsGet, "tls", "", "和caddy一样")
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
	logger := app.Logger()
	ctx := context.Background()
	eg, ctx := errgroup.WithContext(ctx)

	var srvCert atomic.Pointer[tls.Certificate]
	getCert := func() (err error) {
		if tlsGet == "" {
			return
		}
		defer err0.Then(&err, nil, func() {
			logger.Error("获取tls证书出错", "error", err)
		})
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req := try.To1(http.NewRequestWithContext(ctx, http.MethodGet, tlsGet, nil))
		resp := try.To1(http.DefaultClient.Do(req))
		defer resp.Body.Close()
		if code := resp.StatusCode; !(code >= 200 && code < 300) {
			return fmt.Errorf("状态码为 %d, 不在 200 区间", code)
		}
		buf := try.To1(io.ReadAll(resp.Body))
		cert := try.To1(tls.X509KeyPair(buf, buf))
		srvCert.Store(&cert)
		return nil
	}
	app.Cron().Add("tls-cert-get", "0 2 * * *", func() {
		getCert()
	})
	var tc *tls.Config = nil
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if tlsGet == "" {
			return e.Next()
		}
		tc = &tls.Config{
			GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return srvCert.Load(), nil
			},
		}
		if err := getCert(); err != nil {
			return err
		}
		return e.Next()
	})

	var listeners []net.Listener
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		defer func() {
			lns := listeners
			listeners = listeners[:0]
			for _, ln := range lns {
				ln.Close()
			}
		}()
		return e.Next()
	})
	app.OnServe().BindFunc(func(e *core.ServeEvent) (err error) {
		defer err0.Then(&err, nil, nil)
		smtpSrv := try.To1(smtp.New(app, tc))
		imapSrv := imap.New(app, tc)

		for _, l := range listens {
			u := try.To1(url.Parse(l))
			switch u.Scheme {
			case "smtp", "smtp+insecure":
				addr := u.Host
				if port := u.Port(); port == "" {
					addr += ":25"
				}
				ln := try.To1(net.Listen("tcp", addr))
				listeners = append(listeners, ln)
				eg.Go(func() error {
					logger.Warn("smtp server is running", "addr", ln.Addr().String())
					return smtpSrv.Serve(ln)
				})
			case "smtps":
				addr := u.Host
				if port := u.Port(); port == "" {
					addr += ":465"
				}
				ln := try.To1(tls.Listen("tcp", addr, tc))
				listeners = append(listeners, ln)
				eg.Go(func() error {
					logger.Warn("smtps server is running", "addr", ln.Addr().String())
					return smtpSrv.Serve(ln)
				})
			case "imap", "imap+insecure":
				addr := u.Host
				if port := u.Port(); port == "" {
					addr += ":143"
				}
				ln := try.To1(net.Listen("tcp", addr))
				listeners = append(listeners, ln)
				eg.Go(func() error {
					logger.Warn("imap server is running", "addr", ln.Addr().String())
					return imapSrv.Serve(ln)
				})
			case "imaps":
				addr := u.Host
				if port := u.Port(); port == "" {
					addr += ":993"
				}
				ln := try.To1(tls.Listen("tcp", addr, tc))
				listeners = append(listeners, ln)
				eg.Go(func() error {
					logger.Warn("imaps server is running", "addr", ln.Addr().String())
					return imapSrv.Serve(ln)
				})
			}
		}
		return e.Next()
	})
	go func() {
		eg.Wait()
		ev := &core.TerminateEvent{App: app}
		app.OnTerminate().Trigger(ev)
	}()

	try.To(app.Start())
}
