// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package hcloudx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitUserAndProvider(t *testing.T) {
	all := map[string]string{
		"env":                       "prod",
		"role":                      "consul",
		"hcloudgroup.io/managed-by": "hcloudgroup-provider",
		"hcloudgroup.io/slot":       "0",
	}
	user, provider := SplitUserAndProvider(all)
	require.Equal(t, map[string]string{"env": "prod", "role": "consul"}, user)
	require.Equal(t, "hcloudgroup-provider", provider["hcloudgroup.io/managed-by"])
	require.Equal(t, "0", provider["hcloudgroup.io/slot"])
}

func TestMergeForCreate_SetsCompleteFalse(t *testing.T) {
	user := map[string]string{"env": "prod"}
	merged := MergeForCreate(user, "consul", 2, 3, "abc123def456")
	require.Equal(t, "false", merged[LabelComplete])
	require.Equal(t, "hcloudgroup-provider", merged[LabelManagedBy])
	require.Equal(t, "consul", merged[LabelGroup])
	require.Equal(t, "2", merged[LabelSlot])
	require.Equal(t, "3", merged[LabelGeneration])
	require.Equal(t, "abc123def456", merged[LabelReplaceHash])
	require.Equal(t, "prod", merged["env"])
}

func TestMergeForCreate_DropsProviderNamespacedUserKeys(t *testing.T) {
	merged := MergeForCreate(map[string]string{
		"env":                       "prod",
		"hcloudgroup.io/managed-by": "operator-attempted-spoof",
	}, "g", 0, 1, "h")
	require.Equal(t, "hcloudgroup-provider", merged[LabelManagedBy])
}

func TestHasNamespace(t *testing.T) {
	require.True(t, HasNamespace("hcloudgroup.io/anything"))
	require.False(t, HasNamespace("env"))
	require.False(t, HasNamespace(""))
}
