// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// importedReplicaCount must reflect the highest slot index seen, +1 (slots
// are zero-based). Without this seeded value, Read iterates 0..0 and
// produces an empty slot list; the next apply duplicates every server
// because the existing complete=true servers aren't classified as orphans.
func TestImportedReplicaCount_FromObservations(t *testing.T) {
	tests := []struct {
		name      string
		observed  map[int][]hcloudx.Observation
		wantCount int
	}{
		{name: "no servers", observed: map[int][]hcloudx.Observation{}, wantCount: 0},
		{name: "single slot 0", observed: map[int][]hcloudx.Observation{0: {{}}}, wantCount: 1},
		{name: "three slots", observed: map[int][]hcloudx.Observation{0: {{}}, 1: {{}}, 2: {{}}}, wantCount: 3},
		{name: "sparse — highest sets the count",
			observed:  map[int][]hcloudx.Observation{0: {{}}, 5: {{}}},
			wantCount: 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantCount, importedReplicaCount(tc.observed))
		})
	}
}
