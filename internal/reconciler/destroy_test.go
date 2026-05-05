// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

func TestDestroy_RemovesEverySlot_HighestToLowest(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(3)

	prior := reconciler.State{}
	for i := 0; i < 3; i++ {
		srv := c.SeedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, reconciler.SlotState{
			SlotID: i, ServerID: srv.ID, ServerName: srv.Name,
			Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady,
			PrivateIP: hcloudxtest.SeedPrivateIP(srv.ID),
		})
	}

	_, err := reconciler.New(c).Destroy(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, 3, c.DeleteCalls, "all three slots destroyed")
	require.Equal(t, []int64{3, 2, 1}, c.DeletedIDs, "destroy walks highest slot first")
	require.Empty(t, c.Servers, "no servers remain in hcloud")
}

func TestDestroy_SweepsOrphansAfterStateWalk(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	srv := c.SeedServer(testGroup, 0, 1, testNetwork)
	// An orphan that doesn't appear in tofu state — should still get
	// swept by the orphan-cleanup pass at the end of Destroy.
	c.SeedOrphan(testGroup, 0, 99, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{{
		SlotID: 0, ServerID: srv.ID, ServerName: srv.Name,
		Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady,
		PrivateIP: hcloudxtest.SeedPrivateIP(srv.ID),
	}}}

	_, err := reconciler.New(c).Destroy(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Empty(t, c.Servers, "destroy must sweep orphans too")
}

func TestDestroy_BeforeRemoveFailure_ReturnsPartialState(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)
	g.Actions.BeforeRemove = failingActionOnSlot{slot: 1, err: errors.New("drain refused for slot 1")}

	prior := reconciler.State{}
	for i := 0; i < 2; i++ {
		srv := c.SeedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, reconciler.SlotState{
			SlotID: i, ServerID: srv.ID, Generation: 1, ReplaceHash: g.HashFull,
			Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(srv.ID),
		})
	}

	state, err := reconciler.New(c).Destroy(context.Background(), g, prior, nil)
	require.Error(t, err, "before_remove failure on slot 1 must surface")
	require.Equal(t, 0, c.DeleteCalls, "slot 1's before_remove failed → no deletion happens; slot 0 (lower) not yet attempted")

	// Both slots remain in state — slot 1 marked failed with the cause,
	// slot 0 untouched (highest-to-zero order, slot 0 not yet attempted).
	require.Len(t, state.Slots, 2, "destroy must return partial state, not empty")
	bySlot := map[int]reconciler.SlotState{}
	for _, sl := range state.Slots {
		bySlot[sl.SlotID] = sl
	}
	require.Equal(t, reconciler.StatusFailed, bySlot[1].Status,
		"slot 1's before_remove failed → marked failed in returned state")
	require.Contains(t, bySlot[1].LastError, "drain refused for slot 1",
		"failure cause must propagate to LastError")
	require.Equal(t, reconciler.StatusReady, bySlot[0].Status,
		"slot 0 not yet visited → original Ready status preserved")
}

func TestDestroy_Idempotent_OnEmptyState(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)
	_, err := reconciler.New(c).Destroy(context.Background(), g, reconciler.State{}, nil)
	require.NoError(t, err)
	require.Equal(t, 0, c.DeleteCalls)
}
