// Package auth verifies RS256 JWTs against configured issuers and mints mock
// tokens. Standard library only.
package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Contract section 3 values. The LocalOIDC* values describe the sandbox's
// local OIDC provider (DEX in docker compose); any other OIDC issuer is
// configured through AIC_ISSUERS.
const (
	MockIssuer        = "https://aic.local/mock"
	MockAudience      = "aic-inbox"
	MockKid           = "aic-mock-1"
	LocalOIDCIssuer   = "http://localhost:5556/dex"
	LocalOIDCAudience = "aic-cli"
	LocalOIDCJWKSURL  = "http://localhost:5556/dex/keys"

	// DefaultIdentityClaim is the claim that names the caller when an issuer
	// sets no identity_claim.
	DefaultIdentityClaim = "email"

	// Skew is the clock tolerance on exp and nbf.
	Skew = 60 * time.Second
)

// IssuerConfig names one trusted issuer. At most one of JWKSFile and JWKSURL
// is set. When neither is set, the JWKS URL comes from the issuer's OIDC
// discovery document (<issuer>/.well-known/openid-configuration, jwks_uri).
// IdentityClaim names the claim that identifies the caller; empty means
// DefaultIdentityClaim.
type IssuerConfig struct {
	Issuer        string   `json:"issuer"`
	Audiences     []string `json:"audiences"`
	JWKSFile      string   `json:"jwks_file,omitempty"`
	JWKSURL       string   `json:"jwks_url,omitempty"`
	IdentityClaim string   `json:"identity_claim,omitempty"`
}

// DefaultIssuers is the mock issuer (JWKS file under work) and the local OIDC
// provider of the sandbox.
func DefaultIssuers(work string) []IssuerConfig {
	return []IssuerConfig{
		{Issuer: MockIssuer, Audiences: []string{MockAudience}, JWKSFile: work + "/keys/jwks.json"},
		{Issuer: LocalOIDCIssuer, Audiences: []string{LocalOIDCAudience}, JWKSURL: LocalOIDCJWKSURL},
	}
}

// Identity is the verified caller. Email holds the value of the issuer's
// configured identity claim (default "email"), lower-cased. The field keeps
// its name so callers do not change.
type Identity struct {
	Email   string
	Subject string
	Issuer  string
}

// Claims are the registered and contract claims the inbox reads.
type Claims struct {
	Iss   string   `json:"iss"`
	Sub   string   `json:"sub"`
	Aud   audience `json:"aud"`
	Exp   *int64   `json:"exp"`
	Nbf   *int64   `json:"nbf,omitempty"`
	Iat   int64    `json:"iat,omitempty"`
	Email string   `json:"email"`
	Name  string   `json:"name,omitempty"`
}

// audience accepts the string or array form of aud.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("aud must be a string or an array of strings")
	}
	*a = many
	return nil
}

func (a audience) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}

type issuer struct {
	cfg  IssuerConfig
	keys *keySource
}

// Verifier checks bearer tokens. Safe for concurrent use.
type Verifier struct {
	issuers map[string]*issuer
	Now     func() time.Time
	// OnDiscoveryError, when set, is called each time an OIDC discovery fetch
	// fails, so the caller can log it. Set it before the first Verify.
	OnDiscoveryError func(issuer string, err error)
}

// urlReloadGap rate-limits network reloads (JWKS URL and discovery), so
// unknown kids or an outage cannot flood the provider.
const urlReloadGap = 10 * time.Second

// NewVerifier builds a verifier. Keys (and discovery) load on first use, so an
// unreachable provider only fails that provider's tokens.
func NewVerifier(cfgs []IssuerConfig) (*Verifier, error) {
	v := &Verifier{issuers: map[string]*issuer{}, Now: time.Now}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, c := range cfgs {
		if c.Issuer == "" || len(c.Audiences) == 0 {
			return nil, fmt.Errorf("issuer %q: issuer and audiences are required", c.Issuer)
		}
		if c.IdentityClaim == "" {
			c.IdentityClaim = DefaultIdentityClaim
		}
		src := &keySource{now: func() time.Time { return v.Now() }}
		switch {
		case c.JWKSFile != "" && c.JWKSURL != "":
			return nil, fmt.Errorf("issuer %q: set at most one of jwks_file and jwks_url", c.Issuer)
		case c.JWKSFile != "":
			src.load = fileLoader(c.JWKSFile)
		case c.JWKSURL != "":
			src.load = urlLoader(c.JWKSURL, client)
			src.minGap = urlReloadGap
		default:
			src.load = discoveryLoader(c.Issuer, client, func(err error) {
				if v.OnDiscoveryError != nil {
					v.OnDiscoveryError(c.Issuer, err)
				}
			})
			src.minGap = urlReloadGap
		}
		v.issuers[c.Issuer] = &issuer{cfg: c, keys: src}
	}
	return v, nil
}

// Verify checks signature, iss, aud, exp and nbf and returns the identity.
// The error text is safe to log; it never contains the token.
func (v *Verifier) Verify(token string) (Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, errors.New("token is not a JWS compact serialization")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &hdr); err != nil {
		return Identity{}, fmt.Errorf("header: %w", err)
	}
	if hdr.Alg != "RS256" {
		return Identity{}, fmt.Errorf("alg %q not allowed", hdr.Alg)
	}
	var c Claims
	if err := decodePart(parts[1], &c); err != nil {
		return Identity{}, fmt.Errorf("claims: %w", err)
	}
	// iss is read before the signature check only to pick the key set; a
	// forged iss cannot pass the signature of that issuer's keys.
	iss, ok := v.issuers[c.Iss]
	if !ok {
		return Identity{}, fmt.Errorf("issuer %q not trusted", c.Iss)
	}
	pub, err := iss.keys.key(hdr.Kid)
	if err != nil {
		return Identity{}, fmt.Errorf("key %q of %s: %w", hdr.Kid, c.Iss, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, errors.New("signature is not base64url")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return Identity{}, errors.New("bad signature")
	}
	if !slices.ContainsFunc(c.Aud, func(a string) bool { return slices.Contains(iss.cfg.Audiences, a) }) {
		return Identity{}, fmt.Errorf("audience %v not accepted", []string(c.Aud))
	}
	now := v.Now()
	if c.Exp == nil {
		return Identity{}, errors.New("exp missing")
	}
	if now.After(time.Unix(*c.Exp, 0).Add(Skew)) {
		return Identity{}, errors.New("token expired")
	}
	if c.Nbf != nil && now.Add(Skew).Before(time.Unix(*c.Nbf, 0)) {
		return Identity{}, errors.New("token not yet valid")
	}
	name := iss.cfg.IdentityClaim
	var all map[string]any
	if err := decodePart(parts[1], &all); err != nil {
		return Identity{}, fmt.Errorf("claims: %w", err)
	}
	who, _ := all[name].(string)
	if strings.TrimSpace(who) == "" {
		return Identity{}, fmt.Errorf("identity claim %q missing", name)
	}
	return Identity{Email: strings.ToLower(who), Subject: c.Sub, Issuer: c.Iss}, nil
}

func decodePart(s string, dst any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return errors.New("not base64url")
	}
	return json.Unmarshal(b, dst)
}
