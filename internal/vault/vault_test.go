package vault

import (
	"strings"
	"testing"
)

const key = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestSealOpen(t *testing.T) {
	v, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, n := v.Seal([]byte(`{"access_token":"t"}`), 7)
	if strings.Contains(string(ct), "access_token") {
		t.Fatal("not encrypted")
	}
	if pt, err := v.Open(ct, n, 7); err != nil || string(pt) != `{"access_token":"t"}` {
		t.Fatalf("%s %v", pt, err)
	}
	if _, err := v.Open(ct, n, 8); err == nil {
		t.Fatal("ciphertext must be bound to its account")
	}
	if !v.Verify("a", v.Sign("a")) || v.Verify("b", v.Sign("a")) {
		t.Fatal("sign/verify")
	}
	if _, err := New("short"); err == nil {
		t.Fatal("short key must fail")
	}
}
