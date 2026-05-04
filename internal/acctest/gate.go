package acctest

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// EnvHcloudToken is the env var the provider's Configure() reads (in
// addition to the HCL hcloud_token attribute). The acceptance harness
// requires it.
const EnvHcloudToken = "HCLOUD_TOKEN"

// EnvAcceptance is the upstream framework's acceptance-test gate.
const EnvAcceptance = "TF_ACC"

var (
	gateOnce sync.Once
	gateMsg  string
	gateOK   bool
)

// PreCheck is the standard PreCheck function passed to resource.Test. It
// skips the test unless TF_ACC=1 and HCLOUD_TOKEN is set, with one
// stable message used across the suite.
func PreCheck(t *testing.T) {
	t.Helper()
	gateOnce.Do(func() {
		acc := os.Getenv(EnvAcceptance)
		token := os.Getenv(EnvHcloudToken)
		switch {
		case acc == "":
			gateMsg = fmt.Sprintf("acceptance tests skipped: set %s=1 to run", EnvAcceptance)
		case token == "":
			gateMsg = fmt.Sprintf("acceptance tests skipped: %s is empty (real Hetzner credentials required)", EnvHcloudToken)
		default:
			gateOK = true
		}
	})
	if !gateOK {
		t.Skip(gateMsg)
	}
}
