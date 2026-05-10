// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"strconv"
	"sync/atomic"
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

// Create-first replaces leave a transient state where the slot has both
// the old (gen=N) complete server and the new (gen=N+1) complete server
// while the new is being verified. If the apply crashes between the new
// server's complete-flip and the old server's delete, the next apply
// must reap the now-superseded old server.
func TestPreflight_DestroysSupersededServers(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	old := c.SeedServer(testGroup, 0, 1, testNetwork)
	new := c.SeedServer(testGroup, 0, 2, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: new.ID, ServerName: new.Name,
			Generation: 2, ReplaceHash: g.HashFull,
			PrivateIP: hcloudxtest.SeedPrivateIP(new.ID),
			Status:    reconciler.StatusReady},
	}}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, 1, c.DeleteCalls, "exactly the superseded old server (gen=1) is destroyed")
	require.Equal(t, 0, c.CreateCalls, "the new server is healthy; nothing to create")
	require.Contains(t, c.DeletedIDs, old.ID, "the deleted server must be the old gen=1 one")
	require.NotContains(t, c.DeletedIDs, new.ID, "the new gen=2 server must survive")
	require.Len(t, state.Slots, 1)
	require.Equal(t, new.ID, state.Slots[0].ServerID, "state continues to point at the new server")
}

// Gate: if the new server (the one state points at) is observed
// complete=false, the old must NOT be reaped as superseded — the new is
// an orphan from a crashed mid-readiness window. The orphan-reap path
// takes new; the rebind step recovers state from the surviving old.
func TestPreflight_DoesNotReapOldWhenNewIncomplete(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	old := c.SeedServer(testGroup, 0, 1, testNetwork)
	newOrphan := c.SeedOrphan(testGroup, 0, 2, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: newOrphan.ID, ServerName: newOrphan.Name,
			Generation: 2, ReplaceHash: g.HashFull,
			PrivateIP: hcloudxtest.SeedPrivateIP(newOrphan.ID),
			Status:    reconciler.StatusReady},
	}}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)

	// The orphan is reaped. The old (complete=true) is preserved by the
	// gate and rebound by the postlude. After preflight, state points at
	// the surviving old (with empty hash → triggers phaseReplace), and
	// phaseReplace creates a fresh new server at gen=3 (max(1,2)+1 = 3,
	// preserved across the orphan reap by genHighWater).
	require.Contains(t, c.DeletedIDs, newOrphan.ID, "the gen=2 orphan must be reaped")
	require.NotEqual(t, []int64{old.ID}, c.DeletedIDs[:1],
		"the gen=1 complete server must survive the preflight gate")
	require.Equal(t, 1, c.CreateCalls, "phaseReplace re-rolls the slot after rebind")
	require.Len(t, state.Slots, 1)
	require.Equal(t, 3, state.Slots[0].Generation,
		"new gen must be max(observed=2)+1 = 3, even though the gen=2 orphan was reaped")
	require.Equal(t, g.HashFull, state.Slots[0].ReplaceHash, "post-replace hash matches desired")
}

// Localized regression signal for the §7b rebind invariant. The
// integration test below (TestApply_CreateFirst_CrashDuringReadiness_*)
// exercises rebind transitively through the full Apply pipeline; this one
// pins the post-preflight state shape so a future change that breaks the
// rebind specifically (e.g. ServerID, Generation, or the empty-hash
// sentinel) fails here instead of blaming phaseReplace.
func TestPreflight_RebindsStateAfterOrphanReap(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	old := c.SeedServer(testGroup, 0, 1, testNetwork)
	newOrphan := c.SeedOrphan(testGroup, 0, 2, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: newOrphan.ID, ServerName: newOrphan.Name,
			Generation: 2, ReplaceHash: g.HashFull,
			PrivateIP: hcloudxtest.SeedPrivateIP(newOrphan.ID),
			Status:    reconciler.StatusReady},
	}}

	state, err := reconciler.RunPreflightForTest(context.Background(), c, g, prior)
	require.NoError(t, err)
	require.Equal(t, 1, c.DeleteCalls, "preflight reaps the gen=2 orphan")
	require.Contains(t, c.DeletedIDs, newOrphan.ID)
	require.NotContains(t, c.DeletedIDs, old.ID)

	require.Len(t, state.Slots, 1)
	got := state.Slots[0]
	require.Equal(t, 0, got.SlotID)
	require.Equal(t, old.ID, got.ServerID, "rebound to the surviving complete server")
	require.Equal(t, old.Name, got.ServerName)
	require.Equal(t, 1, got.Generation, "Generation matches the surviving canonical, not the reaped orphan")
	require.Empty(t, got.ReplaceHash,
		`empty ReplaceHash is the "needs replace" sentinel — labels store only the 12-char prefix, so the full-hash compare in phaseReplace can't be reconstructed`)
	require.Equal(t, reconciler.StatusReady, got.Status)
	require.Equal(t, hcloudxtest.SeedPrivateIP(old.ID), got.PrivateIP, "private IP rebound from surviving server")
}

func TestApply_CreateFirst_CrashDuringReadiness_RecoversInOneApply(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)
	g.ReplaceMethod = reconciler.ReplaceMethodCreateBeforeDestroy

	old := c.SeedServer(testGroup, 0, 1, testNetwork)
	newOrphan := c.SeedOrphan(testGroup, 0, 2, testNetwork)

	// State as if innerCreate.Upsert ran before the complete-flip.
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: newOrphan.ID, ServerName: newOrphan.Name,
			Generation: 2, ReplaceHash: g.HashFull,
			PrivateIP: hcloudxtest.SeedPrivateIP(newOrphan.ID),
			Status:    reconciler.StatusReady},
	}}

	var log []string
	mu := &atomic.Int32{}
	g.Actions.BeforeCreate = orderingAction{hook: "before_create", log: &log, mu: mu}
	g.Actions.PostCreate = orderingAction{hook: "post_create", log: &log, mu: mu}
	g.Actions.BeforeReplace = orderingAction{hook: "before_replace", log: &log, mu: mu}
	g.Actions.PostReplace = orderingAction{hook: "post_replace", log: &log, mu: mu}
	g.Actions.BeforeRemove = orderingAction{hook: "before_remove", log: &log, mu: mu}
	g.Actions.PostRemove = orderingAction{hook: "post_remove", log: &log, mu: mu}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)

	// Single Apply must converge: orphan reaped in preflight, state
	// rebound to the surviving old, phaseReplace re-rolls slot with full
	// hooks (create-first ordering), final state has one complete=true
	// server at the next generation.
	require.Equal(t, []string{
		"before_replace",
		"before_create",
		"post_create",
		"before_remove",
		"post_remove",
		"post_replace",
	}, log, "full create-first hook sequence fires during recovery")

	// Servers in hcloud after recovery: only the freshly-created gen=3.
	require.Len(t, c.Servers, 1, "only the new gen=3 server remains")
	// Deletion order matters: gen=2 orphan first (preflight), then gen=1
	// `old` via ReplaceSlot's innerRemove. Wrong order would mean the gate
	// or rebind misfired.
	require.Equal(t, []int64{newOrphan.ID, old.ID}, c.DeletedIDs,
		"preflight reaps the gen=2 orphan first; ReplaceSlot drops the gen=1 old after the gen=3 create")

	require.Len(t, state.Slots, 1)
	require.Equal(t, 3, state.Slots[0].Generation, "next gen = max(observed=2)+1 = 3")
	require.Equal(t, reconciler.StatusReady, state.Slots[0].Status)
	require.Equal(t, g.HashFull, state.Slots[0].ReplaceHash)
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
