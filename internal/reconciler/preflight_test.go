// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

func TestPreflight_DestroysIncompleteOrphans_Even_WhenInRange(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	// Two healthy slots already in tofu state, plus an orphan for slot 0
	// at a higher generation (a crashed mid-replace).
	for i := 0; i < 2; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}
	c.SeedOrphan(testGroup, 0, 99, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(1)},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(2)},
	}}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, 1, c.DeleteCalls, "exactly the orphan is destroyed in pre-flight")
	require.Equal(t, 0, c.CreateCalls, "healthy slots are not recreated")
	require.Len(t, state.Slots, 2)
}

func TestPreflight_DestroysOutOfRangeStragglers(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	// Healthy slots 0, 1 in state; a straggler at slot 5 with NO state
	// entry (residue from a crashed scale-down).
	for i := 0; i < 2; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}
	c.SeedServer(testGroup, 5, 1, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(1)},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(2)},
	}}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, 1, c.DeleteCalls, "exactly the straggler is destroyed")
}

// Generation source-of-truth (README §5.4): the spec says the new
// generation is "max(observed)+1 across canonical AND orphan", with the
// orphan's history surviving its pre-flight destruction so the new server
// doesn't collide with the orphan's name.
//
// Prior at gen=2, orphan at gen=5; after preflight the orphan is gone but
// the runner remembers its generation via the genHighWater snapshot taken
// before cleanup, so the new gen is max(2, 5)+1 = 6 — Hetzner therefore
// won't reject the create for duplicate name against the just-deleted
// orphan.
func TestPreflight_NextGenerationFromPreCleanupSnapshot_AvoidsNameCollision(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	srv := c.SeedServer(testGroup, 0, 2, testNetwork)
	c.SeedOrphan(testGroup, 0, 5, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{{
		SlotID: 0, ServerID: srv.ID, ServerName: srv.Name,
		Generation: 2, ReplaceHash: "OLD",
		PrivateIP: hcloudxtest.SeedPrivateIP(srv.ID),
		Status:    reconciler.StatusReady,
	}}}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Len(t, state.Slots, 1)
	require.Equal(t, 6, state.Slots[0].Generation,
		"new gen must be max(canonical=2, orphan=5)+1 = 6 — preserves the orphan's history across pre-flight (README §5.4)")
}

func TestPreflight_OrphansAndStragglersTogether_PhaseOrdered(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(3)

	// Initially-empty group: pre-flight finds two pieces of crash residue.
	c.SeedOrphan(testGroup, 0, 1, testNetwork)
	c.SeedServer(testGroup, 7, 1, testNetwork) // straggler beyond count

	state, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, c.DeleteCalls, "orphan + straggler destroyed")
	require.Equal(t, 3, c.CreateCalls, "all 3 slots created fresh")
	require.Len(t, state.Slots, 3)

	// Slot 0 had an orphan at gen=1 destroyed in pre-flight: the runner
	// remembers it via genHighWater and creates the replacement at gen=2.
	// Slots 1 and 2 had nothing observed, so they create at gen=1.
	gens := map[int]string{}
	for _, s := range c.Servers {
		slotID, err := strconv.Atoi(s.Labels[hcloudx.LabelSlot])
		require.NoError(t, err)
		gens[slotID] = s.Labels[hcloudx.LabelGeneration]
	}
	require.Equal(t, "2", gens[0], "slot 0's new server must skip the destroyed orphan's gen")
	require.Equal(t, "1", gens[1])
	require.Equal(t, "1", gens[2])
}
