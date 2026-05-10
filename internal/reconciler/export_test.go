// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler

import (
	"context"
	"fmt"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// RunPreflightForTest is a black-box test entry point that runs only the
// preflight phase (reap + state rebind) and returns the resulting state.
// It exists so tests can assert the post-preflight state invariants in
// isolation, without the noise of phaseReplace / phaseCreate side effects.
func RunPreflightForTest(ctx context.Context, c hcloudx.Client, group Group, prior State) (State, error) {
	servers, err := c.ListByGroup(ctx, group.Name)
	if err != nil {
		return prior, fmt.Errorf("list servers: %w", err)
	}
	observed := hcloudx.PartitionBySlot(servers)
	r := &runner{
		client:       c,
		group:        group,
		state:        &State{Slots: append([]SlotState(nil), prior.Slots...)},
		observed:     observed,
		genHighWater: snapshotGenerations(observed),
	}
	if err := r.preflight(ctx); err != nil {
		return *r.state, err
	}
	return *r.state, nil
}
