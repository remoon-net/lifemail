package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func TestParseEmailFlag(t *testing.T) {
	cases := []struct {
		name string
		ad   *wbxml.Node
		want bool
	}{
		{
			"flag set",
			wbxml.N(pageAirSync, "ApplicationData",
				wbxml.N(pageEmail, "Flag", wbxml.T(pageEmail, "FlagStatus", "2"))),
			true,
		},
		{
			"flag cleared",
			wbxml.N(pageAirSync, "ApplicationData",
				wbxml.N(pageEmail, "Flag", wbxml.T(pageEmail, "FlagStatus", "0"))),
			false,
		},
		{
			"legacy flag status",
			wbxml.N(pageAirSync, "ApplicationData",
				wbxml.T(pageEmail, "FlagStatus", "1")),
			true,
		},
	}
	for _, tc := range cases {
		m := &store.Message{IsFlagged: !tc.want}
		parseEmailAppData(tc.ad, m)
		if m.IsFlagged != tc.want {
			t.Errorf("%s: IsFlagged = %v, want %v", tc.name, m.IsFlagged, tc.want)
		}
	}
}

func TestEmailAppDataIncludesFlag(t *testing.T) {
	ad := emailAppData(&store.Message{IsFlagged: true}, nil, bodyPref{})
	flag := ad.Child("Flag")
	if flag == nil {
		t.Fatal("Flag element missing")
	}
	if got := flag.ChildText("FlagStatus"); got != "2" {
		t.Fatalf("FlagStatus = %q, want 2", got)
	}
	if got := flag.ChildText("FlagType"); got != "FollowUp" {
		t.Fatalf("FlagType = %q", got)
	}

	clear := emailAppData(&store.Message{}, nil, bodyPref{})
	if got := clear.Child("Flag").ChildText("FlagStatus"); got != "0" {
		t.Fatalf("cleared FlagStatus = %q, want 0", got)
	}
}
