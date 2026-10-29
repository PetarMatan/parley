package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/auth"
)

func TestMint(t *testing.T) {
	work := t.TempDir()
	now := time.Now()
	if err := run([]string{"--work", work, "--users", "a@example.com, B@example.com", "--ttl", "1h"}, now); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--work", work, "--users", "a@example.com", "--expired"}, now); err != nil {
		t.Fatal(err)
	}
	jwks1, _ := os.ReadFile(filepath.Join(work, "keys", "jwks.json"))
	// A second run keeps the key and the JWKS.
	if err := run([]string{"--work", work, "--users", "c@example.com"}, now); err != nil {
		t.Fatal(err)
	}
	jwks2, _ := os.ReadFile(filepath.Join(work, "keys", "jwks.json"))
	if string(jwks1) != string(jwks2) || !strings.Contains(string(jwks1), auth.MockKid) {
		t.Fatal("JWKS changed or lacks the mock kid")
	}
	v, err := auth.NewVerifier(auth.DefaultIssuers(work)[:1])
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(work, "tokens", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	for _, u := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		id, err := v.Verify(read(u + ".jwt"))
		if err != nil || id.Email != u {
			t.Errorf("%s: %+v %v", u, id, err)
		}
	}
	if _, err := v.Verify(read("a@example.com.expired.jwt")); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired token: %v", err)
	}
}
