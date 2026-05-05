// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package actions

import (
	"context"
	"errors"
	"time"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// ReadinessProbe wraps a Command with the polling parameters needed to
// confirm a freshly-created server is ready to serve. Failure resets the
// success streak; the probe succeeds only when SuccessThreshold consecutive
// runs return zero exit code, and fails when wall-clock time exceeds
// TotalTimeout.
type ReadinessProbe struct {
	Command          Command
	Interval         time.Duration
	SuccessThreshold int
	TotalTimeout     time.Duration
}

// Run polls the underlying command. Returns the final attempt's Result on
// success or on total-timeout exhaustion (so diagnostics carry the most
// recent stdout/stderr regardless of outcome).
func (p ReadinessProbe) Run(ctx context.Context, sc slotctx.SlotContext) Result {
	if p.SuccessThreshold <= 0 {
		p.SuccessThreshold = 1
	}
	if p.Interval <= 0 {
		return Result{Err: errors.New("readiness_probe: interval must be > 0")}
	}
	if p.TotalTimeout <= 0 {
		return Result{Err: errors.New("readiness_probe: total_timeout must be > 0")}
	}

	deadline := time.Now().Add(p.TotalTimeout)
	streak := 0
	var last Result

	for {
		last = p.Command.Run(ctx, sc)
		if last.Err == nil && !last.TimedOut {
			streak++
			if streak >= p.SuccessThreshold {
				return last
			}
		} else {
			streak = 0
		}

		// Stop if our wall-clock budget is exhausted, OR if the parent
		// context is done. The Command itself enforces its per-attempt
		// timeout via its own derived context.
		if time.Now().After(deadline) {
			if last.Err == nil {
				last.Err = errors.New("readiness_probe: total_timeout exhausted before success_threshold reached")
			}
			return last
		}
		select {
		case <-ctx.Done():
			last.Err = ctx.Err()
			return last
		case <-time.After(p.Interval):
		}
	}
}
