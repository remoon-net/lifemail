package eas

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestCheckPasswordCache(t *testing.T) {
	srv, user, _ := testServer(t)

	if !srv.checkPassword(user, "secret") {
		t.Fatal("correct password rejected")
	}
	if !srv.checkPassword(user, "secret") {
		t.Fatal("cached password rejected")
	}
	if srv.checkPassword(user, "wrong") {
		t.Fatal("wrong password accepted")
	}

	// A password reset must invalidate the cached entry immediately.
	hash, err := bcrypt.GenerateFromPassword([]byte("newpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetPassword(user.Name, string(hash)); err != nil {
		t.Fatal(err)
	}
	updated, err := srv.Store.UserByName(user.Name)
	if err != nil {
		t.Fatal(err)
	}
	if srv.checkPassword(updated, "secret") {
		t.Fatal("old password still accepted after reset")
	}
	if !srv.checkPassword(updated, "newpass") {
		t.Fatal("new password rejected after reset")
	}
}
