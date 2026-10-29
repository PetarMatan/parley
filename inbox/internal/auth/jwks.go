package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// JWK is one RSA public key in a JSON Web Key Set.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// ParseJWKS returns the RSA keys of a JWKS document by kid. Non-RSA keys are
// skipped.
func ParseJWKS(data []byte) (map[string]*rsa.PublicKey, error) {
	var set JWKS
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			return nil, fmt.Errorf("jwks: bad RSA key %q", k.Kid)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, nil
}

// PublicJWK encodes an RSA public key as a JWK.
func PublicJWK(pub *rsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// keySource loads keys lazily and reloads them when a kid is unknown, so a
// rotated key works without a restart.
type keySource struct {
	mu      sync.Mutex
	load    func() ([]byte, error)
	keys    map[string]*rsa.PublicKey
	lastTry time.Time
	tried   bool
	minGap  time.Duration // reload at most this often, so unknown kids cannot flood the provider
	now     func() time.Time
}

var errUnknownKid = errors.New("unknown key id")

func (s *keySource) key(kid string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	if s.tried && s.now().Sub(s.lastTry) < s.minGap {
		return nil, errUnknownKid
	}
	s.tried, s.lastTry = true, s.now()
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	keys, err := ParseJWKS(data)
	if err != nil {
		return nil, err
	}
	s.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errUnknownKid
}

func fileLoader(path string) func() ([]byte, error) {
	return func() ([]byte, error) { return os.ReadFile(path) }
}

func urlLoader(url string, client *http.Client) func() ([]byte, error) {
	return func() ([]byte, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("jwks fetch: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jwks fetch: status %d", resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
}

// discoveryLoader resolves the JWKS URL from the issuer's OIDC discovery
// document on first use, then fetches the JWKS from it. The document's issuer
// must equal the configured one (trailing slashes ignored). A failed discovery
// is reported to onErr and retried on a later load; keySource rate-limits
// loads. Callers serialize loads (keySource holds its mutex).
func discoveryLoader(issuer string, client *http.Client, onErr func(error)) func() ([]byte, error) {
	var jwks func() ([]byte, error)
	return func() ([]byte, error) {
		if jwks == nil {
			u, err := discoverJWKSURL(issuer, client)
			if err != nil {
				onErr(err)
				return nil, err
			}
			jwks = urlLoader(u, client)
		}
		return jwks()
	}
}

func discoverJWKSURL(issuer string, client *http.Client) (string, error) {
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery: %s: status %d", url, resp.StatusCode)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", fmt.Errorf("oidc discovery: %s: %w", url, err)
	}
	if strings.TrimRight(doc.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return "", fmt.Errorf("oidc discovery: document issuer %q does not match configured issuer %q", doc.Issuer, issuer)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery: %s: jwks_uri missing", url)
	}
	return doc.JWKSURI, nil
}
