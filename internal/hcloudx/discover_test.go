package hcloudx_test

import (
	"strconv"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

func mkServer(id int64, name string, labels map[string]string) *hcloud.Server {
	return &hcloud.Server{ID: id, Name: name, Labels: labels}
}

func validLabels(slot, gen int, complete string, hash string) map[string]string {
	return map[string]string{
		hcloudx.LabelManagedBy:   hcloudx.ManagedByValue,
		hcloudx.LabelGroup:       "g",
		hcloudx.LabelSlot:        strconv.Itoa(slot),
		hcloudx.LabelGeneration:  strconv.Itoa(gen),
		hcloudx.LabelComplete:    complete,
		hcloudx.LabelReplaceHash: hash,
	}
}

func TestObserve_ParsesLabels(t *testing.T) {
	s := mkServer(1, "g-0-1", validLabels(0, 1, "true", "abc123"))
	obs, ok := hcloudx.Observe(s)
	require.True(t, ok)
	require.Equal(t, 0, obs.SlotID)
	require.Equal(t, 1, obs.Generation)
	require.True(t, obs.Complete)
	require.Equal(t, "abc123", obs.HashPrefix)
	require.Same(t, s, obs.Server)
}

func TestObserve_MissingSlotLabel_NotOK(t *testing.T) {
	labels := validLabels(0, 1, "true", "h")
	delete(labels, hcloudx.LabelSlot)
	_, ok := hcloudx.Observe(mkServer(1, "x", labels))
	require.False(t, ok, "missing slot must yield ok=false")
}

func TestObserve_NonNumericSlotLabel_NotOK(t *testing.T) {
	labels := validLabels(0, 1, "true", "h")
	labels[hcloudx.LabelSlot] = "not-a-number"
	_, ok := hcloudx.Observe(mkServer(1, "x", labels))
	require.False(t, ok)
}

func TestObserve_MissingGenerationLabel_NotOK(t *testing.T) {
	labels := validLabels(0, 1, "true", "h")
	delete(labels, hcloudx.LabelGeneration)
	_, ok := hcloudx.Observe(mkServer(1, "x", labels))
	require.False(t, ok)
}

func TestObserve_NilServer_NotOK(t *testing.T) {
	_, ok := hcloudx.Observe(nil)
	require.False(t, ok)
}

func TestObserve_CompleteFalseWhenAnythingButTrue(t *testing.T) {
	for _, v := range []string{"false", "", "True", "yes"} {
		labels := validLabels(0, 1, v, "h")
		obs, ok := hcloudx.Observe(mkServer(1, "x", labels))
		require.True(t, ok)
		require.False(t, obs.Complete, "complete must be true only when label value is exactly 'true', got %q", v)
	}
}

func TestPartitionBySlot_GroupsAndSortsDescending(t *testing.T) {
	servers := []*hcloud.Server{
		mkServer(1, "g-0-1", validLabels(0, 1, "true", "h1")),
		mkServer(2, "g-0-3", validLabels(0, 3, "false", "h3")),
		mkServer(3, "g-0-2", validLabels(0, 2, "true", "h2")),
		mkServer(4, "g-1-1", validLabels(1, 1, "true", "h1")),
	}
	out := hcloudx.PartitionBySlot(servers)
	require.Len(t, out, 2)

	require.Equal(t, []int{3, 2, 1}, []int{
		out[0][0].Generation, out[0][1].Generation, out[0][2].Generation,
	}, "slot 0 must be sorted highest gen first")
	require.Len(t, out[1], 1)
	require.Equal(t, 1, out[1][0].Generation)
}

func TestPartitionBySlot_DropsServersWithoutLabels(t *testing.T) {
	servers := []*hcloud.Server{
		mkServer(1, "foreign", map[string]string{"foo": "bar"}),
		mkServer(2, "g-0-1", validLabels(0, 1, "true", "h")),
	}
	out := hcloudx.PartitionBySlot(servers)
	require.Len(t, out, 1)
	require.Len(t, out[0], 1)
}

func TestPickCanonical_PrefersHighestCompleteGeneration(t *testing.T) {
	// Slot has an orphan at gen=5 (incomplete) above a canonical at gen=3.
	// PickCanonical must skip the orphan.
	obs := []hcloudx.Observation{
		{Server: mkServer(5, "n", nil), Generation: 5, Complete: false},
		{Server: mkServer(3, "n", nil), Generation: 3, Complete: true},
		{Server: mkServer(2, "n", nil), Generation: 2, Complete: true},
	}
	got, ok := hcloudx.PickCanonical(obs)
	require.True(t, ok)
	require.Equal(t, 3, got.Generation)
}

func TestPickCanonical_ReturnsFalse_WhenNoComplete(t *testing.T) {
	obs := []hcloudx.Observation{
		{Generation: 5, Complete: false},
		{Generation: 4, Complete: false},
	}
	_, ok := hcloudx.PickCanonical(obs)
	require.False(t, ok, "all incomplete → no canonical")
}

func TestPickCanonical_EmptyList(t *testing.T) {
	_, ok := hcloudx.PickCanonical(nil)
	require.False(t, ok)
}

func TestMaxObservedGeneration_IncludesOrphans(t *testing.T) {
	// Critical for crash recovery (README §5.4): generation source-of-truth
	// must be max over canonical AND orphan, not just canonical.
	obs := []hcloudx.Observation{
		{Generation: 7, Complete: false}, // orphan
		{Generation: 3, Complete: true},  // canonical
	}
	require.Equal(t, 7, hcloudx.MaxObservedGeneration(obs),
		"new generation must derive from max(observed) so a destroyed orphan doesn't clash with a fresh create")
}

func TestMaxObservedGeneration_EmptyIsZero(t *testing.T) {
	require.Equal(t, 0, hcloudx.MaxObservedGeneration(nil),
		"empty input must produce 0 so first-create lands at gen=1")
}
