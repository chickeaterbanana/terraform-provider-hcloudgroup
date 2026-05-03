terraform {
  required_version = ">= 1.5"

  required_providers {
    hcloudgroup = {
      source  = "chickeaterbanana/hcloudgroup"
      version = "~> 0.1"
    }
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.48"
    }
  }
}

variable "hcloud_token" {
  type      = string
  sensitive = true
}

variable "cluster_token" {
  type      = string
  sensitive = true
  default   = ""
}

provider "hcloudgroup" {
  hcloud_token = var.hcloud_token
}

provider "hcloud" {
  token = var.hcloud_token
}

# Pre-existing infrastructure managed by the official hcloud provider.
# The hcloudgroup provider composes on top of these primitives - it does
# not manage networks, subnets, ssh keys, etc.

resource "hcloud_network" "internal" {
  name     = "consul-internal"
  ip_range = "10.0.0.0/16"
}

resource "hcloud_network_subnet" "internal" {
  network_id   = hcloud_network.internal.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.0.1.0/24"
}

resource "hcloud_ssh_key" "ops" {
  name       = "ops"
  public_key = file("~/.ssh/id_ed25519.pub")
}

# The group itself. Three Consul servers, rolled one at a time on any
# image change. Each server runs cloud-init with a peers.json populated
# from the .Peers list.

locals {
  base_env = {
    PATH = "/usr/bin:/bin:/usr/local/bin"
    HOME = "/root"
  }
}

resource "hcloudgroup_server_group" "consul" {
  name        = "consul-servers"
  count       = 3
  image       = "debian-12"
  server_type = "cx22"
  location    = "fsn1"
  network_id  = hcloud_network.internal.id

  ssh_keys = [hcloud_ssh_key.ops.name]

  labels = {
    role = "consul-server"
    env  = "prod"
  }

  user_data_template = file("${path.module}/cloud-init/consul.yaml.tpl")

  replace_on_change = ["user_data_template"]

  before_create {
    command {
      command       = <<-EOT
        if [ -z "$TOKEN" ]; then exit 0; fi
        curl -fsS -X POST \
          -H "Authorization: Bearer $TOKEN" \
          -H "Content-Type: application/json" \
          --data "{\"slot\": $HCLOUDGROUP_SLOT_ID}" \
          https://cluster.example/prepare/$HCLOUDGROUP_SLOT_ID
      EOT
      env           = merge(local.base_env, { TOKEN = var.cluster_token })
      expected_exit = [0]
      timeout       = "30s"
    }
  }

  readiness_probe {
    command {
      command           = "ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@$HCLOUDGROUP_PRIVATE_IP systemctl is-active consul"
      env               = local.base_env
      expected_exit     = [0]
      interval          = "5s"
      timeout           = "5s"
      success_threshold = 3
      total_timeout     = "5m"
    }
  }

  timeouts {
    create = "60m"
    update = "90m"
    delete = "30m"
  }
}

output "consul_private_ips" {
  value = [for s in hcloudgroup_server_group.consul.slots : s.ip_private]
}
