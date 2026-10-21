package mailparse

import "testing"

func TestDomain(t *testing.T) {
	cases := map[string]string{
		"alice@Example.COM":     "example.com",
		"alice+tag@mail.x.test": "mail.x.test",
		"not-an-address":        "",
		"trailing@":             "",
	}
	for in, want := range cases {
		if got := Domain(in); got != want {
			t.Errorf("Domain(%q) = %q, want %q", in, got, want)
		}
	}
}
