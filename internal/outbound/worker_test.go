package outbound

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := map[int]time.Duration{
		1:  time.Minute,
		2:  2 * time.Minute,
		3:  4 * time.Minute,
		6:  32 * time.Minute,
		7:  60 * time.Minute, // capped
		20: 60 * time.Minute,
	}
	for attempts, want := range cases {
		if got := Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
