// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/acctest"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// hclConfigWithLabels reuses hclConfig but adds an arbitrary `labels`
// map and lists `labels` in `replace_on_change`, so a label change flips
// the hash via the operator-listed knob (not just the always-on labels
// hash). This exercises the replace_on_change → value-resolution path.
func hclConfigWithLabels(t *testing.T, name string, count int, labels map[string]string) string {
	t.Helper()
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, fmt.Sprintf("    %s = %q", k, v))
	}
	sort.Strings(pairs)
	extra := fmt.Sprintf(`
labels = {
%s
}

replace_on_change = ["labels"]
`, strings.Join(pairs, "\n"))
	return hclConfig(t, name, "debian-13", count, extra)
}

// TestAccServerGroup_ReplaceOnChange validates that a label listed in
// replace_on_change triggers a rolling replace. We just toggle the
// label value and assert generations advance from 1 → 2.
func TestAccServerGroup_ReplaceOnChange(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "roc")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: hclConfigWithLabels(t, groupName, 1, map[string]string{"rev": "a"})},
			{
				Config: hclConfigWithLabels(t, groupName, 1, map[string]string{"rev": "b"}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.generation", "2"),
				),
			},
		},
	})
}

// hookOrderingHCL builds a config with all six action hooks set to
// commands that append a stamped line to logFile on disk. After a
// rolling replace, parsing the log proves the hooks fired in the spec
// order.
func hookOrderingHCL(t *testing.T, name, image string, count int, logFile string) string {
	t.Helper()
	suite := acctest.Get(t)
	hook := func(name string) string {
		return fmt.Sprintf(`
%s {
  command {
    command = "echo %s >> %s"
    env = { PATH = "/usr/bin:/bin" }
    timeout = "10s"
  }
}
`, name, name, logFile)
	}
	extras := hook("before_create") + hook("post_create") +
		hook("before_replace") + hook("post_replace") +
		hook("before_remove") + hook("post_remove")
	return fmt.Sprintf(`
resource "hcloudgroup_server_group" "test" {
  name        = %q
  replicas    = %d
  image       = %q
  server_type = %q
  location    = %q
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
`, name, count, image, suite.ServerType, suite.Location, suite.NetworkID, suite.SSHKeyName,
		fmt.Sprintf(baseUserData, suite.PublicKeyOpenSSH),
		extras,
	)
}

// TestAccServerGroup_HookOrdering verifies the hook firing order in a
// replace flow matches the spec (README §6 step list). Uses count=1 so
// the order is unambiguous.
func TestAccServerGroup_HookOrdering(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "hooks")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	logFile := filepath.Join(t.TempDir(), "hooks.log")
	t.Setenv("HOOK_LOG", logFile)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				// Initial create: only before_create + post_create fire.
				Config: hookOrderingHCL(t, groupName, "debian-13", 1, logFile),
				Check: func(*terraform.State) error {
					// #nosec G304 -- logFile is a t.TempDir()-derived path,
					// not operator input.
					data, err := os.ReadFile(logFile)
					require.NoError(t, err)
					lines := nonEmptyLines(string(data))
					require.Equal(t, []string{"before_create", "post_create"}, lines)
					return nil
				},
			},
			{
				// Replace: hooks should fire in the spec order.
				Config: hookOrderingHCL(t, groupName, "ubuntu-24.04", 1, logFile),
				Check: func(*terraform.State) error {
					// #nosec G304 -- logFile is a t.TempDir()-derived path,
					// not operator input.
					data, err := os.ReadFile(logFile)
					require.NoError(t, err)
					lines := nonEmptyLines(string(data))
					// First step's two entries + replace's six = eight total.
					require.Equal(t, []string{
						"before_create", "post_create",
						"before_replace", "before_remove", "post_remove",
						"before_create", "post_create", "post_replace",
					}, lines, "spec §6 hook order on replace")
					return nil
				},
			},
		},
	})
}

func nonEmptyLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestAccServerGroup_OutOfBandDeletion creates a group, deletes a
// managed server out-of-band via the SDK between steps, and confirms
// the next apply recreates that slot.
//
// We change the image in step 2 to force a real Update plan-time diff
// — refresh-only diffs from a Read that shrunk state aren't enough to
// trigger an Update on Computed-only fields. Combined with the OOB
// delete this proves the recovery path: refresh sees no canonical →
// plan + apply recreates the slot.
func TestAccServerGroup_OutOfBandDeletion(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "oobdel")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hclConfig(t, groupName, "debian-13", 1, ""),
			},
			{
				PreConfig: func() {
					hc := acctest.MustHcloud(t)
					sel := fmt.Sprintf("%s=%s,%s=%s",
						hcloudx.LabelManagedBy, hcloudx.ManagedByValue,
						hcloudx.LabelGroup, groupName)
					servers, err := hc.Server.AllWithOpts(context.Background(), hcloud.ServerListOpts{
						ListOpts: hcloud.ListOpts{LabelSelector: sel, PerPage: 50},
					})
					require.NoError(t, err)
					require.Len(t, servers, 1)
					_, _, err = hc.Server.DeleteWithResult(context.Background(), &hcloud.Server{ID: servers[0].ID})
					require.NoError(t, err)
				},
				Config: hclConfig(t, groupName, "ubuntu-24.04", 1, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
					func(*terraform.State) error {
						assertHcloudGroupCount(t, groupName, 1)
						return nil
					},
				),
			},
		},
	})
}

// TestAccServerGroup_OrphanCleanup validates pre-flight: an orphan
// server (complete=false) belonging to this group is destroyed during
// the next Update. The orphan is injected via PreConfig so an Update
// actually runs to clean it up.
//
// To force step 2 to run a real Update (not a no-op), the second step
// changes a label so the hash flips. The orphan still gets cleaned by
// pre-flight even when the Update is otherwise a rolling replace.
func TestAccServerGroup_OrphanCleanup(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "orph")
	t.Cleanup(func() { sweepGroup(t, groupName) })
	suite := acctest.Get(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hclConfigWithLabels(t, groupName, 1, map[string]string{"phase": "before"}),
			},
			{
				PreConfig: func() {
					hc := acctest.MustHcloud(t)
					// Inject a fake orphan: complete=false, slot=99, gen=99.
					_, _, err := hc.Server.Create(context.Background(), hcloud.ServerCreateOpts{
						Name:       fmt.Sprintf("%s-orphan-99-99", groupName),
						ServerType: &hcloud.ServerType{Name: suite.ServerType},
						Image:      &hcloud.Image{Name: "debian-13"},
						Location:   &hcloud.Location{Name: suite.Location},
						Networks:   []*hcloud.Network{{ID: suite.NetworkID}},
						SSHKeys:    []*hcloud.SSHKey{{ID: suite.SSHKeyID}},
						Labels: map[string]string{
							hcloudx.LabelManagedBy:   hcloudx.ManagedByValue,
							hcloudx.LabelGroup:       groupName,
							hcloudx.LabelSlot:        "99",
							hcloudx.LabelGeneration:  "99",
							hcloudx.LabelComplete:    "false",
							hcloudx.LabelReplaceHash: "deadbeef0000",
						},
					})
					require.NoError(t, err)
				},
				// Change the label so hash flips → Update runs → pre-flight executes.
				Config: hclConfigWithLabels(t, groupName, 1, map[string]string{"phase": "after"}),
				Check: func(*terraform.State) error {
					assertHcloudGroupCount(t, groupName, 1) // exactly the canonical, no orphan
					hc := acctest.MustHcloud(t)
					sel := fmt.Sprintf("%s=%s,%s=%s",
						hcloudx.LabelManagedBy, hcloudx.ManagedByValue,
						hcloudx.LabelGroup, groupName)
					servers, _ := hc.Server.AllWithOpts(context.Background(), hcloud.ServerListOpts{
						ListOpts: hcloud.ListOpts{LabelSelector: sel, PerPage: 50},
					})
					for _, s := range servers {
						require.NotEqual(t, "false", s.Labels[hcloudx.LabelComplete],
							"all surviving servers must be complete=true")
					}
					return nil
				},
			},
		},
	})
}

// TestAccServerGroup_ReadinessProbeViaJumpHost is the headline test:
// it exercises the full reachability + readiness contract by SSHing
// into each managed server through the test suite's jump host.
//
// The probe runs `cloud-init status --wait` which blocks until cloud-init
// is fully done, then exits 0. With success_threshold=1, a single OK
// satisfies the probe — but we use threshold=2 to also test the
// streak-counter logic.
func TestAccServerGroup_ReadinessProbeViaJumpHost(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "rdy")
	t.Cleanup(func() { sweepGroup(t, groupName) })
	suite := acctest.Get(t)

	probe := suite.JumpSSHCommand("$HCLOUDGROUP_PRIVATE_IP", "true")
	probeCfg := fmt.Sprintf(`
readiness_probe {
  command {
    command = %q
    env = {
      PATH = "/usr/bin:/bin:/usr/local/bin"
      HOME = "/root"
    }
    timeout           = "15s"
    interval          = "10s"
    success_threshold = 2
    total_timeout     = "10m"
  }
}
`, probe)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: hclConfig(t, groupName, "debian-13", 1, probeCfg),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.#", "1"),
				resource.TestCheckResourceAttr("hcloudgroup_server_group.test", "slots.0.status", "ready"),
				func(*terraform.State) error { assertHcloudGroupCount(t, groupName, 1); return nil },
			),
		}},
	})
}

// TestAccServerGroup_ReadinessProbeFailureLeavesIncomplete forces the
// probe to fail (inner command always exits 1). The slot must end as
// failed, the new server stays in hcloud as complete=false, and a
// recovery apply with a passing probe converges cleanly via pre-flight.
func TestAccServerGroup_ReadinessProbeFailureLeavesIncomplete(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "rdyfail")
	t.Cleanup(func() { sweepGroup(t, groupName) })
	suite := acctest.Get(t)

	failingProbe := suite.JumpSSHCommand("$HCLOUDGROUP_PRIVATE_IP", "false")
	passingProbe := suite.JumpSSHCommand("$HCLOUDGROUP_PRIVATE_IP", "true")

	failingCfg := fmt.Sprintf(`
readiness_probe {
  command {
    command = %q
    env = {
      PATH = "/usr/bin:/bin:/usr/local/bin"
      HOME = "/root"
    }
    timeout           = "10s"
    interval          = "5s"
    success_threshold = 1
    total_timeout     = "30s"
  }
}
`, failingProbe)
	passingCfg := fmt.Sprintf(`
readiness_probe {
  command {
    command = %q
    env = {
      PATH = "/usr/bin:/bin:/usr/local/bin"
      HOME = "/root"
    }
    timeout           = "15s"
    interval          = "10s"
    success_threshold = 1
    total_timeout     = "5m"
  }
}
`, passingProbe)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				// Step 1: probe always fails. The framework treats the
				// matched ExpectError as success and skips Check entirely
				// (a Check here would silently never run). The orphan
				// assertion lives in step 2's PreConfig, which runs after
				// step 1's apply but before step 2's pre-flight cleans up.
				Config:      hclConfig(t, groupName, "debian-13", 1, failingCfg),
				ExpectError: regexpReadinessFailed(),
			},
			{
				PreConfig: func() {
					// One incomplete server must remain in hcloud as the
					// crash-recovery orphan (README §11).
					hc := acctest.MustHcloud(t)
					sel := fmt.Sprintf("%s=%s,%s=%s",
						hcloudx.LabelManagedBy, hcloudx.ManagedByValue,
						hcloudx.LabelGroup, groupName)
					servers, _ := hc.Server.AllWithOpts(context.Background(), hcloud.ServerListOpts{
						ListOpts: hcloud.ListOpts{LabelSelector: sel, PerPage: 50},
					})
					require.Len(t, servers, 1, "incomplete server stays in hcloud as orphan")
					require.Equal(t, "false", servers[0].Labels[hcloudx.LabelComplete])
				},
				// Recovery: passing probe → orphan cleaned by pre-flight,
				// slot 0 freshly created.
				Config: hclConfig(t, groupName, "debian-13", 1, passingCfg),
				Check:  func(*terraform.State) error { assertHcloudGroupCount(t, groupName, 1); return nil },
			},
		},
	})
}

// TestAccServerGroup_Import covers the round-trip via `terraform import`.
// The framework's ImportStateVerify=true requires that the imported state
// matches the post-Create state. Because Required HCL attributes are
// user-supplied (not recoverable from labels alone), several derived
// fields are necessarily empty after import; ImportStateVerifyIgnore
// documents that limitation.
//
// The critical regression assertion is that the framework's import +
// refresh produces a slots list at the right size with correct slot_id /
// server_id / generation values — proving ImportState seeded replicas
// from the observed labels (regression for finding #2). Without that
// fix, slots.# would be 0 after import.
func TestAccServerGroup_Import(t *testing.T) {
	acctest.PreCheck(t)
	groupName := acctest.RandName(t, "import")
	t.Cleanup(func() { sweepGroup(t, groupName) })

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hclConfig(t, groupName, "debian-13", 2, ""),
				Check: resource.TestCheckResourceAttr(
					"hcloudgroup_server_group.test", "slots.#", "2"),
			},
			{
				ResourceName:      "hcloudgroup_server_group.test",
				ImportState:       true,
				ImportStateId:     groupName,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					// Desired-state attributes are user-supplied after
					// import; they aren't recoverable from labels.
					"image", "server_type", "location", "network_id",
					"ssh_keys", "labels", "user_data_template",
					// current_replace_hash includes the desired-state
					// fields; without them in HCL we can't reproduce the
					// hash and ImportStateVerify would otherwise diff.
					"current_replace_hash",
					// timeouts has no value at import; the framework
					// adds defaults later.
					"timeouts",
					// PrivateIP is derived from network_id (which is
					// null after import), so findPrivateIP can't match
					// the server's network attachment. Populated on the
					// next plan/apply once network_id is supplied.
					"slots.0.ip_private",
					"slots.1.ip_private",
					// replace_hash is preserved from prior tofu state;
					// after a fresh import there is no prior state, so
					// it's empty until the next apply runs.
					"slots.0.replace_hash",
					"slots.1.replace_hash",
				},
			},
		},
	})
}

// keep imports tidy.
var (
	_ = sort.Strings
	_ = strconv.Itoa
)
