package actions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// captureLimit is the per-stream byte cap for diagnostic output. Actions
// can be chatty (curl -v, ssh -vvv, kubectl logs); the last 4KB tend to
// carry the actual error. Larger captures bloat tofu diagnostics.
const captureLimit = 4 * 1024

// Run executes the command. Implements Action.Run for *Command.
//
// Environment isolation: cmd.Env is set to the fully-built slice. Setting
// it to nil would make exec inherit the parent's env, which is the exact
// opposite of what the spec requires - the runner often holds secrets
// (cloud credentials, CI tokens) that should never reach action scripts.
//
// Timeout: a derived context with c.Timeout caps the child. On expiry, the
// process group (Setpgid=true) is killed, not just the parent shell.
// /bin/sh -c can spawn children; signaling the leader alone leaks them.
func (c *Command) Run(ctx context.Context, sc slotctx.SlotContext) Result {
	if c.Timeout <= 0 {
		return Result{Err: errors.New("command: timeout must be > 0")}
	}

	env, shadowed := BuildEnv(sc, c.Env)
	if len(shadowed) > 0 {
		return Result{Err: errors.New("command: operator env shadows reserved namespace: " + strings.Join(shadowed, ","))}
	}

	expected := c.ExpectedExit
	if len(expected) == 0 {
		expected = []int{0}
	}

	runCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", c.Command)
	cmd.Env = env // never nil; nil inherits parent env
	if c.WorkingDir != "" {
		cmd.Dir = c.WorkingDir
	}
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}

	stdout := &tailBuffer{limit: captureLimit}
	stderr := &tailBuffer{limit: captureLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the whole process group, not just /bin/sh.
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	err := cmd.Run()

	res := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.Err = errors.New("command timed out")
		res.ExitCode = -1
		return res
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
			res.Err = err
			return res
		}
	}

	if !inSet(res.ExitCode, expected) {
		res.Err = unexpectedExitError{Got: res.ExitCode, Expected: expected}
	}
	return res
}

func inSet(v int, set []int) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

type unexpectedExitError struct {
	Got      int
	Expected []int
}

func (e unexpectedExitError) Error() string {
	return "command exited with unexpected code"
}

// tailBuffer is an io.Writer that retains only the last `limit` bytes
// written. Cheap and unbounded-input-safe; for our 4KB cap a single
// rebuffer per write is acceptable.
type tailBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if t.buf.Len() > t.limit {
		excess := t.buf.Len() - t.limit
		_ = trimFront(&t.buf, excess)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return t.buf.String() }

func trimFront(b *bytes.Buffer, n int) error {
	_, err := io.CopyN(io.Discard, b, int64(n))
	return err
}
