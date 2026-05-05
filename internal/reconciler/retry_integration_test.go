// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

// Drives one create through the retry path: the first WaitForAction call
// fails with ErrFakeNetwork (retryable), the second succeeds. Apply must
// converge with exactly one server in the fake.
func TestApply_RetriesTransientWaitForActionFailure(t *testing.T) {
	c := hcloudxtest.NewFake()
	c.WaitForFailures = 1

	state, err := reconciler.New(c).Apply(context.Background(), defaultGroup(1), reconciler.State{}, nil)
	require.NoError(t, err)
	require.Len(t, state.Slots, 1)
	require.Len(t, c.Servers, 1, "retry should not leave duplicate servers")
}

// The complete=true label flip is a GET+PUT pair and idempotent — a
// transient network error must be retried, not surfaced as a slot
// failure. Without the Retry wrapper, this run would mark the slot
// Failed on the first ErrFakeNetwork; the next apply would then destroy
// the healthy server it was about to bless.
func TestApply_RetriesTransientCompleteLabelFlip(t *testing.T) {
	c := hcloudxtest.NewFake()
	c.UpdateLabelsFailures = 1 // retryable failure on the complete=true PUT

	state, err := reconciler.New(c).Apply(context.Background(), defaultGroup(1), reconciler.State{}, nil)
	require.NoError(t, err, "complete=true label flip must retry through transient errors")
	require.Len(t, state.Slots, 1)
	require.Equal(t, reconciler.StatusReady, state.Slots[0].Status, "slot should converge to Ready, not Failed")

	for _, s := range c.Servers {
		require.Equal(t, "true", s.Labels["hcloudgroup.io/complete"],
			"server must be marked complete=true after retry succeeds")
	}
}

// The post-create GetServer re-read must propagate transient errors
// through Retry — silent fallback to the CreateServer response would
// write an empty ip_private into state because Hetzner attaches networks
// asynchronously and the create response often lacks PrivateNet.
func TestApply_RetriesTransientGetServerAfterCreate(t *testing.T) {
	c := hcloudxtest.NewFake()
	c.GetServerFailures = 1 // re-read fails once, then succeeds

	state, err := reconciler.New(c).Apply(context.Background(), defaultGroup(1), reconciler.State{}, nil)
	require.NoError(t, err, "transient post-create re-read failure must retry")
	require.Len(t, state.Slots, 1)
	require.NotEmpty(t, state.Slots[0].PrivateIP,
		"successful re-read populates PrivateIP — must not be silently empty")
}
