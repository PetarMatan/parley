// Command aic-promote pushes a quarantine worktree's HEAD to the merge request
// branch after the fixed checks of contract section 10.
//
//	aic-promote --worktree <path> --mr <iid> --task <task_id> [--work $AIC_WORK]
//
// It prints the JSON result on stdout and exits 0 when it pushed, 1 when it
// refused, 2 on a usage error.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PetarMatan/parley/inbox/internal/promote"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("aic-promote", flag.ContinueOnError)
	worktree := fs.String("worktree", "", "quarantine worktree path")
	mr := fs.Int("mr", 0, "merge request iid")
	task := fs.String("task", "", "task id (f-<8 hex>)")
	work := fs.String("work", os.Getenv("AIC_WORK"), "work dir (default $AIC_WORK)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *worktree == "" || *mr <= 0 || *task == "" || *work == "" {
		fmt.Fprintln(os.Stderr, "usage: aic-promote --worktree <path> --mr <iid> --task <task_id> [--work $AIC_WORK]")
		return 2
	}
	wt, err1 := filepath.Abs(*worktree)
	wd, err2 := filepath.Abs(*work)
	if err1 != nil || err2 != nil {
		fmt.Fprintln(os.Stderr, "aic-promote: bad path")
		return 2
	}

	// Inbox address and token. The sandbox has a minted service token under
	// the work dir. Otherwise the promoter acts as the person who runs the
	// quarantine: base_url and token_file come from the worktree's
	// .aic/identity.json (the quarantine copies it from the checkout).
	// AIC_BASE_URL and AIC_PROMOTER_TOKEN_FILE override both.
	base := os.Getenv("AIC_BASE_URL")
	tokenFile := os.Getenv("AIC_PROMOTER_TOKEN_FILE")
	serviceToken := filepath.Join(wd, "tokens", "promoter@aic.local.jwt")
	if tokenFile == "" {
		if _, err := os.Stat(serviceToken); err == nil {
			tokenFile = serviceToken
		}
	}
	if base == "" || tokenFile == "" {
		if id, err := readIdentity(filepath.Join(wt, ".aic", "identity.json")); err == nil {
			if base == "" {
				base = id.BaseURL
			}
			if tokenFile == "" && id.TokenFile != "" {
				tokenFile = id.TokenFile
				if !filepath.IsAbs(tokenFile) {
					tokenFile = filepath.Join(wt, tokenFile)
				}
			}
		}
	}
	if base == "" {
		base = "http://127.0.0.1:7070"
	}
	if tokenFile == "" {
		tokenFile = serviceToken
	}
	res := promote.Run(context.Background(), promote.Options{
		Worktree: wt,
		Work:     wd,
		MR:       *mr,
		TaskID:   *task,
		Inbox:    promote.NewHTTPInbox(base, tokenFile),
		Sandbox:  true,
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res)
	if !res.OK {
		return 1
	}
	return 0
}

type identityFile struct {
	BaseURL   string `json:"base_url"`
	TokenFile string `json:"token_file"`
}

func readIdentity(path string) (identityFile, error) {
	var id identityFile
	b, err := os.ReadFile(path)
	if err != nil {
		return id, err
	}
	err = json.Unmarshal(b, &id)
	return id, err
}
