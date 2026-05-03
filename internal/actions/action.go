// Package actions implements the lifecycle action runner: null and command
// types, the slot-context env var assembler, the clean-env shell exec, and
// the readiness probe loop.
package actions

import (
	"context"
	"time"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// Action is the unit invoked at a lifecycle hook (before_create,
// post_replace, etc.). Two implementations: Null (a typed no-op, makes the
// caller branch-free) and Command (the only side-effecting variant in v1).
type Action interface {
	Run(ctx context.Context, sc slotctx.SlotContext) Result
}

// Result is what every Action returns. ExitCode is meaningful only for
// Command actions; for Null it's always zero. Stdout/Stderr hold up to 4KB
// of trailing output so failure diagnostics include real terminal context.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
	Err      error
}

// Null is the explicit no-op action - structurally distinct from "no
// action configured" so the schema can express both.
type Null struct{}

// Run implements Action. A null action always succeeds.
func (Null) Run(_ context.Context, _ slotctx.SlotContext) Result { return Result{} }

// Command is the shell-exec action.
type Command struct {
	// Command is passed to /bin/sh -c verbatim. There is no template
	// substitution: dynamic data flows in via env vars, not by string
	// interpolation, eliminating injection risk.
	Command string
	// Env is the operator-supplied env map. It is merged with the
	// HCLOUDGROUP_* namespace before exec; both together form the full
	// environment seen by the child process. The runner's own env is
	// not inherited.
	Env map[string]string
	// Stdin is fed to the child process verbatim; empty means no stdin.
	Stdin string
	// WorkingDir is the cwd of the child. Empty defaults to a per-call
	// ephemeral tempdir.
	WorkingDir string
	// ExpectedExit is the set of exit codes that count as success.
	// Empty defaults to {0}.
	ExpectedExit []int
	// Timeout is the per-attempt deadline.
	Timeout time.Duration
}
