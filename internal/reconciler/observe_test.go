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

func TestObserve_RebuildsStateFromLabels(t *testing.T) {
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	for i := 0; i < 2; i++ {
		c.SeedServer(testGroup, i, 1, testNetwork)
	}

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, ReplaceHash: "preserve-this-full-hash", Generation: 1},
		{SlotID: 1, ReplaceHash: "and-this", Generation: 1},
	}}

	got, err := reconciler.New(c).Observe(context.Background(), g, prior)
	require.NoError(t, err)
	require.Len(t, got.Slots, 2)

	// ReplaceHash carries through from prior — Observe doesn't store the
	// full hash on the server (only the 12-char prefix is in labels).
	require.Equal(t, "preserve-this-full-hash", got.Slots[0].ReplaceHash)
	require.Equal(t, "and-this", got.Slots[1].ReplaceHash)
	require.Equal(t, reconciler.StatusReady, got.Slots[0].Status)
	require.NotEmpty(t, got.Slots[0].PrivateIP)
}

func TestObserve_DropsSlotWhenCanonicalMissing(t *testing.T) {
	// Initial state has 2 slots, but only 1 canonical exists in hcloud
	// (the other was deleted out of band). README §5.3: Observe shrinks
	// the state, prompting a re-create on the next apply.
	c := hcloudxtest.NewFake()
	g := defaultGroup(2)

	c.SeedServer(testGroup, 0, 1, testNetwork)
	// Slot 1: only an orphan (complete=false), no canonical.
	c.SeedOrphan(testGroup, 1, 1, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, Generation: 1, ReplaceHash: "h"},
		{SlotID: 1, Generation: 1, ReplaceHash: "h"},
	}}

	got, err := reconciler.New(c).Observe(context.Background(), g, prior)
	require.NoError(t, err)
	require.Len(t, got.Slots, 1, "slot 1 has no canonical → dropped from observed state")
	require.Equal(t, 0, got.Slots[0].SlotID)
}

func TestObserve_ReplaceHashPreservedNotFromLabel(t *testing.T) {
	// The server's hcloudgroup.io/replace-hash label is the 12-char
	// prefix. Observe must NOT use it as the SlotState.ReplaceHash —
	// that would silently turn a no-op apply into a replace because the
	// stored full hash never matches the prefix.
	c := hcloudxtest.NewFake()
	g := defaultGroup(1)
	c.SeedServer(testGroup, 0, 1, testNetwork)

	prior := reconciler.State{Slots: []reconciler.SlotState{
		{SlotID: 0, Generation: 1, ReplaceHash: "FULL_HASH_64_CHARS_NOT_PREFIX"},
	}}

	got, err := reconciler.New(c).Observe(context.Background(), g, prior)
	require.NoError(t, err)
	require.Len(t, got.Slots, 1)
	require.Equal(t, "FULL_HASH_64_CHARS_NOT_PREFIX", got.Slots[0].ReplaceHash,
		"prior full hash must pass through; do not read the truncated label")
}

func TestObserve_PropagatesListError(t *testing.T) {
	c := hcloudxtest.NewFake()
	c.FailListByGroupErr = errSimulatedListFailure
	_, err := reconciler.New(c).Observe(context.Background(), defaultGroup(1), reconciler.State{})
	require.ErrorIs(t, err, errSimulatedListFailure)
}

// Sentinel error for plumbing tests.
var errSimulatedListFailure = forcedListErr("simulated")

type forcedListErr string

func (e forcedListErr) Error() string { return string(e) }
