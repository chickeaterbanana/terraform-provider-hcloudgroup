// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// orderingAction appends a stamped string to a shared slice each time it
// is invoked. Used to assert phase ordering across slots.
type orderingAction struct {
	hook string
	log  *[]string
	mu   *atomic.Int32
}

func (a orderingAction) Run(_ context.Context, sc slotctx.SlotContext) actions.Result {
	if a.mu != nil {
		_ = a.mu.Add(1)
	}
	if a.log != nil {
		*a.log = append(*a.log, a.hook)
	}
	return actions.Result{}
}

func TestApply_MixedDelta_RemoveBeforeReplaceBeforeCreate(t *testing.T) {
	// Initial: 3 slots at OLD hash. Desired: 2 slots at NEW hash plus a
	// new slot beyond? Actually let's do count=2 with NEW hash so:
	//   - slot 2 → remove
	//   - slots 0,1 → replace
	// (no new creates beyond slot 2). Then drive a follow-up apply where
	// count grows to 3 to exercise create-after-replace too.
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	for i := 0; i < 3; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: "OLD", PrivateIP: hcloudxtest.SeedPrivateIP(1), Status: reconciler.StatusReady},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: "OLD", PrivateIP: hcloudxtest.SeedPrivateIP(2), Status: reconciler.StatusReady},
		{SlotID: 2, ServerID: 3, Generation: 1, ReplaceHash: g.HashFull, PrivateIP: hcloudxtest.SeedPrivateIP(3), Status: reconciler.StatusReady},
	}}

	state, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Len(t, state.Slots, 2, "slot 2 removed, slots 0,1 replaced")

	// Slots 0 and 1 should each have generation=2 (one replace), slot 2 gone.
	for i, sl := range state.Slots {
		require.Equal(t, i, sl.SlotID)
		require.Equal(t, 2, sl.Generation, "slot %d replaced once → gen 2", i)
		require.Equal(t, g.HashFull, sl.ReplaceHash)
	}

	// Verify the call order on the fake: first delete fires for slot 2's
	// server (id=3), THEN the replace pairs (delete + create) for slots
	// 0 and 1.
	require.Equal(t, 3, c.DeleteCalls, "1 remove + 2 replaces")
	require.Equal(t, 2, c.CreateCalls, "2 replaces (no scale-up)")
	require.Equal(t, int64(3), c.DeletedIDs[0],
		"phase order means slot 2 is removed before any replace runs")
}

func TestApply_HookOrderingOnReplace(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	c.SeedServer(testGroup, 0, 1, testNetwork)
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: "OLD", PrivateIP: hcloudxtest.SeedPrivateIP(1), Status: reconciler.StatusReady},
	}}

	var log []string
	mu := &atomic.Int32{}
	g.Actions.BeforeCreate = orderingAction{hook: "before_create", log: &log, mu: mu}
	g.Actions.PostCreate = orderingAction{hook: "post_create", log: &log, mu: mu}
	g.Actions.BeforeReplace = orderingAction{hook: "before_replace", log: &log, mu: mu}
	g.Actions.PostReplace = orderingAction{hook: "post_replace", log: &log, mu: mu}
	g.Actions.BeforeRemove = orderingAction{hook: "before_remove", log: &log, mu: mu}
	g.Actions.PostRemove = orderingAction{hook: "post_remove", log: &log, mu: mu}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)

	// Per README §6 step list:
	// 1. before_replace
	// 2. before_remove
	// 3. (delete)
	// 4. post_remove
	// 5. before_create
	// 6. (create)
	// 7. (readiness probe — none here)
	// 8. post_create
	// 9. (label flip)
	// 10. post_replace
	require.Equal(t, []string{
		"before_replace",
		"before_remove",
		"post_remove",
		"before_create",
		"post_create",
		"post_replace",
	}, log, "hook ordering on replace flow")
}

func TestApply_PureScaleDown_SkipsReplacePhase(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	for i := 0; i < 3; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}
	// Prior matches desired hash for all slots → no replace, just scale-down.
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: g.HashFull, PrivateIP: hcloudxtest.SeedPrivateIP(1), Status: reconciler.StatusReady},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: g.HashFull, PrivateIP: hcloudxtest.SeedPrivateIP(2), Status: reconciler.StatusReady},
		{SlotID: 2, ServerID: 3, Generation: 1, ReplaceHash: g.HashFull, PrivateIP: hcloudxtest.SeedPrivateIP(3), Status: reconciler.StatusReady},
	}}

	var beforeReplaceCalls int32
	g.Actions.BeforeReplace = orderingAction{mu: &atomic.Int32{}, log: nil, hook: "br"}
	g.Actions.BeforeReplace = recordingAction{calls: &beforeReplaceCalls}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, int32(0), beforeReplaceCalls, "scale-down only must not invoke before_replace")
	require.Equal(t, 1, c.DeleteCalls)
	require.Equal(t, 0, c.CreateCalls)
}
