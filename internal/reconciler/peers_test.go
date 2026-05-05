// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPeersExcluding_OmitsExcludedSlot(t *testing.T) {
	s := &State{Slots: []SlotState{
		{SlotID: 0, ServerName: "g-0-1", PrivateIP: "10.0.0.10", Generation: 1},
		{SlotID: 1, ServerName: "g-1-1", PrivateIP: "10.0.0.11", Generation: 1},
		{SlotID: 2, ServerName: "g-2-1", PrivateIP: "10.0.0.12", Generation: 1},
	}}
	peers := peersExcluding(s, 1)
	require.Len(t, peers, 2)
	require.Equal(t, 0, peers[0].SlotID)
	require.Equal(t, 2, peers[1].SlotID)
}

func TestPeersExcluding_DropsEmptyPrivateIP(t *testing.T) {
	s := &State{Slots: []SlotState{
		{SlotID: 0, ServerName: "g-0-1", PrivateIP: "10.0.0.10", Generation: 1},
		{SlotID: 1, ServerName: "g-1-1", PrivateIP: "", Generation: 1}, // not yet attached
	}}
	peers := peersExcluding(s, 5)
	require.Len(t, peers, 1, "slot without IP must be skipped")
	require.Equal(t, 0, peers[0].SlotID)
}

func TestPeersExcluding_OrderedBySlotID(t *testing.T) {
	// Insert slots out of order; peersExcluding must still produce a
	// slot-id-sorted list (cluster join templates depend on stable order).
	s := &State{Slots: []SlotState{
		{SlotID: 2, ServerName: "g-2-1", PrivateIP: "10.0.0.12", Generation: 1},
		{SlotID: 0, ServerName: "g-0-1", PrivateIP: "10.0.0.10", Generation: 1},
		{SlotID: 1, ServerName: "g-1-1", PrivateIP: "10.0.0.11", Generation: 1},
	}}
	peers := peersExcluding(s, -1)
	require.Equal(t, []int{0, 1, 2}, []int{peers[0].SlotID, peers[1].SlotID, peers[2].SlotID})
}

func TestPeersExcluding_SingleSlotGroup(t *testing.T) {
	s := &State{Slots: []SlotState{
		{SlotID: 0, ServerName: "g-0-1", PrivateIP: "10.0.0.10", Generation: 1},
	}}
	require.Empty(t, peersExcluding(s, 0), "a single-slot group has no peers")
}
