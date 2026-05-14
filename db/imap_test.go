package db

import "testing"

func TestFlags(t *testing.T) {
	v := AllMailFlags.Flags()
	t.Log(v)
}
