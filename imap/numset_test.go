package imap

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestSeqSet(t *testing.T) {
	x := imap.SeqSetNum()
	x.AddRange(0, 0)
	s := x.Dynamic()
	t.Log(s)
	const n = 0
	b := n != 0 && x.Contains(n)
	t.Log(b)
}

func TestUIDSet(t *testing.T) {
	x := imap.UIDSetNum()
	x.AddNum(1, 2, 3, 4)
	s := x.String()
	t.Log(s)
	b := x.Contains(4)
	t.Log(b)
}
