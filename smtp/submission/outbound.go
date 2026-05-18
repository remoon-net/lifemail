package submission

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"golang.org/x/sync/errgroup"
	"remoon.net/lifemail/db"
)

func Bind(app core.App) {
	oc := newOutboundCenter(app)
	app.OnRecordAfterCreateSuccess(db.TableOutbounds).BindFunc(func(e *core.RecordEvent) error {
		oc.SendCh(e.Record)
		return e.Next()
	})
	var cancel context.CancelFunc
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		ctx := context.Background()
		ctx, cancel = context.WithCancel(ctx)
		go oc.StartSendLoop(ctx)
		return e.Next()
	})
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		if cancel != nil {
			cancel()
		}
		return e.Next()
	})
}

type OutboundCenter struct {
	app core.App
	ch  chan *core.Record
}

func newOutboundCenter(app core.App) *OutboundCenter {
	return &OutboundCenter{
		app: app,
		ch:  make(chan *core.Record),
	}
}

func (oc *OutboundCenter) SendCh(out *core.Record) {
	select {
	case oc.ch <- out:
	default:
	}
}

const Day = 24 * time.Hour

func (oc *OutboundCenter) StartSendLoop(ctx context.Context) {
	nextTryDuration := 60 * time.Second
	t := time.AfterFunc(nextTryDuration, func() {
		oc.SendCh(nil)
	})
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case out := <-oc.ch:
			func() (err error) {
				defer err0.Then(&err, nil, nil)
				ctx, cancel := context.WithTimeout(ctx, nextTryDuration)
				defer cancel()

				msend := oc.getMailSend()
				if out != nil {
					return oc.SendOut(ctx, msend, out)
				}
				now := time.Now()
				day7 := now.Add(-7 * Day)
				q := "{:day7} <= next_retry_at && next_retry_at < {:now} && delivered = false"
				p := dbx.Params{
					"day7": day7,
					"now":  now,
				}
				outs := try.To1(oc.app.FindRecordsByFilter(db.TableOutbounds, q, "-next_retry_at", 0, 0, p))
				for outs := range slices.Chunk(outs, 100) {
					eg := new(errgroup.Group)
					for _, out := range outs {
						eg.Go(func() error {
							return oc.SendOut(ctx, msend, out)
						})
					}
					eg.Wait()
				}
				return
			}()
		}
		t.Reset(nextTryDuration)
	}
}

type MailSend func(from string, to string, r io.Reader) error

func (oc *OutboundCenter) getMailSend() MailSend {
	sc := oc.app.Settings().SMTP
	if !sc.Enabled {
		return nativeSend
	}
	var auth sasl.Client
	switch sc.AuthMethod {
	case "PLAIN":
		auth = sasl.NewPlainClient("", sc.Username, sc.Password)
	case "LOGIN":
		auth = sasl.NewLoginClient(sc.Username, sc.Password)
	}
	addr := net.JoinHostPort(sc.Host, fmt.Sprintf("%d", sc.Port))
	if sc.TLS {
		return func(from, to string, r io.Reader) error {
			return smtp.SendMailTLS(addr, auth, from, []string{to}, r)
		}
	}
	return func(from, to string, r io.Reader) error {
		return smtp.SendMail(addr, auth, from, []string{to}, r)
	}
}

func (oc *OutboundCenter) SendOut(ctx context.Context, msend MailSend, out *core.Record) (err error) {
	to := out.GetString("to")
	logger := oc.app.Logger().With(
		"out", out.Id,
		"to", "to",
		"msg", out.GetString("msg"),
	)
	defer err0.Then(&err, nil, func() {
		logger.Error("信件投递失败", "error", err)
		c := out.GetDateTime("created")
		n := out.GetDateTime("next_retry_at")
		d := max(n.Sub(c), 5*time.Minute)
		if d > 7*Day {
			g, _ := types.ParseDateTime(time.Time{})
			out.Set("next_retry_at", g)
			// todo: 这个时候应该生成退信
		} else {
			n.Add(d)
			out.Set("next_retry_at", n)
		}
		if err := oc.app.Save(out); err != nil {
			logger.Error("更新out失败", "error", err)
		}
	})
	msg := try.To1(oc.app.FindRecordById(db.TableMessages, out.GetString("msg")))
	fs := try.To1(oc.app.NewFilesystem())
	defer fs.Close()
	fk := msg.BaseFilesPath() + "/" + msg.GetString("raw")
	r := try.To1(fs.GetReader(fk))
	defer r.Close()
	from := msg.GetString("from")
	try.To(msend(from, to, r))
	out.Set("delivered", true)
	try.To(oc.app.Save(out))
	return nil
}

func nativeSend(from string, to string, r io.Reader) error
