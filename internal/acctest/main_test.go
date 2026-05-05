// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package acctest_test

import (
	"context"
	"os"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/acctest"
)

// TestMain runs once per `go test` invocation in this package. We don't
// have any TestXxx funcs that drive resource.Test in this package
// directly; the per-resource _acc_test.go files do that. But we still
// hook TestMain to run a final sweep at the end of the suite so any
// leftover provider-managed servers from a panic are destroyed.
func TestMain(m *testing.M) {
	code := m.Run()

	// Best-effort suite-level teardown. Tests that called acctest.Get(t)
	// are responsible for fixture lifecycle via t.Cleanup; this is a
	// final safety net.
	if os.Getenv(acctest.EnvAcceptance) != "" && os.Getenv(acctest.EnvHcloudToken) != "" {
		hc := hcloud.NewClient(hcloud.WithToken(os.Getenv(acctest.EnvHcloudToken)))
		_ = acctest.SweepLeftoverResources(context.Background(), hc)
		acctest.Teardown()
	}

	os.Exit(code)
}
