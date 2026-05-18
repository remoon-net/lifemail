package smtp

import "testing"

func TestSign(t *testing.T) {
	z := Sign("666", "777")
	t.Log(z)
}
