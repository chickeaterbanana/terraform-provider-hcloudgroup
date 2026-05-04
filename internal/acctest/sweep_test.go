package acctest_test

import (
	"context"
	"os"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/acctest"
)

// TestSweep is a manual entry point that wipes every tfacc-* resource
// from the configured Hetzner project. Invoked by `make sweep` and gated
// the same way as the acceptance suite (TF_ACC + HCLOUD_TOKEN). Safe to
// run any time on a sandbox project — the sweeper is filtered to the
// "tfacc-" prefix so unrelated resources stay untouched.
func TestSweep(t *testing.T) {
	if os.Getenv(acctest.EnvAcceptance) == "" || os.Getenv(acctest.EnvHcloudToken) == "" {
		t.Skipf("set %s=1 and %s to run sweeper",
			acctest.EnvAcceptance, acctest.EnvHcloudToken)
	}
	hc := hcloud.NewClient(hcloud.WithToken(os.Getenv(acctest.EnvHcloudToken)))
	if err := acctest.SweepLeftoverResources(context.Background(), hc); err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
}
