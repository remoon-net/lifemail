package authlimit

import (
	"testing"
	"time"
)

func TestLimiterLocksAfterBudget(t *testing.T) {
	now := time.Unix(1700000000, 0)
	l := New(3, 10*time.Minute, 5*time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if l.Fail("key") {
			t.Fatalf("locked after %d failures, want 3", i+1)
		}
		if locked, _ := l.Locked("key"); locked {
			t.Fatalf("locked after %d failures", i+1)
		}
	}
	if !l.Fail("key") {
		t.Fatal("third failure must lock the key")
	}
	locked, retry := l.Locked("key")
	if !locked {
		t.Fatal("key should be locked")
	}
	if retry != 5*time.Minute {
		t.Fatalf("retry = %v, want 5m", retry)
	}
	if locked, _ := l.Locked("other"); locked {
		t.Fatal("unrelated key must not be locked")
	}

	// The lock expires and the budget starts over.
	now = now.Add(5 * time.Minute)
	if locked, _ := l.Locked("key"); locked {
		t.Fatal("lock should have expired")
	}
	if l.Fail("key") {
		t.Fatal("single failure after expiry must not lock")
	}
}

func TestLimiterWindowResets(t *testing.T) {
	now := time.Unix(1700000000, 0)
	l := New(2, time.Minute, time.Minute)
	l.now = func() time.Time { return now }

	l.Fail("key")
	now = now.Add(2 * time.Minute)
	if l.Fail("key") {
		t.Fatal("failures outside the window must not accumulate")
	}
}

func TestLimiterReset(t *testing.T) {
	l := New(1, time.Minute, time.Minute)
	if !l.Fail("key") {
		t.Fatal("expected lock")
	}
	l.Reset("key")
	if locked, _ := l.Locked("key"); locked {
		t.Fatal("reset must clear the lock")
	}
}

func TestNilLimiterIsNoop(t *testing.T) {
	var l *Limiter
	if locked, _ := l.Locked("key"); locked {
		t.Fatal("nil limiter must not lock")
	}
	if l.Fail("key") {
		t.Fatal("nil limiter must not lock")
	}
	l.Reset("key")
}
