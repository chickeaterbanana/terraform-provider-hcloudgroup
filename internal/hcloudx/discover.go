package hcloudx

import (
	"sort"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Observation is the result of inspecting a single hcloud server's labels.
// It provides cheap access to slot/generation/complete without re-parsing
// labels every time the reconciler asks.
type Observation struct {
	Server     *hcloud.Server
	SlotID     int
	Generation int
	Complete   bool
	HashPrefix string
}

// Observe parses provider-internal labels off a server. Servers missing
// required labels (slot, generation) are returned with valid=false; callers
// should treat these as foreign and ignore them - the label selector
// guarantees managed-by + group, but not the rest.
func Observe(s *hcloud.Server) (Observation, bool) {
	if s == nil || s.Labels == nil {
		return Observation{}, false
	}
	slotStr, ok := s.Labels[LabelSlot]
	if !ok {
		return Observation{}, false
	}
	slot, err := strconv.Atoi(slotStr)
	if err != nil {
		return Observation{}, false
	}
	genStr, ok := s.Labels[LabelGeneration]
	if !ok {
		return Observation{}, false
	}
	gen, err := strconv.Atoi(genStr)
	if err != nil {
		return Observation{}, false
	}
	return Observation{
		Server:     s,
		SlotID:     slot,
		Generation: gen,
		Complete:   s.Labels[LabelComplete] == "true",
		HashPrefix: s.Labels[LabelReplaceHash],
	}, true
}

// PartitionBySlot groups observations by slot id, sorting each slot's list
// by generation descending so callers can pick canonical at index 0.
func PartitionBySlot(servers []*hcloud.Server) map[int][]Observation {
	out := map[int][]Observation{}
	for _, s := range servers {
		obs, ok := Observe(s)
		if !ok {
			continue
		}
		out[obs.SlotID] = append(out[obs.SlotID], obs)
	}
	for slot := range out {
		sort.Slice(out[slot], func(i, j int) bool {
			return out[slot][i].Generation > out[slot][j].Generation
		})
	}
	return out
}

// PickCanonical returns the highest-generation Observation for a slot whose
// complete flag is true. Returns ok=false if no such server exists - the
// slot is then treated as missing and will be (re)created on the next
// apply.
func PickCanonical(slotObs []Observation) (Observation, bool) {
	for _, o := range slotObs {
		if o.Complete {
			return o, true
		}
	}
	return Observation{}, false
}

// MaxObservedGeneration returns the largest generation seen across both
// canonical and orphan servers for the slot. The new generation for a
// (re)create is this value + 1, ensuring the deterministic server name
// never collides with a soon-to-be-destroyed orphan during a brief overlap
// window (spec section 5.4).
//
// When no observation exists for the slot, the result is 0 - so the
// caller's MaxObservedGeneration+1 produces the correct first-generation
// value of 1.
func MaxObservedGeneration(slotObs []Observation) int {
	max := 0
	for _, o := range slotObs {
		if o.Generation > max {
			max = o.Generation
		}
	}
	return max
}
