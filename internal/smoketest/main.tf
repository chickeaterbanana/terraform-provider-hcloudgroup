# Resources reuse the `tfacc-` prefix and `tfacc.io/fixture` label so
# the acctest sweeper (internal/acctest/fixtures.go) cleans up stranded
# smoke fixtures from a panicked or cancelled run.

terraform {
  required_providers {
    hcloud      = { source = "hetznercloud/hcloud", version = "~> 1.48" }
    hcloudgroup = { source = "chickeaterbanana/hcloudgroup" }
  }
}

variable "suffix" {
  type        = string
  description = "Per-matrix-leg suffix to keep concurrent runs from colliding."
}

# Image is varied across two consecutive `apply` invocations to exercise
# the rolling-replace path (image is in the always-on hash set, so flipping
# it triggers replace_method-driven slot reordering).
# Smoke pins replace_method to the v0.2.0 default (create_before_destroy)
# implicitly by omission. The destroy_before_create path is exercised
# exhaustively in the acceptance suite (see internal/servergroup/
# server_group_advanced_acc_test.go: TestAccServerGroup_HookOrdering_DestroyFirst).
variable "image" {
  type        = string
  description = "Hetzner image; defaults to debian-13 for the first apply, smoke step flips to debian-12 for the second apply to exercise replace."
  default     = "debian-13"
}

# HCLOUD_TOKEN is read from the env by both providers
# (hcloudgroup: provider.go Configure fallback; hcloud: HCLOUD_TOKEN env).
# Do NOT pass it as -var: -var values appear in /proc/<pid>/cmdline.
provider "hcloud" {}
provider "hcloudgroup" {}

resource "hcloud_network" "smoke" {
  name     = "tfacc-smoke-net-${var.suffix}"
  ip_range = "10.97.0.0/16"
  labels   = { "tfacc.io/fixture" = "shared" }
}

resource "hcloud_network_subnet" "smoke" {
  network_id   = hcloud_network.smoke.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.97.1.0/24"
}

resource "hcloudgroup_server_group" "smoke" {
  name        = "tfacc-smoke-${var.suffix}"
  replicas    = 2
  image       = var.image
  server_type = "cpx22"
  location    = "fsn1"
  network_id  = hcloud_network.smoke.id
  labels      = { "tfacc.io/fixture" = "shared" }

  # Server attaches to the network; subnet must exist first or hcloud
  # rejects the attachment.
  depends_on = [hcloud_network_subnet.smoke]
}
