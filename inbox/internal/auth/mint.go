package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOrCreateKey reads a PEM RSA private key, or creates a 2048-bit one with
// mode 0600 when the file is missing.
func LoadOrCreateKey(path string) (*rsa.PrivateKey, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		k, err := ParsePrivateKey(data)
		return k, false, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return k, true, os.WriteFile(path, out, 0o600)
}

// ParsePrivateKey accepts PKCS#8 and PKCS#1 PEM.
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	b, _ := pem.Decode(data)
	if b == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

// WriteJWKS writes the public half of key as a one-key JWKS.
func WriteJWKS(path string, key *rsa.PrivateKey, kid string) error {
	b, err := json.MarshalIndent(JWKS{Keys: []JWK{PublicJWK(&key.PublicKey, kid)}}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Sign returns an RS256 compact JWS of claims.
func Sign(key *rsa.PrivateKey, kid string, claims any) (string, error) {
	hdr, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
