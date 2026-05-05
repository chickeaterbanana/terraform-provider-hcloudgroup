// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

// baseModel is the minimal known-only resourceModel that produces a
// stable hash. Tests mutate one attribute at a time to verify the
// expected behavior of the hash inputs / unknown detection.
func baseModel() resourceModel {
	return resourceModel{
		Name:             types.StringValue("consul"),
		Count:            types.Int64Value(3),
		Image:            types.StringValue("debian-13"),
		ServerType:       types.StringValue("cx22"),
		Location:         types.StringValue("fsn1"),
		NetworkID:        types.Int64Value(42),
		SSHKeys:          mkStringList("alice"),
		Labels:           mkStringMap(map[string]string{"env": "prod"}),
		UserDataTemplate: types.StringValue("#cloud-config\n"),
		ReplaceOnChange:  mkStringSet("user_data_template"),
	}
}

// TestHasUnknownHashInputs_KnownModel guards Finding 6: the plan
// modifier must produce a known planned hash whenever every hash input
// is known. A false positive here would silently regress to the noisy
// "(known after apply)" diff this modifier is meant to remove.
func TestHasUnknownHashInputs_KnownModel(t *testing.T) {
	require.False(t, hasUnknownHashInputs(baseModel()))
}

// TestHasUnknownHashInputs_FlagsEachInput guarantees the unknown check
// catches each contributing attribute. Adding a new hash-input
// attribute to the schema without updating hasUnknownHashInputs would
// make the modifier produce a spurious concrete hash from an unknown
// value — fail loud at test time instead.
func TestHasUnknownHashInputs_FlagsEachInput(t *testing.T) {
	mutators := []struct {
		name string
		mut  func(*resourceModel)
	}{
		{"image", func(m *resourceModel) { m.Image = types.StringUnknown() }},
		{"server_type", func(m *resourceModel) { m.ServerType = types.StringUnknown() }},
		{"location", func(m *resourceModel) { m.Location = types.StringUnknown() }},
		{"network_id", func(m *resourceModel) { m.NetworkID = types.Int64Unknown() }},
		{"user_data_template", func(m *resourceModel) { m.UserDataTemplate = types.StringUnknown() }},
		{"ssh_keys", func(m *resourceModel) { m.SSHKeys = types.ListUnknown(types.StringType) }},
		{"labels", func(m *resourceModel) { m.Labels = types.MapUnknown(types.StringType) }},
		{"replace_on_change", func(m *resourceModel) { m.ReplaceOnChange = types.SetUnknown(types.StringType) }},
	}
	for _, tc := range mutators {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel()
			tc.mut(&m)
			require.True(t, hasUnknownHashInputs(m), "unknown %s must be detected", tc.name)
		})
	}
}

// TestModelHashInputs_DeterministicAcrossCalls is the load-bearing
// property: same inputs → same hash. The plan modifier and modelToGroup
// both call modelHashInputs; if they ever produced different hashes for
// the same model, planned current_replace_hash would not match the
// applied one.
func TestModelHashInputs_DeterministicAcrossCalls(t *testing.T) {
	m := baseModel()

	hi1, _, _, d1 := modelHashInputs(context.Background(), m)
	require.False(t, d1.HasError())
	hi2, _, _, d2 := modelHashInputs(context.Background(), m)
	require.False(t, d2.HasError())

	full1, _ := hi1.Hash()
	full2, _ := hi2.Hash()
	require.Equal(t, full1, full2, "modelHashInputs must be deterministic")
}

// TestModelHashInputs_FlipsOnImageChange asserts the hash actually
// detects an input change. A no-op modifier would still pass
// TestModelHashInputs_DeterministicAcrossCalls; this catches that.
func TestModelHashInputs_FlipsOnImageChange(t *testing.T) {
	a := baseModel()
	b := baseModel()
	b.Image = types.StringValue("debian-12")

	hiA, _, _, _ := modelHashInputs(context.Background(), a)
	hiB, _, _, _ := modelHashInputs(context.Background(), b)
	fullA, _ := hiA.Hash()
	fullB, _ := hiB.Hash()
	require.NotEqual(t, fullA, fullB, "image change must flip the hash")
}
