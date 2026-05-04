package reconciler_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

func TestServerName_Format(t *testing.T) {
	require.Equal(t, "consul-0-1", reconciler.ServerName("consul", 0, 1))
	require.Equal(t, "g-12-345", reconciler.ServerName("g", 12, 345))
}

// The schema validator caps group names at 63-1-3-1-6 = 52 chars so the
// deterministic name fits RFC 1123. This test pins the budget by
// constructing a worst-case name and confirming it stays at 63.
func TestServerName_FitsRFC1123_AtBudgetBoundary(t *testing.T) {
	// 52-char group + 3-digit slot + 6-digit generation
	groupAtBudget := strings.Repeat("a", 52)
	name := reconciler.ServerName(groupAtBudget, 999, 999999)
	require.Equal(t, 63, len(name), "worst-case name must equal 63 chars")
}
