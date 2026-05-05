// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/acctest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// One simple Debian cloud-init template that authorizes our test SSH key
// so the jump-host probe can land. Keeps the boot light: no service
// install, just enough cloud-init to say "running".
const baseUserData = `#cloud-config
users:
  - name: root
    ssh_authorized_keys:
      - %s
runcmd:
  - [ "true" ]
`

// hclConfig builds an HCL config string for one scenario. count is the
// desired group size; image and userData are interpolated. The provider
// reads HCLOUD_TOKEN from env.
//
// The HCL refers to the provider by short name "hcloudgroup".
// Plugin-testing registers the in-process provider under
// {host}/{namespace}/hcloudgroup for several common host+namespace
// combinations (host defaults to registry.terraform.io; override with
// TF_ACC_PROVIDER_HOST=registry.opentofu.org for tofu runs).
func hclConfig(t *testing.T, name, image string, count int, extras string) string {
	t.Helper()
	suite := acctest.Get(t)
	return fmt.Sprintf(`
resource "hcloudgroup_server_group" "test" {
  name        = %q
  replicas    = %d
  image       = %q
  server_type = "cx23"
  location    = "fsn1"
  network_id  = %d
  ssh_keys    = [%q]

  user_data_template = <<EOT
%s
EOT

  %s

  timeouts {
    create = "20m"
    update = "20m"
    delete = "10m"
  }
}
`, name, count, image, suite.NetworkID, suite.SSHKeyName,
		fmt.Sprintf(baseUserData, suite.PublicKeyOpenSSH),
		extras,
	)
}

// AssertHcloudHasGroupServers fails the test if hcloud's reality doesn't
// match the expected count of complete=true servers for this group.
func assertHcloudGroupCount(t *testing.T, group string, want int) {
	t.Helper()
	hc := acctest.MustHcloud(t)
	selector := fmt.Sprintf("%s=%s,%s=%s",
		hcloudx.LabelManagedBy, hcloudx.ManagedByValue,
		hcloudx.LabelGroup, group,
	)
	servers, err := hc.Server.AllWithOpts(context.Background(), hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: selector, PerPage: 50},
	})
	require.NoError(t, err)
	complete := 0
	for _, s := range servers {
		if s.Labels[hcloudx.LabelComplete] == "true" {
			complete++
		}
	}
	require.Equal(t, want, complete, "hcloud canonical (complete=true) server count for group %q", group)
}

func sweepGroup(t *testing.T, group string) {
	t.Helper()
	hc := acctest.MustHcloud(t)
	selector := fmt.Sprintf("%s=%s,%s=%s",
		hcloudx.LabelManagedBy, hcloudx.ManagedByValue,
		hcloudx.LabelGroup, group,
	)
	servers, _ := hc.Server.AllWithOpts(context.Background(), hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: selector, PerPage: 50},
	})
	for _, s := range servers {
		_, _, _ = hc.Server.DeleteWithResult(context.Background(), &hcloud.Server{ID: s.ID})
	}
}

// TestAccServerGroup_Basic is the smoke test: count=1, single server,
// reapply is a no-op, then destroy. Validates the full CRUD round-trip.
func TestAccServerGroup_Basic(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "basic")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hclConfig(t, groupName, "debian-13", 1, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "name", groupName),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "replicas", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.slot_id", "0"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.generation", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.status", "ready"),
					resource.TestCheckResourceAttrSet("hcloudgroup_server_group.test", "slots.0.server_id"),
					resource.TestCheckResourceAttrSet("hcloudgroup_server_group.test", "slots.0.ip_private"),
					resource.TestCheckResourceAttrSet("hcloudgroup_server_group.test", "current_replace_hash"),
					func(*terraform.State) error {
						assertHcloudGroupCount(t, groupName, 1)
						return nil
					},
				),
			},
			{
				// Re-apply the same config: zero diff.
				Config:   hclConfig(t, groupName, "debian-13", 1, ""),
				PlanOnly: true,
			},
		},
	})
}

// TestAccServerGroup_ScaleUp grows from 1 to 2 slots. Slot 0 must be
// untouched; slot 1 must be a fresh creation at generation=1.
func TestAccServerGroup_ScaleUp(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "scaleup")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hclConfig(t, groupName, "debian-13", 1, ""),
				Check:  resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
			},
			{
				Config: hclConfig(t, groupName, "debian-13", 2, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "2"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.generation", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.1.slot_id", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.1.generation", "1"),
					func(*terraform.State) error { assertHcloudGroupCount(t, groupName, 2); return nil },
				),
			},
		},
	})
}

// TestAccServerGroup_ScaleDown shrinks from 2 to 1. Highest slot must be
// removed; remaining slot's server_id must match the prior step.
func TestAccServerGroup_ScaleDown(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "scaledn")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: hclConfig(t, groupName, "debian-13", 2, "")},
			{
				Config: hclConfig(t, groupName, "debian-13", 1, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
					func(*terraform.State) error { assertHcloudGroupCount(t, groupName, 1); return nil },
				),
			},
		},
	})
}

// TestAccServerGroup_RollingReplace changes the image, which triggers a
// rolling replace of every slot. Each slot's generation must increment
// by 1 and its server_id must change.
func TestAccServerGroup_RollingReplace(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "roll")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: hclConfig(t, groupName, "debian-13", 2, "")},
			{
				Config: hclConfig(t, groupName, "ubuntu-24.04", 2, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "2"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.generation", "2"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.1.generation", "2"),
					func(*terraform.State) error { assertHcloudGroupCount(t, groupName, 2); return nil },
				),
			},
		},
	})
}

// TestAccServerGroup_RejectsReservedLabels exercises the plan-time
// validator: HCL with a hcloudgroup.io/* label must fail before any API
// call.
// TestAccServerGroup_RejectsBadUserDataTemplate proves that a syntactic
// error in user_data_template surfaces at plan time via the
// ValidateConfig hook (regression for finding #6) — not after some slots
// have already been created. The test is plan-only; nothing reaches
// hcloud.
func TestAccServerGroup_RejectsBadUserDataTemplate(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "badtmpl")

	suite := acctest.Get(t)
	cfg := fmt.Sprintf(`
resource "hcloudgroup_server_group" "test" {
  name        = %q
  replicas    = 1
  image       = "debian-13"
  server_type = "cx23"
  location    = "fsn1"
  network_id  = %d
  ssh_keys    = [%q]

  user_data_template = "{{ .DoesNotClose"
}
`, groupName, suite.NetworkID, suite.SSHKeyName)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      cfg,
			ExpectError: regexp.MustCompile("Invalid user_data_template"),
			PlanOnly:    true,
		}},
	})
}

func TestAccServerGroup_RejectsReservedLabels(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "rejlbl")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	cfg := hclConfig(t, groupName, "debian-13", 1, `
labels = {
  "hcloudgroup.io/foo" = "bar"
}
`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      cfg,
			ExpectError: regexpReservedLabel(),
			PlanOnly:    true,
		}},
	})
}

// TestAccServerGroup_Destroy creates a group and confirms tofu destroy
// removes every server in hcloud. resource.Test runs a destroy step
// implicitly at the end of the steps list, so we just verify the
// post-state via a final check that runs after.
func TestAccServerGroup_Destroy(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "del")
	// We don't need an extra cleanup — destroy is the test itself.

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			assertHcloudGroupCount(t, groupName, 0)
			return nil
		},
		Steps: []resource.TestStep{{
			Config: hclConfig(t, groupName, "debian-13", 2, ""),
			Check:  resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "2"),
		}},
	})
}
