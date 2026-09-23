package caldav

import (
	"context"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/err0"
)

type ctxKey string

var ctxAuthKey = ctxKey("account")

func getAuth(ctx context.Context) *core.Record {
	r, _ := ctx.Value(ctxAuthKey).(*core.Record)
	return r
}

func getAuthTry(ctx context.Context, path string) *core.Record {
	r := getAuth(ctx)
	if r == nil {
		err0.Throw(ErrUnauthed)
	}
	if path == "" {
		return r
	}
	m, ok := userPath.Match(path)
	if !ok {
		err0.Throw(ErrPathMatchFailed)
	}
	if m.Params["account"] != r.Id {
		err0.Throw(ErrUnauthed)
	}
	return r
}

var ErrUnauthed = fmt.Errorf("unauthenticated requests are not supported")
