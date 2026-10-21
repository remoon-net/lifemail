package smtp

import "testing"

func TestEnvelopeFromMultiDomain(t *testing.T) {
	cfg := Config{
		Host:     "smtp.example.com",
		Password: "secret",
		Domains:  []string{"example.com", "example.org"},
		From:     "noreply@example.com",
	}
	s := &Sender{Provider: func() Config { return cfg }}
	cases := map[string]string{
		"alice@example.com": "alice@example.com",
		"bob@example.org":   "bob@example.org",
		"carol@other.test":  "noreply@example.com",
		"":                  "noreply@example.com",
	}
	for input, want := range cases {
		if got := s.EnvelopeFrom(input); got != want {
			t.Errorf("EnvelopeFrom(%q) = %q, want %q", input, got, want)
		}
	}

	// With no configured domains, any user address is accepted.
	open := &Sender{Provider: func() Config {
		return Config{Host: "smtp.example.com", From: "noreply@example.com"}
	}}
	if got := open.EnvelopeFrom("x@other.test"); got != "x@other.test" {
		t.Errorf("EnvelopeFrom on open sender = %q", got)
	}
}

func TestEnabledFollowsProvider(t *testing.T) {
	cfg := Config{}
	s := &Sender{Provider: func() Config { return cfg }}
	if s.Enabled() {
		t.Fatal("sender should be disabled without a password")
	}
	cfg.Host = "smtp.example.com"
	cfg.Password = "secret"
	if !s.Enabled() {
		t.Fatal("sender should pick up the updated configuration")
	}
}
