# Resources reuse the `tfacc-` prefix and `tfacc.io/fixture` label so
# the acctest sweeper (internal/acctest/fixtures.go) cleans up stranded
# smoke fixtures from a panicked or cancelled run.

terraform {
  required_providers {
    hcloud      = { source = "hetznercloud/hcloud", version = "~> 1.48" }
    hcloudgroup = { source = "chickeaterbanana/hcloudgroup" }
    tls         = { source = "hashicorp/tls", version = "~> 4.0" }
  }
}

variable "suffix" {
  type        = string
  description = "Per-matrix-leg suffix to keep concurrent runs from colliding."
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

resource "tls_private_key" "smoke" {
  algorithm = "ED25519"
}

resource "hcloud_ssh_key" "smoke" {
  name       = "tfacc-smoke-key-${var.suffix}"
  public_key = trimspace(tls_private_key.smoke.public_key_openssh)
  labels     = { "tfacc.io/fixture" = "shared" }
}

resource "hcloudgroup_server_group" "smoke" {
  name        = "tfacc-smoke-${var.suffix}"
  replicas    = 1
  image       = "debian-13"
  server_type = "cpx22"
  location    = "fsn1"
  network_id  = hcloud_network.smoke.id
  ssh_keys    = [hcloud_ssh_key.smoke.name]
  labels      = { "tfacc.io/fixture" = "shared" }

  # Server attaches to the network; subnet must exist first or hcloud
  # rejects the attachment.
  depends_on = [hcloud_network_subnet.smoke]
}
