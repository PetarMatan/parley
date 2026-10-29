// Command aic-mint creates the mock signing key, its JWKS (kid aic-mock-1)
// and RS256 tokens for the mock issuer (contract section 3).
//
//	aic-mint --work $AIC_WORK --users a@example.com,b@example.com --ttl 720h
//	aic-mint --users a@example.com --expired     # writes tokens/<user>.expired.jwt
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/auth"
)

func main() {
	if err := run(os.Args[1:], time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "aic-mint:", err)
		os.Exit(1)
	}
}

func run(args []string, now time.Time) error {
	fs := flag.NewFlagSet("aic-mint", flag.ContinueOnError)
	defWork := os.Getenv("AIC_WORK")
	if defWork == "" {
		defWork = "work"
	}
	work := fs.String("work", defWork, "work dir (keys/ and tokens/ live here)")
	users := fs.String("users", "a@example.com,b@example.com,c@example.com", "comma-separated emails")
	ttl := fs.Duration("ttl", 24*time.Hour, "token lifetime")
	expired := fs.Bool("expired", false, "mint tokens that expired one hour ago, named <user>.expired.jwt")
	out := fs.String("out", "", "token directory (default <work>/tokens)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(*work, "tokens")
	}
	key, created, err := auth.LoadOrCreateKey(filepath.Join(*work, "keys", "signing.pem"))
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	jwksPath := filepath.Join(*work, "keys", "jwks.json")
	// The JWKS is rewritten only with a new key, so a running gateway keeps
	// a stable file.
	if _, statErr := os.Stat(jwksPath); created || statErr != nil {
		if err := auth.WriteJWKS(jwksPath, key, auth.MockKid); err != nil {
			return err
		}
		fmt.Println("wrote", jwksPath)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	iat, exp, suffix := now, now.Add(*ttl), ".jwt"
	if *expired {
		iat, exp, suffix = now.Add(-2*time.Hour), now.Add(-time.Hour), ".expired.jwt"
	}
	for _, u := range strings.Split(*users, ",") {
		u = strings.ToLower(strings.TrimSpace(u))
		if u == "" {
			continue
		}
		tok, err := auth.Sign(key, auth.MockKid, map[string]any{
			"iss": auth.MockIssuer, "aud": auth.MockAudience, "sub": u, "email": u,
			"name": strings.SplitN(u, "@", 2)[0], "iat": iat.Unix(), "exp": exp.Unix(),
		})
		if err != nil {
			return err
		}
		p := filepath.Join(*out, u+suffix)
		if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Printf("wrote %s (expires %s)\n", p, exp.UTC().Format(time.RFC3339))
	}
	return nil
}
