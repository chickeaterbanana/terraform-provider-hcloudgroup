// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package actions

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

func TestCommand_RunSuccess(t *testing.T) {
	c := &Command{
		Command: "printf hello",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Timeout: 5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, "hello", res.Stdout)
}

func TestCommand_NonZeroExitFails(t *testing.T) {
	c := &Command{
		Command: "exit 7",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Timeout: 5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.Error(t, res.Err)
	require.Equal(t, 7, res.ExitCode)
}

func TestCommand_AcceptsCustomExpectedExit(t *testing.T) {
	c := &Command{
		Command:      "exit 2",
		Env:          map[string]string{"PATH": "/usr/bin:/bin"},
		ExpectedExit: []int{0, 2},
		Timeout:      5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
	require.Equal(t, 2, res.ExitCode)
}

func TestCommand_TimeoutKillsProcessGroup(t *testing.T) {
	c := &Command{
		Command: "sleep 30",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Timeout: 200 * time.Millisecond,
	}
	start := time.Now()
	res := c.Run(context.Background(), slotCtx())
	elapsed := time.Since(start)
	require.True(t, res.TimedOut, "expected TimedOut=true")
	require.Less(t, elapsed, 5*time.Second, "process should be killed promptly, not orphaned")
}

func TestCommand_CleanEnv_DoesNotInheritParent(t *testing.T) {
	const sentinel = "HCLOUDGROUP_TEST_CANARY_DO_NOT_INHERIT"
	t.Setenv(sentinel, "set-by-runner")
	c := &Command{
		Command: "printenv " + sentinel + " || echo MISSING",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Timeout: 5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
	require.Equal(t, "MISSING\n", res.Stdout, "child must NOT inherit runner env")
}

func TestCommand_CapturesStdoutAndStderr(t *testing.T) {
	c := &Command{
		Command: "printf out; printf err 1>&2",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Timeout: 5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
	require.Equal(t, "out", res.Stdout)
	require.Equal(t, "err", res.Stderr)
}

func TestCommand_StdinDelivered(t *testing.T) {
	c := &Command{
		Command: "cat",
		Env:     map[string]string{"PATH": "/usr/bin:/bin"},
		Stdin:   "fed-via-stdin",
		Timeout: 5 * time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
	require.Equal(t, "fed-via-stdin", res.Stdout)
}

func TestCommand_TailBufferTrimsToLast4KB(t *testing.T) {
	tb := &tailBuffer{limit: 4 * 1024}
	for i := 0; i < 1000; i++ {
		_, _ = tb.Write([]byte("0123456789"))
	}
	require.Equal(t, 4*1024, len(tb.String()))
}

func TestCommand_RejectsShadowingEnv(t *testing.T) {
	c := &Command{
		Command: "true",
		Env:     map[string]string{"HCLOUDGROUP_SLOT_ID": "999"},
		Timeout: time.Second,
	}
	res := c.Run(context.Background(), slotCtx())
	require.Error(t, res.Err)
}

func slotCtx() slotctx.SlotContext {
	return slotctx.SlotContext{GroupName: "g", SlotID: 0, Generation: 1, ServerName: "g-0-1", Now: time.Now()}
}

func TestMain(m *testing.M) {
	// Defensive: tests assume a Linux/macOS-style /bin/sh. Skip on other
	// platforms. The provider doesn't support them in v1.
	if _, err := os.Stat("/bin/sh"); err != nil {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
