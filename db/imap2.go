package db

import (
	"github.com/emersion/go-imap/v2"
)

var mailFlagMap = map[imap.Flag]MailFlag{}
var mailboxAttrMap = map[imap.MailboxAttr]MailboxAttr{}

func init() {
	for k, v := range MailFlagMap {
		mailFlagMap[v] = k
	}
	for k, v := range MailboxAttrMap {
		mailboxAttrMap[v] = k
	}
}

func ToMailboxAttr(k imap.MailboxAttr) MailboxAttr {
	v, ok := mailboxAttrMap[k]
	if ok {
		return v
	}
	return 0
}

func ToMailFlag(k imap.Flag) MailFlag {
	v, ok := mailFlagMap[k]
	if ok {
		return v
	}
	return 0
}
