# hcloud Server Group Provider

[![CI](https://github.com/chickeaterbanana/terraform-provider-hcloudgroup/actions/workflows/ci.yml/badge.svg)](https://github.com/chickeaterbanana/terraform-provider-hcloudgroup/actions/workflows/ci.yml)
[![Terraform Registry](https://img.shields.io/badge/registry-chickeaterbanana%2Fhcloudgroup-623CE4)](https://registry.terraform.io/providers/chickeaterbanana/hcloudgroup/latest)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL--2.0-brightgreen.svg)](./LICENSE)

> **Status:** v0.1.0 published on the Terraform Registry. Acceptance tests run against a real Hetzner sandbox on every release.
> **Scope:** Independent greenfield project. Pure tofu/terraform provider, no daemon, no central state store.
>
> **v0.2.0 upgrade note:** the per-slot replace flow now defaults to **create-before-destroy** (`replace_method = "create_before_destroy"`). v0.1.x behaved as if pinned to `destroy_before_create`. Pin destroy-first for fixed-membership quorum systems (etcd, consul, RabbitMQ) and when hcloud vCPU quota is tight. See [§6.2](#62-replacement-method) and [CHANGELOG](./CHANGELOG.md).

---

## 0. Installation

The provider is published on `registry.terraform.io`; `terraform init` resolves it automatically. OpenTofu support (`tofu init` against `registry.opentofu.org`) is a follow-up.

```hcl
terraform {
  required_providers {
    hcloudgroup = {
      source  = "chickeaterbanana/hcloudgroup"
      version = "~> 0.1"
    }
  }
}

provider "hcloudgroup" {
  # Reads HCLOUD_TOKEN from the environment if omitted.
  # token = var.hcloud_token
}
```

Releases are GPG-signed (RSA-4096, fingerprint `E8CCF925766517EC1E99A9F9444DC818EC36F233`); the registry verifies the signature on ingestion. Linux, macOS, and FreeBSD binaries are built for `amd64`, `arm`, and `arm64` (except `darwin/arm`). Windows is not supported in v1 — the action runner uses `/bin/sh` and Unix process-group signals.

See [§7.1](#71-hcl-example) for a fully-worked resource example.

---

## 1. Overview

A custom Terraform/OpenTofu provider that manages groups of Hetzner Cloud servers as a single declarative unit, with rolling-replace semantics. This is the missing primitive that hcloud lacks natively (cf. AWS ASG, GCP MIG).

The provider executes the full reconciliation in-process during `tofu apply`. There is no controller VM, no central daemon, no shared state store. State lives in tofu state plus labels on the hcloud servers themselves.

---

## 2. Goals & non-goals

### 2.1 Goals

- Single resource: `hcloudgroup_server_group` taking `(name, replicas, image, server_type, location, network_id, ssh_keys, labels, user_data_template, replace_on_change, actions, readiness_probe)`. The size attribute is `replicas` (not `count`) because Terraform reserves `count` as a meta-argument on every resource.
- Sequential reconciliation: create N machines one-by-one, replace one-by-one.
- Lifecycle actions: `before_create`, `post_create`, `before_replace`, `post_replace`, `before_remove`, `post_remove`. Type **null** or **command**.
- Templated `user_data` with `.Peers` and slot context, so cluster-join scenarios (raft initial peers, gossip seeds) work declaratively.
- Slot context delivered to commands as environment variables (no inline templating in commands).
- Replace trigger: SHA-256 hash over a defined attribute subset, recorded as a label on each managed server.
- Crash-convergent: `tofu apply` interrupted mid-roll converges on next apply by reading reality from hcloud server labels.
- Linux/macOS runners only. Action commands execute via `/bin/sh -c`, which doesn't exist on Windows. Windows support is a v2 concern.

### 2.2 Non-goals

- Continuous health checking with auto-replace. Readiness is checked once during create/replace; nothing watches afterwards.
- HA / multi-controller. There is no controller.
- Auto-rollback on action failure. Fail fast, surface the error.
- Selective non-replace updates (e.g., labels-only). Any change to a hashed attribute replaces every slot.
- Auto-scaling. Fixed `replicas`.
- Multi-tenancy. Provider auth is the operator's hcloud token, period.

### 2.3 Out of scope

- Other hcloud resources (load balancers, volumes, firewalls). Compose at the tofu level using the official hcloud provider.

### 2.4 Operator responsibilities

The runner that executes `tofu apply` must have:

- hcloud API access (token).
- Reachability to managed VMs' private IPs for readiness probes.
- Reachability to whatever action endpoints the operator configures (typically also on the private network).

For private-only managed VMs this means running tofu from inside the network — a bastion, a VPN/mesh peer (NetBird, WireGuard), a self-hosted CI runner attached to the network, etc. How that reachability is established is outside the provider's concern.

---

## 3. Architecture

```
   ┌──────────────────────────────┐
   │  Operator's runner           │
   │  (tofu apply)                │
   │                              │
   │   ┌────────────────────┐     │
   │   │ hcloudgroup        │─────┼─ HTTPS ─► hcloud API
   │   │ provider in-process│─────┼─ HTTP  ─► action endpoints
   │   └────────────────────┘─────┼─ HTTP  ─► managed VMs (readiness)
   │                              │
   └──────────────────────────────┘
```

The provider is a single Go binary running inside `tofu apply`. No long-running components.

---

## 4. The provider

- Built on **terraform-plugin-framework**.
- Resources: `hcloudgroup_server_group`.
- Data sources: none in v1; a read-only `hcloudgroup_server_group` data source is planned for v2.
- Provider config: `hcloud_token` (write-only / ephemeral). Optional `hcloud_endpoint` for testing.
- Dependencies:
  - `github.com/hetznercloud/hcloud-go/v2` — official hcloud SDK
  - `github.com/hashicorp/terraform-plugin-framework` — provider SDK

A long apply blocks the runner. A 5-node rolling replace with multi-minute readiness windows is on the order of 20-30 minutes of runner time. Set `timeouts` accordingly.

---

## 5. State model

State is split between two tiers, each handling a different failure mode:

- **Tofu state** holds the per-slot records. Updated when CRUD functions return. Crucially, the framework persists state even on error returns — so partial progress across slots survives graceful failures (action timeout, readiness fail, hcloud 4xx). Does **not** survive ungraceful crashes (kill -9, OOM, runner death) because the function never returns.
- **Hcloud server labels** record per-slot facts that must survive ungraceful crashes. Each label update is an atomic hcloud API call, persisted server-side immediately. The `Read` function reconstructs reality from these labels.

The two tiers are complementary: graceful errors → tofu state captures inter-slot progress; ungraceful crashes → labels let `Read` rebuild observed state from scratch.

**Drift detection limits.** `Read` rebuilds state from provider labels, not from physical server attributes. The deltas it can detect are:

- A managed server has been deleted out-of-band → `Read` removes the resource from state so the next plan presents as a fresh create.
- The set of `complete=true` servers per slot has changed → reflected in the `slots` attribute.

The deltas it **cannot** detect are out-of-band edits to `image`, `server_type`, or `location` on a live server. Those are not surfaced as drift because the provider does not compare physical attributes against the desired state during refresh; the rolling-replace contract is hash-based and triggered by HCL changes through `replace_on_change`. Treat the hcloud API as the single source of truth for those attributes — don't edit them outside tofu.

### 5.1 Per-slot record (tofu state)

The resource exposes a computed `slots` attribute:

| Field | Type | Notes |
|-------|------|-------|
| `slot_id` | int | stable, 0..count-1 |
| `server_id` | int | hcloud id |
| `server_name` | string | deterministic: `{group}-{slot}-{generation}` |
| `generation` | int | increments per replace |
| `replace_hash` | string | sha256 hex (full) of inputs that triggered this slot's last create/replace |
| `ip_private` | string | private network IP |
| `status` | string | `ready` / `failed` |
| `last_error` | string | populated only when `status=failed` |

### 5.2 Server labels (hcloud)

Every managed hcloud server carries the following provider-internal labels, all under the **reserved prefix `hcloudgroup.io/`**. The prefix is not configurable. Any user-supplied `labels = { ... }` from the resource HCL must not collide with this namespace; the provider rejects user labels with the prefix at plan time.

| Label | Value | Purpose |
|-------|-------|---------|
| `hcloudgroup.io/managed-by` | `hcloudgroup-provider` | identifies provider-managed servers |
| `hcloudgroup.io/group` | the resource's `name` | scopes by group |
| `hcloudgroup.io/slot` | `0`..`count-1` | which slot |
| `hcloudgroup.io/generation` | integer string | per-slot replace counter |
| `hcloudgroup.io/replace-hash` | first 12 hex chars of full hash | debugging; tofu state holds the full hash |
| `hcloudgroup.io/complete` | `true` / `false` | flipped to `true` only after every post-action for create/replace succeeds |

User labels and provider labels share a single labels map on the hcloud server; they're distinguished by prefix. When `Read` reflects user labels back into tofu state, it filters out the `hcloudgroup.io/` namespace.

**Label updates are full-replace, not patch.** The hcloud `Server.Update(labels: ...)` call replaces the entire labels map. To flip `complete=false` → `complete=true`, the provider must GET the server's current labels, modify the one key locally, and PUT the full set. The provider implements this as a read-modify-write helper. Single-writer per state (tofu state lock) makes the lack of optimistic concurrency safe.

The `complete` label is the linchpin of crash recovery. A server with `complete=false` is an incomplete creation; `Read` ignores it; the next apply destroys it (in pre-flight cleanup) and retries the slot.

### 5.3 Read function

`Read` is purely observational. It does not destroy orphans, does not mutate hcloud state, does not call action endpoints. Its only job is to refresh the tofu-state view of reality.

1. List servers in the project where `hcloudgroup.io/managed-by=hcloudgroup-provider AND hcloudgroup.io/group=<name>`.
2. Group by `slot` label.
3. For each slot id 0..count-1, find the server with the highest `hcloudgroup.io/generation` AND `hcloudgroup.io/complete=true`. That's the canonical record.
4. Build the `slots` list from those canonical records, copying the prior state's `replace_hash` value through unchanged (the full hash isn't stored in labels). Only observable fields — `ip_private`, `server_id`, `server_name`, `generation`, `status` — are refreshed from hcloud.

**Missing canonical:** if no canonical server exists for slot `i` but the prior state had one, the slot is removed from the `slots` list in the response state. This produces a diff at plan time (length mismatch in the `slots` computed attribute), which prompts an Update. The framework allows this: Read can shrink the state. Plan output shows the slot as needing recreation.

Servers that are lower-generation, `complete=false`, or have `slot` ≥ `count` are **not** surfaced. They're tombstones for pre-flight cleanup in the next Create/Update.

### 5.4 Crash recovery

Two recovery paths, depending on failure mode.

**Graceful failure (action returns error / readiness times out / hcloud 4xx).** The CRUD function detects the error, sets the failing slot's `status=failed` and `last_error` in the in-memory model, calls `resp.State.Set` with the partial-progress model, and appends an error diagnostic. Tofu persists that state. The next apply sees:

- Slots completed before the failure: hash matches, no diff, no work.
- The failed slot: status=failed (or, if Read reconciles with hcloud, missing canonical server because `complete=false`). Plan diff → Update retries that slot.
- Slots not yet started: still at old hash → Update continues from there.

**Ungraceful crash (kill -9, OOM, runner death).** The function never returns, no state update reaches tofu. On next apply, `Read` rebuilds observed state from hcloud labels. Anything with `complete=false` (e.g., a half-created server from the crashed slot) is invisible to `Read`; the slot looks empty/needing-replace; Update redoes it. Already-completed slots are visible (their servers have `complete=true`) and match the desired hash, so they're no-ops.

**Pre-flight cleanup at the start of Create/Update.** Before doing the per-slot work, the operation lists servers labeled for this group and destroys any that are `complete=false` or whose `slot` is ≥ current count. This is the only place orphan cleanup happens — `Read` doesn't do it.

**Generation source-of-truth: `max(observed labels for the slot) + 1`, not `prior_state.generation + 1`.** When creating a new server for a slot during a replace, the new generation is computed from the highest `hcloudgroup.io/generation` label observed across all servers (canonical or orphan) for that slot. This avoids name collisions after a crashed mid-replace: the orphan was at generation N, pre-flight destroys it, but its history still informs the next allocation as N+1 rather than N (which would collide with the destroyed orphan's deterministic name during any brief overlap window).

**Action idempotency is the operator's responsibility.** A crashed apply may cause `before_create` / `post_create` / `before_replace` etc. to be invoked twice for the same slot generation. Webhook receivers should be idempotent (e.g., "register peer" should be a no-op if the peer is already registered). v1 deliberately does not track per-action completion via labels — pre-* actions have no persistent entity to label, and the cost-benefit of more granular tracking doesn't beat just expecting idempotent handlers.

### 5.5 Concurrency

Tofu's state lock prevents concurrent applies against the same state. The provider does no additional locking.

---

## 6. State machine

Three flows. Replace composes Remove and Create.

```
CREATE FLOW (initial, scale-up, or inner step of replace):
  empty
    └─► before_create ─fail─► failed
          └─► hcloud Server.Create (returns Action)
                └─► wait Action to success ─fail/timeout─► failed
                      └─► server is "creating" until cloud-init reports ready
                            └─► readiness_probe ─timeout─► failed
                                  └─► post_create ─fail─► failed
                                        └─► label complete=true (read-modify-write)
                                              └─► ready

REMOVE FLOW (scale-down, or inner step of replace):
  ready
    └─► before_remove ─fail─► failed
          └─► hcloud Server.Delete (returns Action)
                └─► wait Action to success ─fail/timeout─► failed
                      └─► post_remove ─fail─► failed
                            └─► empty

REPLACE FLOW (rolling update, generation+1):
  ready
    └─► before_replace ─fail─► failed
          └─► [REMOVE FLOW]
                └─► [CREATE FLOW with generation+1]
                      └─► post_replace ─fail─► failed
                            └─► ready
```

**hcloud operations are async.** `Server.Create` and `Server.Delete` return immediately with an `Action` object; the actual work happens asynchronously on Hetzner's side. The provider must poll the Action via `client.Action.WaitForFunc` (in `hcloud-go/v2`) until it reaches a terminal state (`success` or `error`). Server creation typically takes 30-90 seconds; deletion is faster. Timeout the wait at a sensible bound (e.g., 5 min) and treat exhaustion as a slot failure.

Full action sequence on replace depends on `replace_method` (see §6.2):

**`create_before_destroy` (default since v0.2.0):**

1. `before_replace`
2. `before_create`
3. hcloud `Server.Create` (with `complete=false` label) + wait Action
4. `readiness_probe`
5. `post_create`
6. label flip `complete=true`
7. `before_remove`
8. hcloud `Server.Delete` + wait Action
9. `post_remove`
10. `post_replace`

**`destroy_before_create` (v0.1.x behavior):**

1. `before_replace`
2. `before_remove`
3. hcloud `Server.Delete` + wait Action
4. `post_remove`
5. `before_create`
6. hcloud `Server.Create` (with `complete=false` label) + wait Action
7. `readiness_probe`
8. `post_create`
9. label flip `complete=true`
10. `post_replace`

Cluster-wide concerns go in `before_replace` / `post_replace` (suspend rebalancer, snapshot). Per-server concerns go in the inner hooks.

### 6.1 Group-level orchestration

Every Create/Update operation runs the following ordered phases:

1. **Pre-flight cleanup.** List managed servers for the group; destroy any `complete=false` orphans and any with `slot` ≥ new count. Reach a clean baseline before applying the delta.
2. **Remove** slots beyond `new_count` (scale-down, walking `N-1` down to `new_count`).
3. **Replace** remaining slots whose recorded hash differs from the new desired hash, walking `0` to `new_count-1`. Slot `K+1` only starts once slot `K` reaches `ready`.
4. **Create** new slots beyond the current count (scale-up), walking up to `new_count-1`.

The order matters. Removing first means a soon-to-be-deleted slot is never replaced (no wasted roll), and a slot that is currently `failed` and being scaled away just gets removed — no make-it-healthy-first detour. Replacing before creating means initial scale-up benefits from the most recent peer state having already been rolled.

| Trigger | Behavior |
|---------|----------|
| **Initial create** | Pre-flight (no-op, nothing exists). Phase 4 only: walk slots `0..N-1` sequentially through CREATE FLOW. Sequential because cluster-join templates may need IPs of earlier slots. |
| **Scale up** | Pre-flight, then phase 4 starting from current count. No `before_replace` / `post_replace`. |
| **Scale down** | Pre-flight, then phase 2 in reverse order. No `before_replace` / `post_replace`. |
| **Rolling replace** | Pre-flight, then phase 3 across all slots. |
| **Mixed (replace + scale)** | Pre-flight → remove → replace → create, in that order. |

If a slot fails mid-phase, the provider returns an error to tofu but **preserves progress** for slots that did complete — those updates are written into the response state via `resp.State.Set` before the error is appended. The next apply picks up from the resulting partial state.

### 6.2 Replacement method

The `replace_method` resource attribute selects per-slot replace ordering. Two values; default is `"create_before_destroy"` since v0.2.0 (v0.1.x behaved as if pinned to `"destroy_before_create"`).

- **`create_before_destroy` (default).** For each slot under replace: create the new server at `generation+1`, run the readiness probe and `post_create`, flip its `complete=true` label, THEN delete the old server. The slot transiently has two `complete=true` servers (visible briefly in `hcloud server list`) for at least the duration of the readiness probe. Suits stateless workloads and any cluster member that can join before its predecessor is removed.
- **`destroy_before_create`.** Delete the old server first, then create the new one. The slot is transiently empty between delete and the new server reaching `ready`. Matches v0.1.x behavior. Required for fixed-membership quorum systems (etcd, consul, RabbitMQ) where two simultaneous members at slot K can break voting math; recommended for any operator running close to their hcloud vCPU/server quota (create-first transiently uses `replicas + 1` servers per replace step).

**Crash recovery does not run hooks.** If an apply crashes between the new server's `complete=true` flip and the old server's delete (create-first), the next apply's preflight reaps the now-superseded old server *without* running `before_remove`/`post_remove` — same contract as today's orphan cleanup. If the crash happens earlier (mid-readiness, before the new server's label flip), preflight reaps the incomplete new server as an orphan and rebinds the slot's tofu state to the surviving old server, then `phaseReplace` rolls the slot again with full hooks. Both windows converge in a single follow-up apply.

**Toggling `replace_method` does not trigger a replace.** It controls *how* a replace happens, not *whether* one is needed; the value is deliberately omitted from the replace hash. Operators can flip the attribute on a steady-state config and see only a no-op plan.

**Upgrading from v0.1.x:** the default switched. Operators running stateful clusters or close to quota should pin `replace_method = "destroy_before_create"` BEFORE running `terraform plan` after the upgrade. Imported v0.1.x resources (via `terraform import`) likewise inherit the new default; pin explicitly to preserve prior ordering.

---

## 7. Resource schema: `hcloudgroup_server_group`

### 7.1 HCL example

```hcl
resource "hcloudgroup_server_group" "consul" {
  name        = "consul-servers"
  replicas    = 3
  image       = "consul-1.21-bookworm-20260415"
  server_type = "cx23"
  location    = "fsn1"
  network_id  = 12345
  ssh_keys    = ["ops"]

  labels = {
    role = "consul-server"
    env  = "prod"
  }

  user_data_template = file("${path.module}/cloud-init/consul.yaml.tpl")

  # Attributes whose change triggers a rolling replace, in addition to
  # the always-on set (see §10).
  replace_on_change = ["user_data_template"]

  before_create {
    command {
      # Action commands run with a CLEAN environment — only the operator-supplied
      # `env` map plus the auto-populated HCLOUDGROUP_* vars are present. PATH is
      # not inherited from the runner. Set it (and HOME etc.) explicitly when needed.
      command = <<-EOT
        curl -fsS -X POST \
          -H "Authorization: Bearer $TOKEN" \
          -H "Content-Type: application/json" \
          --data "{\"slot\": $HCLOUDGROUP_SLOT_ID}" \
          https://cluster.example/prepare/$HCLOUDGROUP_SLOT_ID
      EOT
      env = {
        PATH  = "/usr/bin:/bin"
        TOKEN = var.cluster_token
      }
      expected_exit = [0]
      timeout       = "30s"
    }
  }

  readiness_probe {
    command {
      command           = "ssh -o StrictHostKeyChecking=no ops@$HCLOUDGROUP_PRIVATE_IP systemctl is-active consul"
      env = {
        PATH = "/usr/bin:/bin"
        HOME = "/root"   # ssh needs HOME to find ~/.ssh
      }
      expected_exit     = [0]
      interval          = "5s"
      timeout           = "5s"
      success_threshold = 3
      total_timeout     = "5m"
    }
  }

  post_create    { command { command = "..."; env = { PATH = "/usr/bin:/bin" } } }
  before_replace { command { command = "..."; env = { PATH = "/usr/bin:/bin" } } }
  before_remove  { command { command = "..."; env = { PATH = "/usr/bin:/bin" } } }
  post_remove    { command { command = "..."; env = { PATH = "/usr/bin:/bin" } } }
  post_replace   { command { command = "..."; env = { PATH = "/usr/bin:/bin" } } }

  timeouts {
    # Defaults are sized for a 5-node group with multi-minute readiness;
    # tune up for slower clusters.
    create = "60m"
    update = "90m"
    delete = "30m"
  }
}
```

All action blocks are optional; absent or `null` actions are no-ops. For larger configurations, factor common env values into a `locals` block:

```hcl
locals {
  base_env = {
    PATH = "/usr/bin:/bin:/usr/local/bin"
    HOME = "/root"
  }
}
# then: env = merge(local.base_env, { TOKEN = var.token })
```

### 7.2 Computed attributes

- `slots` — list of `{ slot_id, server_id, server_name, ip_private, generation, replace_hash, status }`.
- `current_replace_hash` — for debugging / drift inspection.

---

## 8. Action system

Two action types:

- **`null`** — explicit no-op. Same as omitting the action block.
- **`command`** — runs a shell command on the runner that's executing tofu apply.

### 8.0 The `command` block

```hcl
command {
  command       = "..."           # required; passed to /bin/sh -c
  env           = { K = "v" }     # optional; the ENTIRE environment seen by the command
                                  # (other than auto-populated HCLOUDGROUP_* vars)
  stdin         = "..."           # optional; written to the command's stdin
  working_dir   = "/tmp/..."      # optional; defaults to a per-action ephemeral tempdir
  expected_exit = [0]             # optional; default [0]
  timeout       = "30s"           # required for actions; per-attempt for readiness_probe
}
```

**Clean execution environment.** The child process runs with a deliberately empty environment by design. Only two sources contribute:

1. The auto-populated `HCLOUDGROUP_*` variables (§8.1).
2. The operator's `env = { ... }` map.

The runner's environment is **not** inherited. PATH, HOME, USER, SSH_AUTH_SOCK, AWS_*, and any other variable from the runner's process are absent unless the operator explicitly puts them in `env`. This is a deliberate isolation choice: the runner often holds secrets unrelated to the action (cloud credentials, CI tokens, SSH agent sockets). Cleaning the environment prevents those from leaking into action scripts that may log, dump, or transmit env to third parties.

Practical consequence: most commands need at least `PATH = "/usr/bin:/bin"` to find executables. SSH-based commands typically need `HOME` so OpenSSH can locate `~/.ssh`. The HCL example in §7.1 shows the standard pattern.

**Reserved namespace.** Operator `env` values whose keys begin with `HCLOUDGROUP_` are rejected at plan time. The `HCLOUDGROUP_*` variables are reserved for the provider and operators cannot shadow them.

Execution: the provider runs `/bin/sh -c <command>` as a child process of the tofu apply process. Operators wanting bash-specific features write `bash -c '...'` explicitly. There is no template substitution in the `command` string — all dynamic data flows through env vars, eliminating injection risk from quoted/unquoted template values.

The `readiness_probe` block uses the same `command` shape with three additional fields:

- `interval` — wait between attempts
- `success_threshold` — consecutive successful attempts required
- `total_timeout` — overall deadline; if exceeded, the probe fails

### 8.1 Slot context as environment variables

The provider populates the following env vars before running each command. Operator `env` keys cannot collide with this namespace (rejected at plan time).

| Env var | Set when | Notes |
|---|---|---|
| `HCLOUDGROUP_GROUP_NAME` | always | resource's `name` |
| `HCLOUDGROUP_SLOT_ID` | always | int as string |
| `HCLOUDGROUP_GENERATION` | always | int as string |
| `HCLOUDGROUP_SERVER_NAME` | always | deterministic: `{group}-{slot}-{generation}` |
| `HCLOUDGROUP_NOW` | always | RFC3339 timestamp |
| `HCLOUDGROUP_PEERS_JSON` | always | JSON array of `{ slot_id, server_name, private_ip, generation }` for **other** slots; `[]` if none |
| `HCLOUDGROUP_PEER_PRIVATE_IPS` | always | space-separated; convenience for shell loops |
| `HCLOUDGROUP_SERVER_ID` | post-create / replace / remove | unset in `before_create` |
| `HCLOUDGROUP_PRIVATE_IP` | post-create / replace / remove | unset in `before_create` |

Peers JSON deliberately excludes `status` — it changes more often than peer identity and would noise up scripts that aren't watching for it.

Per-action availability of post-create vars:

| Action / probe | post-create vars set? |
|---|---|
| `before_create` | no (server doesn't exist) |
| `readiness_probe` | yes |
| `post_create` | yes |
| `before_replace` | yes (old generation's server) |
| `before_remove` | yes (slot being removed) |
| `post_remove` | no (server destroyed) |
| `post_replace` | yes (new generation's server) |

Scripts should use `[ -n "$HCLOUDGROUP_PRIVATE_IP" ]` style guards if they need to run uniformly across action contexts.

### 8.2 Result semantics

| Outcome | Slot | Operation |
|---------|------|-----------|
| Command exits within `timeout` AND exit code ∈ `expected_exit` | advance | continue |
| Command exits with code ∉ `expected_exit`, or process timed out | → `failed` | → tofu error; partial progress preserved |
| Probe reaches `total_timeout` without satisfying `success_threshold` | → `failed` | → tofu error; partial progress preserved |
| hcloud API transient error (5xx, network) | retry with exponential backoff up to 5 min, then fail | |

When a command fails, the provider captures stdout and stderr (tail to ~4KB each) and surfaces them in the tofu error diagnostic so operators can see what went wrong without digging through process logs.

When a slot enters `failed`, the new server (if any) is **not** destroyed. It carries `complete=false`, so `Read` won't surface it on the next apply, and pre-flight cleanup will remove it before retry.

---

## 9. user_data templating

Only `user_data_template` uses Go's `text/template`. Commands receive the same slot context via environment variables (§8.1), not template substitution.

### 9.1 Template variables for user_data

| Variable | Notes |
|----------|-------|
| `.GroupName` | always |
| `.SlotID` (int) | always |
| `.ServerName` | always (deterministic from `{group}-{slot}-{generation}`) |
| `.Generation` | always |
| `.Peers` | list of `{ SlotID, ServerName, PrivateIP, Generation, Status }` for **other** slots in the group, in slot-id order |
| `.Now` (RFC3339 string) | always |

`.PrivateIP` and `.ServerID` for the slot itself are not available — `user_data` is rendered before the server exists.

### 9.2 Cluster-join example

`user_data_template`:

```
write_files:
  - path: /etc/consul.d/peers.json
    content: |
      {{ "{" }}"retry_join": [
        {{ range $i, $p := .Peers }}{{ if $i }},{{ end }}"{{ $p.PrivateIP }}"{{ end }}
      ]{{ "}" }}
```

For slot 0, `.Peers` is empty → bootstrap-expect mode.
For slot 1, `.Peers = [slot 0]` → joins existing.
For slot 2, `.Peers = [slot 0, slot 1]` → joins existing.

This is why initial creation is sequential.

---

## 10. Replace-trigger hashing

The provider computes:

```
replace_hash = sha256(canonical_json(
  image, server_type, user_data_template_source, network_id, location, ssh_keys, labels,
  ...replace_on_change
))
```

- `user_data_template_source` is the raw template string before rendering. Peer changes therefore do **not** trigger cascading replaces.
- `replace_on_change` is a user-controlled list of attribute names whose values are folded into the hash. Recognized names are the same as the always-on set: `image`, `server_type`, `user_data_template`, `network_id`, `location`, `ssh_keys`, `labels`. Listing one is currently redundant (every recognized name is already always-on); the knob is the documented extension point for attributes added outside the always-on set in future versions. Unknown names are rejected at plan time.

Reconcile loop, per slot:

| Condition | Action |
|-----------|--------|
| Slot doesn't exist (count grew) | create |
| Slot exists, beyond new count (count shrank) | remove |
| Slot exists, recorded hash == new hash | no-op |
| Slot exists, recorded hash != new hash | replace |

Coarse by design: any change to a hashed attribute replaces every slot.

---

## 11. Failure & retry semantics

| Failure class | Behavior |
|---------------|----------|
| Command exits with code ∉ `expected_exit`, times out, or fails to spawn | slot → `failed`; tofu apply errors with stdout/stderr tail. Re-run apply to retry. Incomplete server (if any) cleaned up on retry by pre-flight. |
| Readiness probe `total_timeout` reached | slot → `failed`. Server not destroyed inline; `complete=false` keeps it out of canonical state; cleaned on next apply. |
| hcloud API transient (5xx, network) | exponential backoff to 5 min cap, then fail. |
| hcloud API permanent (4xx) | immediate fail. |
| Template render error (`user_data_template`) | fail at provider plan time. |
| Out-of-band server deletion | next `Read` doesn't find a canonical server for the slot → next apply replaces it. |

---

## 12. Network reachability

The runner needs to reach the hcloud API (public) and whatever endpoints the operator's commands talk to — typically the managed VMs' private IPs (for readiness probes) and cluster control planes. Common patterns:

- **Self-hosted CI runner inside the private network.** Cleanest. Runner has a NIC on the same hcloud Network as the managed VMs.
- **Mesh VPN (NetBird, WireGuard, Tailscale).** Runner is a peer; private IPs route through the mesh.
- **SSH bastion + tofu over SSH.** Runner runs on a bastion host inside the network.

The runner must also have whatever tools the operator's commands invoke installed: `curl`, `ssh`, `kubectl`, `psql`, etc. The provider does not provision these — operators are responsible for the runner image.

If a command can't reach its target, it will fail (timeout or non-zero exit), the slot ends in `failed`, and the operator sees the captured stderr in the tofu error.

---

## 13. Implementation hints

### 13.1 Repository layout

```
terraform-provider-hcloudgroup/
├── main.go                          # provider entry point
├── internal/
│   ├── provider/                    # provider boilerplate (config, schema)
│   ├── resource_server_group/       # CRUD functions for the resource
│   ├── reconciler/                  # state machine, slot iteration, pre-flight cleanup
│   ├── actions/                     # null + command action types, env var assembly, exec wrapper
│   ├── template/                    # Go text/template wrapper for user_data
│   └── hcloudx/                     # hcloud client wrapper, label-based discovery, Action waiting
├── examples/                        # HCL examples
└── docs/                            # generated provider docs
```

The repo name follows the tofu convention (`terraform-provider-<NAME>` works for both Terraform and OpenTofu). The published binary's resource type prefix is `hcloudgroup_`.

### 13.2 Sequencing of v1 implementation

1. Resource schema: attributes, blocks, plan-time validators:
   - `replicas ≥ 1`
   - server name length: `len(group_name) + len("-{slot}-{generation}")` ≤ 63 (RFC 1123); reject at plan time using a worst-case generation budget (e.g., 6 digits)
   - operator `labels` must not use the `hcloudgroup.io/` prefix
   - operator `env` keys must not start with `HCLOUDGROUP_`
2. Replace-hash computation. Pure function, easy to unit-test.
3. hcloud client wrapper:
   - `ListByGroup(name) → []Server` (label selector)
   - `Create(spec) → (server, action)` and `WaitAction(action)`
   - `Delete(id) → action` and `WaitAction(action)`
   - `UpdateLabels(id, labels)` — read-modify-write helper that GETs current labels, merges, PUTs full set
4. `Read`: list by labels, group by slot, pick canonical (highest generation with `complete=true`), build state. Strip `hcloudgroup.io/*` from user-visible labels.
5. Action runner: null + command types. Env var population from slot context. `/bin/sh -c` execution with **clean environment** (only operator `env` + `HCLOUDGROUP_*`). Stdout/stderr capture (4KB tail each). Exit code matching against `expected_exit`.
6. `user_data` template renderer.
7. `Create`: initial group, sequential slot creation. Each slot: action runner + Server.Create + WaitAction + readiness probe + post-create + label flip. Generation source-of-truth: `max(observed labels for slot) + 1`.
8. Readiness probe: polling loop on top of the command runner with `success_threshold` and `total_timeout`.
9. `Update`: pre-flight cleanup, diff slots, dispatch to CREATE / REPLACE / REMOVE flows in the §6.1 ordering. Partial-progress error reporting via `resp.State.Set` + diagnostics.
10. `Delete`: walk slots through REMOVE flow.
11. End-to-end acceptance tests against a real hcloud sandbox project plus a private network.

### 13.3 Testing

- Unit tests with a mocked hcloud client for the reconciler and action runner.
- Acceptance tests via the framework's `acctest` against real hcloud (sandbox project).
- Crash-recovery tests: kill the test process mid-replace, verify next apply converges. (Hardest to write; defer to manual chaos testing initially.)

### 13.4 Distribution

The provider is published as a tofu/terraform provider binary, not as a Go module for direct import. Distribution paths in order of effort:

- **OpenTofu Registry** (registry.opentofu.org): publish source from this repo, registry serves the binaries built per-OS/arch via GitLab CI. Best UX for consumers (`tofu init` just works). Requires registry submission.
- **Private GitLab distribution.** GitLab does **not** have a Terraform/OpenTofu Provider Registry as of this writing ([gitlab#356716](https://gitlab.com/gitlab-org/gitlab/-/issues/356716) is open). The realistic pattern for a private provider:
  1. GitLab CI builds binaries per-OS/arch on tag, plus the SHA256SUMS and `manifest.json` files the provider protocol expects.
  2. Upload to GitLab Generic Package Registry as artifacts.
  3. Consumers configure tofu's `provider_installation` block with a `network_mirror` or `filesystem_mirror` pointing at the package URL or a synced local directory.
  4. Optionally GPG-sign the artifacts.
- **Self-hosted provider registry** (Citizen, Boring Registry, etc.) on top of GitLab artifacts. Real overhead; only worth it if a fleet of teams will consume.

For the Go *module* (the source code, useful only if other projects want to import the reconciler internals as a library — not the typical case): GitLab's Go module proxy at `/api/v4/projects/:id/packages/go` serves modules tagged in the project's Git history. Caveat: GitLab UI doesn't display Go modules ([gitlab#213770](https://gitlab.com/gitlab-org/gitlab/-/issues/213770)).

Default recommendation: build for OpenTofu Registry first, fall back to GitLab Generic Package + filesystem mirror for private use cases.

---

## 14. Open questions

None blocking. Default timeouts (`create=60m`, `update=90m`, `delete=30m` per the example) are sized for a 5-node group with multi-minute readiness probes. Tune after first benchmark on real workloads.

---

## 15. Glossary

- **Slot**: a stable position 0..count-1 in a group. A slot can be empty or hold a server of some generation. Replacing a slot increments its generation but keeps the slot id.
- **Generation**: per-slot counter, incremented on each replace. Computed as `max(observed labels for slot) + 1`. Used in the deterministic server name and as a label.
- **Replace hash**: SHA-256 over a defined attribute subset. Recorded per-slot in tofu state and as a 12-char prefix in a label.
- **Canonical server (for a slot)**: the highest-generation server for that slot with `hcloudgroup.io/complete=true`. The one `Read` surfaces.
- **Incomplete server**: a server with `hcloudgroup.io/complete=false` — created but post-actions did not finish. Garbage-collected by pre-flight cleanup on the next Update.
- **Reserved namespaces**: `hcloudgroup.io/` for server labels, `HCLOUDGROUP_` for command env vars. Operator inputs in the matching prefix are rejected at plan time.

---

## License

[Mozilla Public License v2.0](./LICENSE). File-scoped weak copyleft: vendor freely, but modifications to MPL-licensed source files in this repo stay MPL.
