// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler

import (
	"sort"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// peersExcluding builds the peer list for a slot's lifecycle hook or
// user_data render. It walks the current in-memory State and includes
// every slot *other than* excludedSlot whose entry has a non-empty
// PrivateIP (i.e. the server is observable).
//
// During initial creation slot K's peers are slots 0..K-1; during a
// replace at slot K, peers are every other slot's current canonical state
// - same code path, different starting state. The reconciler updates
// State as it goes, so the peer list reflects the most recent reality.
func peersExcluding(s *State, excludedSlot int) []slotctx.Peer {
	out := make([]slotctx.Peer, 0, len(s.Slots))
	for _, sl := range s.Slots {
		if sl.SlotID == excludedSlot {
			continue
		}
		if sl.PrivateIP == "" {
			continue
		}
		out = append(out, slotctx.Peer{
			SlotID:     sl.SlotID,
			ServerName: sl.ServerName,
			PrivateIP:  sl.PrivateIP,
			Generation: sl.Generation,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}
