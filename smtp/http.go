package smtp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"remoon.net/lifemail/db"
)

func Bind(app core.App) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Router.POST("/api/smtp", PostMail)
		return e.Next()
	})
}

func PostMail(e *core.RequestEvent) (err error) {
	app := e.App
	defer err0.Then(&err, nil, nil)

	key := os.Getenv("HTTP_SMTP_KEY")
	if key == "" {
		return apis.NewApiError(http.StatusPreconditionFailed, "服务器尚未开放 HTTP_SMTP 提交功能", nil)
	}

	h := e.Request.Header
	from := h.Get("X-SMTP-FROM")
	to := h.Get("X-SMTP-TO")
	datetimeStr := h.Get("X-SMTP-Datetime")

	d, err := time.Parse(time.RFC3339, datetimeStr)
	if err != nil {
		return apis.NewBadRequestError("时间解析出错", nil)
	}
	n := time.Now()
	if n.Sub(d) > 60*time.Second {
		return apis.NewBadRequestError("签名已过期", nil)
	}

	sign := h.Get("X-SMTP-Sign")
	sign2 := Sign(key, from+to+datetimeStr)
	if sign != sign2 {
		return apis.NewBadRequestError("签名有误", nil)
	}

	acc, err := GetAcc(app, to)
	if err != nil {
		return apis.NewNotFoundError("用户不存在", err)
	}

	buf := try.To1(io.ReadAll(e.Request.Body))
	extra := map[string]any{
		"from":  from,
		"inbox": []string{acc},
	}
	msg := try.To1(SaveMsg(app, buf, extra))

	mails := try.To1(app.FindCachedCollectionByNameOrId(db.TableMails))
	mailbox, _ := try.To2(GetMailboxOrCreate(app, acc, INBOX, nil))
	mail := core.NewRecord(mails)
	mail.Load(map[string]any{
		"to":      acc,
		"msg":     msg.Id,
		"mailbox": mailbox.Id,
		"uid":     0,
	})
	err = app.RunInTransaction(func(tx core.App) error {
		return SaveMail(tx, mail)
	})
	try.To(err)

	return e.NoContent(http.StatusNoContent)
}

func Sign(key string, msg string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	sign2 := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return sign2
}
