package smtp

import (
	"net"
	"testing"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/shynome/err0/try"
)

func TestSMTP(t *testing.T) {
	conn := try.To1(net.Dial("tcp", "127.0.0.1:25"))
	c := smtp.NewClient(conn)
	defer c.Close()
	try.To(c.Hello("localhost"))
	c.SupportsAuth("")
	try.To(c.Auth(sasl.NewExternalClient("wwww")))
	t.Log(c)
}
