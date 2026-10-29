// Command aic-inbox is the agent inbox service (contract section 6). It stores
// and routes typed messages between two people's agents; code review is the
// first conversation kind.
//
// Environment:
//
//	AIC_INBOX_ADDR     listen address, default 127.0.0.1:7070
//	AIC_WORK           work dir, default ./work
//	AIC_ISSUERS        JSON array of trusted OIDC issuers, replaces the default:
//	                   [{"issuer":"…","audiences":["…"],
//	                     "jwks_file":"…" | "jwks_url":"…" (optional),
//	                     "identity_claim":"…" (optional, default "email")}]
//	                   With neither jwks_file nor jwks_url, the JWKS URL comes
//	                   from <issuer>/.well-known/openid-configuration (jwks_uri),
//	                   fetched on first use; its issuer must match.
//	                   Default: mock issuer (JWKS file) and the local OIDC
//	                   provider of the sandbox (DEX, JWKS URL)
//	AIC_MAX_ROUNDS     round cap, default 5
//	AIC_SERVICE_USERS  comma list allowed to post notes on any MR, default promoter@aic.local
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PetarMatan/parley/inbox/internal/api"
	"github.com/PetarMatan/parley/inbox/internal/auth"
	"github.com/PetarMatan/parley/inbox/internal/logx"
	"github.com/PetarMatan/parley/inbox/internal/mr"
	"github.com/PetarMatan/parley/inbox/internal/store"
)

func main() {
	if err := run(); err != nil {
		logx.New(os.Stderr).Log(logx.Fatal, logx.Fields{"event": "startup_failed", "detail": err.Error()})
		os.Exit(1)
	}
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func run() error {
	work, err := filepath.Abs(env("AIC_WORK", "work"))
	if err != nil {
		return err
	}
	addr := env("AIC_INBOX_ADDR", "127.0.0.1:7070")
	maxRounds, err := strconv.Atoi(env("AIC_MAX_ROUNDS", "5"))
	if err != nil || maxRounds < 1 {
		return fmt.Errorf("AIC_MAX_ROUNDS must be a positive integer")
	}
	issuers := auth.DefaultIssuers(work)
	if raw := os.Getenv("AIC_ISSUERS"); raw != "" {
		issuers = nil
		if err := json.Unmarshal([]byte(raw), &issuers); err != nil {
			return fmt.Errorf("AIC_ISSUERS: %w", err)
		}
	}
	var service []string
	for _, u := range strings.Split(env("AIC_SERVICE_USERS", "promoter@aic.local"), ",") {
		if u = strings.TrimSpace(strings.ToLower(u)); u != "" {
			service = append(service, u)
		}
	}

	if err := os.MkdirAll(filepath.Join(work, "logs"), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(work, "logs", "inbox.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log := logx.New(os.Stdout, logFile)

	ver, err := auth.NewVerifier(issuers)
	if err != nil {
		return err
	}
	ver.OnDiscoveryError = func(issuer string, err error) {
		log.Warn(logx.Fields{"event": "oidc_discovery_failed", "issuer": issuer, "detail": err.Error()})
	}
	st, err := store.Open(filepath.Join(work, "inbox", "store.json"))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	srv, err := api.New(api.Config{MaxRounds: maxRounds, ServiceSenders: service}, st, mr.NewLocal(work), ver, log)
	if err != nil {
		return err
	}

	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams stay open.
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	iss := make([]string, len(issuers))
	for i, c := range issuers {
		iss[i] = c.Issuer
	}
	log.Info(logx.Fields{"event": "listening", "addr": addr, "work": work, "max_rounds": maxRounds, "issuers": iss})
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info(logx.Fields{"event": "stopped"})
	return nil
}
