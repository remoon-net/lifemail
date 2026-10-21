package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestUserByLogin(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "login.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	// bob has no address at creation; one is assigned afterwards.
	bob, err := db.CreateUser("bob", "", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetPrimaryAddress(bob.ID, "bob@other.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("info@example.com", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("alice@other.test", alice.ID); err != nil {
		t.Fatal(err)
	}

	for _, login := range []string{
		"alice",                 // bare name
		"ALICE",                 // case insensitive
		"alice@example.com",     // primary address
		"alice@other.test",      // alias address
		"info@example.com",      // alias address
		"alice+tag@example.com", // plus addressing
	} {
		user, err := db.UserByLogin(login)
		if err != nil {
			t.Fatalf("%q: %v", login, err)
		}
		if user.ID != alice.ID {
			t.Fatalf("%q resolved to %s", login, user.Name)
		}
		if user.Address != "alice@example.com" {
			t.Fatalf("%q carried address %q, want the primary one", login, user.Address)
		}
	}

	// Accounts without a domain in their identity still resolve by name.
	if user, err := db.UserByLogin("bob"); err != nil {
		t.Fatalf("bob: %v", err)
	} else if user.ID != bob.ID || user.Address != "bob@other.test" {
		t.Fatalf("bob = %+v", user)
	}

	// Addresses lookups are not tied to a domain either.
	if _, err := db.UserByLogin("bob@other.test"); err != nil {
		t.Fatalf("bob address: %v", err)
	}

	if _, err := db.UserByLogin("nobody"); err == nil {
		t.Fatal("unknown login must fail")
	}
	if _, err := db.UserByLogin(""); err == nil {
		t.Fatal("empty login must fail")
	}
}

func TestSetPrimaryAddressMovesBetweenAccounts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "primary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser("bob", "", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}

	// An address owned by another account cannot be taken over.
	if _, err := db.SetPrimaryAddress(bob.ID, "alice@example.com"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("take over error = %v, want ErrDuplicate", err)
	}

	// Switching the primary address replaces the previous one.
	if _, err := db.SetPrimaryAddress(alice.ID, "alice@example.org"); err != nil {
		t.Fatal(err)
	}
	user, err := db.UserByID(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.Address != "alice@example.org" {
		t.Fatalf("primary address = %q", user.Address)
	}
	aliases, err := db.AliasesByUser(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 2 || !aliases[0].IsPrimary || aliases[0].Address != "alice@example.org" {
		t.Fatalf("aliases = %+v", aliases)
	}

	// The previous primary address stays as a regular alias.
	if user, err := db.UserByLogin("alice@example.com"); err != nil {
		t.Fatalf("previous address: %v", err)
	} else if user.ID != alice.ID || user.Address != "alice@example.org" {
		t.Fatalf("previous address resolved to %+v", user)
	}
}

func TestDeletePrimaryAddressRefused(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	alice, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("info@example.com", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAlias("alice@example.com"); !errors.Is(err, ErrPrimaryAddress) {
		t.Fatalf("delete primary = %v, want ErrPrimaryAddress", err)
	}
	if err := db.DeleteAlias("info@example.com"); err != nil {
		t.Fatalf("delete alias: %v", err)
	}
}

func TestCreateUserRejectsDomainInName(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "reject.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.CreateUser("alice@example.com", "", "Alice", "hash"); err == nil {
		t.Fatal("account names must not carry a domain")
	}
}
