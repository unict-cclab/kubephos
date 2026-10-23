package main

import (
	"strings"
	"testing"
)

func TestReadAdministrator(t *testing.T) {
	account, err := readAdministrator(strings.NewReader(`{"email":"admin@example.test","username":" Admin ","password":"a-long-generated-password"}`))
	if err != nil || account.Username != "admin" {
		t.Fatalf("unexpected account: %#v %v", account, err)
	}
	for _, input := range []string{`{}`, `{"email":"admin@example.test","username":"ad","password":"a-long-password"}`, `{"email":"admin@example.test","username":"admin","password":"short"}`, `{"email":"admin@example.test","username":"admin","password":"a-long-password","role":"admin"}`, `{"email":"admin@example.test","username":"admin","password":"a-long-password"} {}`, `not-json`} {
		if _, err := readAdministrator(strings.NewReader(input)); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
