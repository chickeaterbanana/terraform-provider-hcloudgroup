# Testing

The provider has three test tiers. Each gates a different scenario and the
ratchet from one to the next is real-world cost.

| Tier | When | Cost | Network |
|---|---|---|---|
| Hermetic | every commit, PR | free | none |
| Reconciler scenarios | every commit | free (uses fake hcloud client) | none |
| Acceptance | nightly, before release, on demand | hcloud spend (single-digit EUR/run) | real Hetzner Cloud |

## Run the tests

```sh
# Tiers 1+2: hermetic. Fast.
make testrace

# Tier 3: acceptance. Requires HCLOUD_TOKEN and ssh on PATH.
HCLOUD_TOKEN=... make testacc
```

Both tiers also run via the `CI` GitHub Actions workflow. The `acctest` job
runs nightly (cron `17 3 * * *`) and on `workflow_dispatch`.

## Acceptance test fixtures

The acctest suite provisions a small set of shared resources once per run
and reuses them across tests:

- A Hetzner Network `tfacc-shared-net` (10.99.0.0/16, fsn1).
- An ed25519 SSH key uploaded as `tfacc-shared-key`.
- A `cx23` jump host `tfacc-jump` with public IPv4. Readiness probes and
  hook commands SSH into managed servers as
  `ssh -J root@<jump-public> root@<server-private-ip>`.

These cost roughly €0.0076/h plus traffic. Tests are serial (`-parallel=1`)
because they share that jump host and Network.

Each test calls `t.Cleanup(...)` to delete its own group's servers via the
SDK, regardless of whether the framework destroy step runs. A panic-style
failure leaves shared fixtures behind; the next run's `TestMain` global
sweeper picks them up before any tests start.

## Sandbox token requirement

Use a Hetzner project dedicated to test workloads. The sweeper has two
levels of safety:

- Provider-managed servers must carry the
  `hcloudgroup.io/managed-by=hcloudgroup-provider` label AND a
  `hcloudgroup.io/group` label that begins with `tfacc`.
- Shared fixtures (network, ssh key, jump host) must carry the
  `tfacc.io/fixture=shared` identity label.

So a same-named resource without those labels is left untouched. Even
so: keeping production resources in a separate project is the proper
boundary, not the labels.

## Run with terraform vs. opentofu

The provider factories in `internal/acctest/provider_factories.go`
register the in-process provider under the bare key `hcloudgroup`. The
plugin-testing framework builds the full address as
`<host>/<namespace>/hcloudgroup` based on the runner. By default, that's
`registry.terraform.io/hashicorp/hcloudgroup` — fine for `terraform`.

For `tofu`, set:

```sh
TF_ACC_TERRAFORM_PATH=$(which tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org \
  HCLOUD_TOKEN=... make testacc
```

Both binaries pass.

## Crash-recovery (chaos) recipe

The framework harness can't simulate `kill -9` mid-apply. To exercise
the README §5.4 ungraceful-crash guarantee by hand:

1. Apply a small group:

   ```hcl
   resource "hcloudgroup_server_group" "test" {
     name        = "chaos"
     replicas    = 2
     image       = "debian-13"
     server_type = "cx23"
     location    = "fsn1"
     network_id  = <id>
     ssh_keys    = ["<key>"]
     user_data_template = "#cloud-config\n"
   }
   ```

2. Plan a rolling replace by changing `image` to `ubuntu-24.04`. Run
   `terraform apply` in one terminal.

3. As soon as you see "creating server" for the first slot, in another
   terminal:

   ```sh
   pkill -9 -f 'terraform apply'
   ```

4. The hcloud project now has either an old canonical and a new
   `complete=false` server (mid-replace), or just the old canonical
   (crash before create).

5. `terraform apply` again. Expected:
   - Pre-flight destroys any `complete=false` orphan.
   - Replace re-runs from a clean state.
   - Generation continues from `max(observed)+1` so names don't collide.

6. Verify:
   ```sh
   hcloud server list --selector hcloudgroup.io/group=chaos
   ```
   Two servers, both `complete=true`, both at the new generation.

If the recovery apply errors out repeatedly, that's a regression —
file an issue with the labels of the surviving servers attached.

## Sweep manually

```sh
HCLOUD_TOKEN=... make sweep
```

This wipes any `tfacc-*` server, network or SSH key in the project. Safe
to run any time on a sandbox project.
