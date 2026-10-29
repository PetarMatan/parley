package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func claims(iss, aud, email string, exp time.Time) Claims {
	e := exp.Unix()
	return Claims{Iss: iss, Sub: email, Aud: audience{aud}, Exp: &e, Iat: now.Unix(), Email: email}
}

func mockSetup(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	dir := t.TempDir()
	key, created, err := LoadOrCreateKey(filepath.Join(dir, "keys", "signing.pem"))
	if err != nil || !created {
		t.Fatalf("create key: %v %v", created, err)
	}
	if err := WriteJWKS(filepath.Join(dir, "keys", "jwks.json"), key, MockKid); err != nil {
		t.Fatal(err)
	}
	again, created, err := LoadOrCreateKey(filepath.Join(dir, "keys", "signing.pem"))
	if err != nil || created || again.N.Cmp(key.N) != 0 {
		t.Fatalf("reload key: %v %v", created, err)
	}
	return key, dir
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, c any) string {
	t.Helper()
	tok, err := Sign(key, kid, c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestVerifyMock(t *testing.T) {
	key, dir := mockSetup(t)
	// The local OIDC provider points at a closed port: it must not affect mock tokens.
	v, err := NewVerifier([]IssuerConfig{
		{Issuer: MockIssuer, Audiences: []string{MockAudience}, JWKSFile: filepath.Join(dir, "keys", "jwks.json")},
		{Issuer: LocalOIDCIssuer, Audiences: []string{LocalOIDCAudience}, JWKSURL: "http://127.0.0.1:1/dex/keys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v.Now = func() time.Time { return now }
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	future := now.Add(10 * time.Minute).Unix()

	ok := claims(MockIssuer, MockAudience, "A@example.com", now.Add(time.Hour))
	id, err := v.Verify(sign(t, key, MockKid, ok))
	if err != nil || id.Email != "a@example.com" || id.Issuer != MockIssuer {
		t.Fatalf("valid token: %+v %v", id, err)
	}
	if _, err := v.Verify(sign(t, key, MockKid, claims(MockIssuer, MockAudience, "a@example.com", now.Add(-30*time.Second)))); err != nil {
		t.Fatalf("expired inside skew must pass: %v", err)
	}

	nbf := ok
	nbf.Nbf = &future
	noEmail := ok
	noEmail.Email = ""
	noExp := ok
	noExp.Exp = nil
	bad := map[string]string{
		"expired":     sign(t, key, MockKid, claims(MockIssuer, MockAudience, "a@example.com", now.Add(-2*time.Minute))),
		"nbf future":  sign(t, key, MockKid, nbf),
		"wrong aud":   sign(t, key, MockKid, claims(MockIssuer, "other", "a@example.com", now.Add(time.Hour))),
		"unknown iss": sign(t, key, MockKid, claims("https://evil", MockAudience, "a@example.com", now.Add(time.Hour))),
		"other key":   sign(t, other, MockKid, ok),
		"unknown kid": sign(t, key, "nope", ok),
		"no email":    sign(t, key, MockKid, noEmail),
		"no exp":      sign(t, key, MockKid, noExp),
		"oidc down":   sign(t, key, "dex-1", claims(LocalOIDCIssuer, LocalOIDCAudience, "a@example.com", now.Add(time.Hour))),
		"garbage":     "a.b",
	}
	hs := strings.Split(sign(t, key, MockKid, ok), ".")
	bad["alg none"] = "eyJhbGciOiJub25lIn0." + hs[1] + "."
	for name, tok := range bad {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The mock path still works after the local provider failure.
	if _, err := v.Verify(sign(t, key, MockKid, ok)); err != nil {
		t.Fatalf("mock after local provider failure: %v", err)
	}
}

func TestVerifyJWKSURLRefetchOnUnknownKid(t *testing.T) {
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	var current atomic.Value
	current.Store(JWKS{Keys: []JWK{PublicJWK(&k1.PublicKey, "k1")}})
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		_ = json.NewEncoder(w).Encode(current.Load())
	}))
	defer srv.Close()

	v, err := NewVerifier([]IssuerConfig{{Issuer: LocalOIDCIssuer, Audiences: []string{"x", LocalOIDCAudience}, JWKSURL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	v.Now = func() time.Time { return clock }
	c := claims(LocalOIDCIssuer, LocalOIDCAudience, "b@example.com", now.Add(time.Hour))
	c.Aud = audience{"aic-cli", "something-else"}

	for i := 0; i < 3; i++ {
		if _, err := v.Verify(sign(t, k1, "k1", c)); err != nil {
			t.Fatalf("k1: %v", err)
		}
	}
	if fetches.Load() != 1 {
		t.Fatalf("keys must be cached, fetched %d times", fetches.Load())
	}
	// Rotation: an unknown kid triggers one refetch once the gap has passed.
	current.Store(JWKS{Keys: []JWK{PublicJWK(&k2.PublicKey, "k2")}})
	clock = clock.Add(11 * time.Second)
	if _, err := v.Verify(sign(t, k2, "k2", c)); err != nil {
		t.Fatalf("k2 after rotation: %v", err)
	}
	// An unknown kid inside the gap does not refetch.
	n := fetches.Load()
	_, _ = v.Verify(sign(t, k2, "k3", c))
	if fetches.Load() != n {
		t.Fatal("refetched inside the rate-limit gap")
	}
}

// discoveryServer serves a discovery document (with docIssuer, or its own URL
// when empty) and a JWKS holding pub. It counts discovery requests.
func discoveryServer(t *testing.T, docIssuer string, pub *rsa.PublicKey) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realm/.well-known/openid-configuration":
			hits.Add(1)
			iss := docIssuer
			if iss == "" {
				iss = srv.URL + "/realm"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss, "jwks_uri": srv.URL + "/realm/certs"})
		case "/realm/certs":
			_ = json.NewEncoder(w).Encode(JWKS{Keys: []JWK{PublicJWK(pub, "k1")}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestVerifyViaDiscovery(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv, hits := discoveryServer(t, "", &key.PublicKey)
	iss := srv.URL + "/realm"
	v, err := NewVerifier([]IssuerConfig{{Issuer: iss, Audiences: []string{"aic-cli"}}})
	if err != nil {
		t.Fatal(err)
	}
	v.Now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		id, err := v.Verify(sign(t, key, "k1", claims(iss, "aic-cli", "B@Example.com", now.Add(time.Hour))))
		if err != nil || id.Email != "b@example.com" || id.Issuer != iss {
			t.Fatalf("via discovery: %+v %v", id, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("discovery must be fetched once, got %d", hits.Load())
	}
}

func TestVerifyDiscoveryIssuerMismatch(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv, hits := discoveryServer(t, "https://someone-else.example/realm", &key.PublicKey)
	iss := srv.URL + "/realm"
	v, err := NewVerifier([]IssuerConfig{{Issuer: iss, Audiences: []string{"aic-cli"}}})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	v.Now = func() time.Time { return clock }
	var reported []error
	v.OnDiscoveryError = func(issuer string, err error) {
		if issuer != iss {
			t.Errorf("reported issuer %q", issuer)
		}
		reported = append(reported, err)
	}
	tok := sign(t, key, "k1", claims(iss, "aic-cli", "b@example.com", now.Add(time.Hour)))
	if _, err := v.Verify(tok); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch must fail: %v", err)
	}
	// Inside the gap: no new discovery request.
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("accepted inside the gap")
	}
	if hits.Load() != 1 || len(reported) != 1 {
		t.Fatalf("hits %d, reported %d", hits.Load(), len(reported))
	}
	// After the gap: retried, still failing.
	clock = clock.Add(11 * time.Second)
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("accepted after retry")
	}
	if hits.Load() != 2 || len(reported) != 2 {
		t.Fatalf("retry: hits %d, reported %d", hits.Load(), len(reported))
	}
}

func TestVerifyIdentityClaim(t *testing.T) {
	key, dir := mockSetup(t)
	v, err := NewVerifier([]IssuerConfig{{Issuer: MockIssuer, Audiences: []string{MockAudience},
		JWKSFile: filepath.Join(dir, "keys", "jwks.json"), IdentityClaim: "preferred_username"}})
	if err != nil {
		t.Fatal(err)
	}
	v.Now = func() time.Time { return now }
	exp := now.Add(time.Hour).Unix()
	base := map[string]any{"iss": MockIssuer, "sub": "s1", "aud": MockAudience, "exp": exp, "email": "other@example.com"}
	withName := map[string]any{"preferred_username": "B.User@Corp.Example"}
	for k, val := range base {
		withName[k] = val
	}
	id, err := v.Verify(sign(t, key, MockKid, withName))
	if err != nil || id.Email != "b.user@corp.example" {
		t.Fatalf("preferred_username: %+v %v", id, err)
	}
	// Missing (and non-string) identity claim is an error, even with email present.
	_, err = v.Verify(sign(t, key, MockKid, base))
	if err == nil || !strings.Contains(err.Error(), `identity claim "preferred_username" missing`) {
		t.Fatalf("missing claim: %v", err)
	}
	withName["preferred_username"] = 42
	if _, err := v.Verify(sign(t, key, MockKid, withName)); err == nil {
		t.Fatal("non-string identity claim accepted")
	}
}

func TestNewVerifierRejectsBothJWKSSources(t *testing.T) {
	_, err := NewVerifier([]IssuerConfig{{Issuer: "https://x", Audiences: []string{"a"}, JWKSFile: "f", JWKSURL: "u"}})
	if err == nil {
		t.Fatal("both jwks_file and jwks_url accepted")
	}
}
