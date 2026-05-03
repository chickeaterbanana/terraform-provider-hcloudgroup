package actions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadinessProbe_ReachesSuccessThreshold(t *testing.T) {
	probe := ReadinessProbe{
		Command: Command{
			Command: "true",
			Env:     map[string]string{"PATH": "/usr/bin:/bin"},
			Timeout: time.Second,
		},
		Interval:         10 * time.Millisecond,
		SuccessThreshold: 3,
		TotalTimeout:     2 * time.Second,
	}
	res := probe.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
}

func TestReadinessProbe_FailureResetsStreak(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "ready")
	probe := ReadinessProbe{
		Command: Command{
			// Succeed only once the file exists. Caller creates it
			// after enough attempts to verify the streak is reset by
			// earlier failures.
			Command: "test -f " + flag,
			Env:     map[string]string{"PATH": "/usr/bin:/bin"},
			Timeout: time.Second,
		},
		Interval:         50 * time.Millisecond,
		SuccessThreshold: 2,
		TotalTimeout:     5 * time.Second,
	}

	go func() {
		// Let a few failed attempts run, then enable success.
		time.Sleep(150 * time.Millisecond)
		_ = touch(flag)
	}()

	res := probe.Run(context.Background(), slotCtx())
	require.NoError(t, res.Err)
}

func TestReadinessProbe_TotalTimeoutFails(t *testing.T) {
	probe := ReadinessProbe{
		Command: Command{
			Command: "false",
			Env:     map[string]string{"PATH": "/usr/bin:/bin"},
			Timeout: 100 * time.Millisecond,
		},
		Interval:         50 * time.Millisecond,
		SuccessThreshold: 1,
		TotalTimeout:     250 * time.Millisecond,
	}
	res := probe.Run(context.Background(), slotCtx())
	require.Error(t, res.Err)
}

func touch(path string) error {
	return writeFile(path, "")
}
