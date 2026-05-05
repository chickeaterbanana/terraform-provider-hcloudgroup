// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// failingAction returns a result with Err set every time it's invoked.
type failingAction struct {
	err    error
	stdout string
	stderr string
}

func (f failingAction) Run(_ context.Context, _ slotctx.SlotContext) actions.Result {
	return actions.Result{Err: f.err, Stdout: f.stdout, Stderr: f.stderr}
}

// failingActionOnSlot fails only when invoked on the matching slot id.
type failingActionOnSlot struct {
	slot int
	err  error
}

func (f failingActionOnSlot) Run(_ context.Context, sc slotctx.SlotContext) actions.Result {
	if sc.SlotID == f.slot {
		return actions.Result{Err: f.err}
	}
	return actions.Result{}
}

func TestApply_BeforeCreateFailure_ProducesSlotError_AndPreservesPrior(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)
	g.Actions.BeforeCreate = failingAction{err: errors.New("hook denied"), stderr: "policy reject"}

	_, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, nil)

	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, 0, slotErr.SlotID, "first slot should have failed")
	require.Equal(t, "before_create", slotErr.Phase)
	require.Equal(t, "policy reject", slotErr.Stderr)
	require.Equal(t, 0, c.CreateCalls, "no hcloud create on before_create failure")
}

func TestApply_PostCreateFailure_LeavesIncompleteServerInHcloud(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)
	g.Actions.PostCreate = failingAction{err: errors.New("registration failed")}

	state, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, nil)

	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, "post_create", slotErr.Phase)

	// Server exists but complete=false — pre-flight will clean it next apply.
	require.Equal(t, 1, c.CreateCalls)
	require.Equal(t, 0, c.DeleteCalls, "incomplete server is NOT destroyed inline (README §11)")
	require.Len(t, c.Servers, 1)
	for _, s := range c.Servers {
		require.Equal(t, "false", s.Labels[hcloudx.LabelComplete],
			"slot must remain complete=false so canonical-pick ignores it")
	}

	// State reflects the failure: status=failed, last_error populated.
	require.Len(t, state.Slots, 1)
	require.Equal(t, reconciler.StatusFailed, state.Slots[0].Status)
	require.Contains(t, state.Slots[0].LastError, "post_create")
}

func TestApply_PartialProgress_PreservesEarlierSlots(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(3)
	// Fail post_create only on slot 1. Slot 0 should reach ready, slot 2
	// never starts (sequential).
	g.Actions.PostCreate = failingActionOnSlot{slot: 1, err: errors.New("fail slot 1")}

	state, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, nil)
	require.Error(t, err)

	require.Len(t, state.Slots, 2, "slot 0 (ready) + slot 1 (failed); slot 2 never attempted")
	require.Equal(t, reconciler.StatusReady, state.Slots[0].Status)
	require.Equal(t, reconciler.StatusFailed, state.Slots[1].Status)

	// Verify slot 0 has hcloud labels complete=true (post_create succeeded).
	servers, _ := c.ListByGroup(context.Background(), testGroup)
	require.Len(t, servers, 2, "two servers created (slot 0 + slot 1 incomplete)")
}

func TestApply_BeforeReplaceFailure_DoesNotDeleteOldServer(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)

	// Seed an existing slot, then trigger a replace by changing hash.
	srv := c.SeedServer(testGroup, 0, 1, testNetwork)
	prior := reconciler.State{Slots: []reconciler.SlotState{{
		SlotID: 0, ServerID: srv.ID, ServerName: srv.Name,
		Generation: 1, ReplaceHash: "OLD",
		PrivateIP: hcloudxtest.SeedPrivateIP(srv.ID),
		Status:    reconciler.StatusReady,
	}}}

	g.Actions.BeforeReplace = failingAction{err: errors.New("drain refused")}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)

	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, "before_replace", slotErr.Phase)
	require.Equal(t, 0, c.DeleteCalls, "must not delete old server when before_replace failed")
	require.Equal(t, 0, c.CreateCalls, "must not create new server when before_replace failed")
}

func TestApply_BeforeRemoveFailure_DoesNotDeleteServer(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	// Two slots; scale down to 1 with a failing before_remove.
	for i := 0; i < 2; i++ {
		srv := c.SeedServer(testGroup, i, 1, testNetwork)
		_ = srv
	}
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(1)},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(2)},
	}}

	g.Count = 1
	g.Actions.BeforeRemove = failingAction{err: errors.New("drain refused")}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, "before_remove", slotErr.Phase)
	require.Equal(t, 0, c.DeleteCalls, "must not delete server when before_remove failed")
}

func TestApply_PostRemoveFailure_StillTombstonesServer(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)
	for i := 0; i < 2; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}
	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ServerID: 1, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(1)},
		{SlotID: 1, ServerID: 2, Generation: 1, ReplaceHash: g.HashFull, Status: reconciler.StatusReady, PrivateIP: hcloudxtest.SeedPrivateIP(2)},
	}}

	g.Count = 1
	g.Actions.PostRemove = failingAction{err: errors.New("audit hook failed")}

	_, err := reconciler.New(c).Apply(context.Background(), g, prior, nil)
	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, "post_remove", slotErr.Phase)
	require.Equal(t, 1, c.DeleteCalls, "delete already happened before post_remove ran")
}

func TestApply_ReadinessProbeFailure_PreservesIncompleteServer(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)
	g.ReadinessProbe = &actions.ReadinessProbe{
		Command: actions.Command{
			Command: "false",
			Env:     map[string]string{"PATH": "/usr/bin:/bin"},
			Timeout: 200 * time.Millisecond,
		},
		Interval:         10 * time.Millisecond,
		SuccessThreshold: 1,
		TotalTimeout:     50 * time.Millisecond,
	}

	state, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, nil)

	var slotErr *reconciler.SlotError
	require.ErrorAs(t, err, &slotErr)
	require.Equal(t, "readiness_probe", slotErr.Phase)
	require.Equal(t, 1, c.CreateCalls)
	require.Equal(t, 0, c.DeleteCalls, "failed-probe server stays in hcloud as orphan")
	require.Len(t, c.Servers, 1)
	for _, s := range c.Servers {
		require.Equal(t, "false", s.Labels[hcloudx.LabelComplete])
	}
	require.Equal(t, reconciler.StatusFailed, state.Slots[0].Status)
}

func TestApply_ProgressFnFiresOnPartialFailure(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(3)
	// Fail slot 1. Progress callback should fire for slot 0 (success).
	g.Actions.PostCreate = failingActionOnSlot{slot: 1, err: errors.New("fail")}

	var snapshots []int
	progress := func(_ context.Context, snap reconciler.State) error {
		snapshots = append(snapshots, len(snap.Slots))
		return nil
	}

	_, err := reconciler.New(c).Apply(context.Background(), g, reconciler.State{}, progress)
	require.Error(t, err)
	require.Equal(t, []int{1}, snapshots,
		"progress fires after slot 0; slot 1 fails before reportProgress; slot 2 never starts")
}
