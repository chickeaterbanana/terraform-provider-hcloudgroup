# Findings from the test-suite review

The new test suite went through six independent code reviews (three per
round, with the second round driven by perspectives the first round
suggested). Most findings about the test code itself were addressed in
the same session — see the commit history. The reviews also surfaced
**production-code defects** that were subsequently fixed in this same
PR; status is marked per finding below.

## Production-code findings — status

| # | Finding                                                                | Status |
|---|------------------------------------------------------------------------|--------|
| 1 | Generation source-of-truth violates README §5.4                        | fixed  |
| 2 | `ImportState` corrupts state — duplicates servers on first apply       | fixed  |
| 3 | `replace_on_change` hashes attribute *names*, not values               | fixed (BREAKING — see below) |
| 4 | `SetProviderLabel` (the `complete=true` flip) is not retried           | fixed  |
| 5 | `GetServer` re-fetch after `CreateServer` silently swallows errors     | fixed  |
| 6 | `user_data_template` parse errors fail at apply time, not plan time    | fixed  |
| 7 | Concurrent applies from separate state backends destroy in-flight servers | open (lowest priority — invasive; documented usage forbids the trigger) |
| 8 | `SingleNestedBlock` workaround loses diagnostic path quality           | fixed  |

### Breaking change: replace_on_change

Before this PR, `replace_on_change = ["typo"]` was silently a no-op (the
hash flipped only on list shape, never on attribute value). After this
PR, unknown attribute names are rejected at plan time with an
`AddAttributeError` diagnostic. Operators with existing typos will see a
plan-time error and must remove the typo from their HCL.

## Detailed finding descriptions (with fix references)

### 1. Generation source-of-truth violates README §5.4 — FIXED

**Confidence:** 95+

`internal/reconciler/phases.go` re-fetched `r.observed` after preflight
orphan cleanup. The orphan's generation was gone from the map by the
time `nextGenerationFor` ran, so the new server's generation could
collide with the just-deleted orphan's name.

Concrete failure: a crashed apply left `complete=false` orphan at
generation N+1; the next apply destroyed it and created a new server
also named `{group}-{slot}-(N+1)`. Hetzner refuses duplicate server
names; the provider retried the 409 (it classifies Conflict as
retryable), opaquely failing for ~5 minutes.

**Fix:** `runner.genHighWater` snapshots the per-slot generation
high-watermark before pre-flight runs (`reconciler.go` —
`snapshotGenerations`). `nextGenerationFor` reads from that snapshot
and falls through to the live `observed` when the snapshot is lower.

**Tests flipped/added:** `TestPreflight_NextGenerationFromPreCleanupSnapshot_AvoidsNameCollision`
and `TestPreflight_OrphansAndStragglersTogether_PhaseOrdered` now assert
the correct (README §5.4) behavior.

**Why no test caught it earlier:** the original tests pinned the buggy
behavior with inline `FINDING:` comments — they were "passing" against
the bug rather than asserting the spec. See task #31 on test-pin
discipline.

### 2. `ImportState` corrupts state — duplicates servers on first apply — FIXED

**Confidence:** 100

`internal/resource_server_group/crud.go` seeded only `id`+`name`
on import. After import, `Read` ran `modelToGroup` with `replicas=0`
→ `Observe` iterated `0..0` → empty `slots` → next apply created fresh
servers; the pre-existing servers were not classified as orphans
(they're `complete=true`), so the operator ended up with double the
servers per slot.

**Fix:** `ImportState` now calls `client.ListByGroup`, partitions the
result with `hcloudx.PartitionBySlot`, derives `replicas` from the
highest slot label via `importedReplicaCount`, and seeds
`name`/`id`/`replicas` before the framework's refresh runs.

**Tests added:** `TestImportedReplicaCount_FromObservations` covers the
helper; `TestAccServerGroup_Import` exercises the round-trip via the
framework's `ImportStateVerify=true` (with `ImportStateVerifyIgnore`
for desired-state attributes that aren't recoverable from labels —
those must be supplied via HCL after import, which is the standard
tofu import workflow).

**Why no test caught it earlier:** there was no acceptance test for the
import flow — partly because the implementation couldn't round-trip.
See task #27 on lifecycle round-trip / import audit.

### 3. `replace_on_change` hashes attribute *names*, not values — FIXED (BREAKING)

**Confidence:** 95

`extrasFromReplaceOnChange` wrote `out[name] = "1"`. The hash flipped
when the *list* changed, not when the *value of a listed attribute*
changed. The schema description promised the latter.

**Fix:** `replaceOnChangeResolvers` is a small registry mapping each
recognized attribute name to a function that serializes the model's
current value. `extrasFromReplaceOnChange` now resolves each listed
name to its serialized value (and rejects unknown names with a
plan-time `AddAttributeError`).

**Breaking change:** unknown attribute names that previously silently
no-op'd now hard-fail at plan time. Operators with typos in
`replace_on_change` will need to fix them — the spec contract is
preserved, and silently accepting typos was a footgun.

**Tests added:** `TestExtrasFromReplaceOnChange_ValueChangeFlipsExtras`,
`TestExtrasFromReplaceOnChange_UnknownNameDiagnoses`, and
`TestExtrasFromReplaceOnChange_ListAndMapValuesContribute` cover the
new behavior (the original order-independence and empty-input tests
remain).

**Why no test caught it earlier:** the original test only asserted
order-independence and empty-input behavior — never asserted that a
*value change* on a listed attribute changed the resulting extras map.
See task #30 on property-based hash audits.

### 4. `SetProviderLabel` (the `complete=true` flip) is not retried — FIXED

**Confidence:** 89

`internal/reconciler/slot.go` called `hcloudx.SetProviderLabel` bare.
Both `CreateServer` and `DeleteServer` in the same file are wrapped in
`hcloudx.Retry`. A transient network blip during the label flip caused
immediate failure → slot marked failed → next apply destroyed the
healthy server it was about to bless.

**Fix:** wrapped the call in `hcloudx.Retry`. The GET+PUT pair is
idempotent.

**Tests added:** `TestApply_RetriesTransientCompleteLabelFlip` injects
one transient `UpdateServerLabels` failure and asserts the slot still
converges to Ready.

**Why no test caught it earlier:** existing retry tests only exercised
`CreateServer`/`DeleteServer` failures and never the
`UpdateServerLabels` callsite. See task #25 on cross-callsite consistency.

### 5. `GetServer` re-fetch after `CreateServer` silently swallows errors — FIXED

**Confidence:** 85

`internal/reconciler/slot.go` had:

```go
if reread, gerr := r.client.GetServer(ctx, srv.ID); gerr == nil && reread != nil {
    srv = reread
}
```

If the re-read failed (transient 5xx, rate limit), the original `srv`
was used. The original CreateServer response doesn't have the
`private_net` populated (Hetzner attaches asynchronously), so
`findPrivateIP` returned `""` and the slot's `ip_private` was silently
empty. Templates and probe env vars relying on `HCLOUDGROUP_PRIVATE_IP`
got an empty string with no diagnostic.

**Fix:** the re-read is now wrapped in `hcloudx.Retry`. Transient errors
retry through the budget; a final failure marks the slot
`server_get_after_create` failed instead of silently using a
half-populated server.

**Tests added:** `TestApply_RetriesTransientGetServerAfterCreate`
injects one transient `GetServer` failure and asserts the slot still
gets a populated `PrivateIP`.

**Why no test caught it earlier:** the existing retry tests didn't
exercise the post-create `GetServer` callsite at all. See tasks #25
(cross-callsite consistency) and #26 (silent-failure audit).

### 6. `user_data_template` parse errors fail at apply time, not plan time — FIXED

**Confidence:** 85

README §11 said "Template render error (user_data_template): fail at
provider plan time." There was no `ValidateConfig` hook. Template
parsing happened during `Create`/`Update`. A malformed template
failed after API calls had already run for earlier slots.

**Fix:** added `resource.ResourceWithValidateConfig` to the resource;
the implementation calls a new `template.Parse` helper that parses
without rendering. Plan-time syntactic errors now surface as
`AddAttributeError` on `path.Root("user_data_template")`.

**Tests added:** `TestParse_*` suite in `internal/template`;
`TestAccServerGroup_RejectsBadUserDataTemplate` is a plan-only
acceptance step that asserts a malformed template fails the plan with
an "Invalid user_data_template" error.

**Why no test caught it earlier:** the README §11 contract was never
mapped to a test. See task #24 on README spec-conformance audit.

### 7. Concurrent applies from separate state backends destroy in-flight servers — OPEN

**Confidence:** 88

`preflightTargets` treats every `complete=false` server as an orphan,
regardless of which apply is creating it. If two operators run
concurrently against the same group name in the same hcloud project
(both bypassing tofu's state lock by using separate state backends),
WS2's preflight destroys WS1's mid-create server.

README §5.5 acknowledges single-writer is the design assumption, but
the failure mode is silent and cascading. The provider could detect a
foreign in-flight create (e.g., by attaching a per-applier nonce label
on `ServerCreate`) and refuse to clean it up.

**Status:** intentionally not fixed in this PR — the lowest-priority
finding, the fix is invasive, and the documented usage explicitly
forbids the trigger. See task #29 on concurrent / multi-actor review
for follow-up.

### 8. `SingleNestedBlock` workaround loses diagnostic path quality — FIXED

**Confidence:** 90

The `Required→Optional` workaround in `schema_blocks.go` (still required
against `terraform-plugin-framework v1.19.0` per the closed
[issue #740](https://github.com/hashicorp/terraform-plugin-framework/issues/740))
moved command/timeout validation from plan-time to convert-time. The
runtime errors from `commandActionFromBlock` used `diags.AddError` (no
path), so operators saw a resource-level message with no pointer to the
offending block.

**Fix:** `actionFromBlock`, `commandActionFromBlock`, and
`readinessFromBlock` now take a `path.Path` argument; the convert-time
diagnostics use `AddAttributeError` with `blockPath.AtName("command")
.AtName(field)`.

**Tests added:** `TestActionFromBlock_DiagnosticHasAttributePath`
asserts the resulting diagnostic carries the expected attribute path.

**Why no test caught it earlier:** existing tests asserted only that
errors were *produced* (via `diags.HasError()`) — never that they
carried a path. See task #28 on diagnostic UX audit.

A fuller fix is to switch action blocks to `ListNestedBlock` with
`listvalidator.SizeAtMost(1)` — the framework's documented escape
hatch — but that changes the HCL surface slightly and is out of scope.

---

## Test-suite findings (already addressed)

For traceability, the following review findings about the *test* code
were fixed in the same PR:

- `commandActionFromBlock` returning a half-built `*actions.Command`
  alongside error diagnostics → added `if diags.HasError() { return Null{}, diags }` guard.
  (Same fix in `readinessFromBlock`.)
- `Check` after `ExpectError` is dead code — the orphan-server assertion
  in `TestAccServerGroup_ReadinessProbeFailureLeavesIncomplete` step 1
  was unreachable. Moved to step 2's `PreConfig`.
- Sweeper deleted shared fixtures (network, ssh key, jump host) by
  exact name, no label gate. Added a `tfacc.io/fixture=shared` identity
  label at creation time and made the sweeper require it; refused to
  adopt a same-named pre-existing fixture without the label.
- Sweeper deleted the Network without waiting for in-flight server
  delete actions → silent failure → leaked Network. Now waits.
- Made `make testacc` use `-p 1` so cross-package binaries don't
  bootstrap/teardown the same shared fixtures concurrently.
- `TestDestroy_BeforeRemoveFailure_ReturnsPartialState` discarded the
  returned state with `_ = state`. Added assertions on slot 0 (Ready,
  unmodified) and slot 1 (Failed, error propagated).
