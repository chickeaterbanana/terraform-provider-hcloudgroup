package servergroup_test

import (
	"testing"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/acctest"
)

// TestMain ensures the shared acceptance fixture suite (jump host,
// shared Network, shared SSH key) gets torn down after the package's
// tests finish. Without this, the once-allocated jump host would stay
// running after the test binary exits.
func TestMain(m *testing.M) {
	acctest.AccTestMain(m)
}
