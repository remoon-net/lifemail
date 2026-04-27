package imap

import (
	"os"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/pocketbase/pocketbase/core"
)

func New(app core.App) *imapserver.Server {
	opts := &imapserver.Options{
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
	}
	if app.IsDev() {
		opts.DebugWriter = os.Stderr
	}
	srv := imapserver.New(opts)
	return srv
}
