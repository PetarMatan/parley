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

	// Task brief: AIC_BASE_URL, default direct mode (contract section 2).
	base := os.Getenv("AIC_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:7070"
	}
	res := promote.Run(context.Background(), promote.Options{
		Worktree: wt,
		Work:     wd,
		MR:       *mr,
		TaskID:   *task,
		Inbox:    promote.NewHTTPInbox(base, filepath.Join(wd, "tokens", "promoter@aic.local.jwt")),
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
