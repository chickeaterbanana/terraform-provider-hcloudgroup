// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package reconciler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHash_Deterministic_AcrossMapOrder(t *testing.T) {
	a := HashInputs{
		Image: "ubuntu-24.04", ServerType: "cx22",
		UserDataTemplate: "#cloud-config\n", NetworkID: 1, Location: "fsn1",
		SSHKeys: []string{"ops", "alice"},
		Labels:  map[string]string{"env": "prod", "role": "consul"},
		Extras:  map[string]string{"x": "1", "y": "2"},
	}
	b := HashInputs{
		Image: "ubuntu-24.04", ServerType: "cx22",
		UserDataTemplate: "#cloud-config\n", NetworkID: 1, Location: "fsn1",
		// reversed key insertion - relies on sorting in canonicalForm
		SSHKeys: []string{"alice", "ops"},
		Labels:  map[string]string{"role": "consul", "env": "prod"},
		Extras:  map[string]string{"y": "2", "x": "1"},
	}
	fa, pa := a.Hash()
	fb, pb := b.Hash()
	require.Equal(t, fa, fb, "hash must be stable across map iteration order")
	require.Equal(t, pa, pb)
	require.Len(t, pa, HashLabelLength)
}

func TestHash_FlipsOnImageChange(t *testing.T) {
	base := HashInputs{Image: "a", ServerType: "cx22", NetworkID: 1, Location: "fsn1"}
	a, _ := base.Hash()
	base.Image = "b"
	c, _ := base.Hash()
	require.NotEqual(t, a, c)
}

func TestHash_StripsProviderNamespacedLabels(t *testing.T) {
	clean := HashInputs{
		Image: "x", ServerType: "cx22", NetworkID: 1, Location: "fsn1",
		Labels: map[string]string{"env": "prod"},
	}
	dirty := HashInputs{
		Image: "x", ServerType: "cx22", NetworkID: 1, Location: "fsn1",
		Labels: map[string]string{
			"env":                       "prod",
			"hcloudgroup.io/managed-by": "hcloudgroup-provider", // must be ignored
		},
	}
	a, _ := clean.Hash()
	b, _ := dirty.Hash()
	require.Equal(t, a, b, "provider-namespaced labels must not affect the hash")
}

func TestHash_ExtrasContribute(t *testing.T) {
	a := HashInputs{Image: "x", ServerType: "cx22", NetworkID: 1, Location: "fsn1"}
	b := a
	b.Extras = map[string]string{"foo": "bar"}
	ah, _ := a.Hash()
	bh, _ := b.Hash()
	require.NotEqual(t, ah, bh)
}
