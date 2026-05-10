# Changelog

All notable changes to this provider are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.1] — 2026-05-10

### Fixed

- Post-create `GetServer` now tolerates Hetzner's eventual-consistency window via a new `RetryIncludingNotFound` helper (NotFound retryable for up to 30s). The v0.4.0 smoke matrix exposed this race because rolling-replace (introduced by v0.4.0's image-flip step) calls `server_get_after_create` four times per leg under heavy concurrent API load; production users on slower regions or under API contention can hit it too. Plain `Retry` semantics are unchanged — destroy / delete-confirmation paths still treat NotFound as terminal.

## [0.4.0] — 2026-05-10

### Changed

- **BREAKING (default behavior).** Per-slot replace flow now defaults to **create-before-destroy** (`replace_method = "create_before_destroy"`). v0.1.x behaved as if pinned to `destroy_before_create`. Operators of fixed-membership quorum systems (etcd, consul, RabbitMQ) and operators near their hcloud vCPU/server quota should pin the prior behavior explicitly:

  ```hcl
  resource "hcloudgroup_server_group" "example" {
    # …
    replace_method = "destroy_before_create"
  }
  ```

  Toggling the attribute does **not** trigger a rolling replace — it controls *how* a replace happens, not *whether* one is needed. Hash inputs are unchanged.

  Imported v0.1.x state (via `terraform import`) inherits the new default; pin explicitly to preserve prior ordering.

### Added

- `replace_method` attribute on `hcloudgroup_server_group` (string enum: `create_before_destroy` | `destroy_before_create`).
- Preflight reaps superseded servers (lower-generation `complete=true` siblings of the slot's recorded server) automatically. Reaping is gated on the new server being observed `complete=true`, so a crash mid-readiness-probe does not over-reap and strand the slot.
- After reaping, preflight rebinds slot state to the surviving canonical observation (clearing the recorded `replace_hash` so `phaseReplace` re-rolls the slot with full hooks). Resolves the create-first crash-mid-readiness recovery path in a single follow-up apply.

### Notes for upgraders

- Existing acceptance tests for hook ordering have been split into `_CreateFirst` and `_DestroyFirst` siblings; CI exercises both.
- Smoke runs against the new default (`create_before_destroy`); `destroy_before_create` is exercised by the acceptance suite.
- README §6.2 documents the new ordering, the dual-`complete=true` window, crash recovery, and quota / stateful-cluster guidance.

## [0.2.0] — 2026-04 and [0.3.0] — 2026-05

CI / smoke / acceptance plumbing only — no user-visible provider behavior change. See `git log v0.1.0..v0.3.0` for the full set of release-engineering fixes (acctest provider host, candidate server-types, opentofu pre-install, smoke fixture iterations, etc.).

## [0.1.0] — 2026-04

Initial public release. See README for scope.
