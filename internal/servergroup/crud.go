// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

// Create handles the resource's initial provisioning. It builds a Group
// from the plan, runs reconciler.Apply (which from-empty becomes
// phaseCreate), and writes the resulting State and computed attributes
// back to tofu state.
func (r *ServerGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, hashFull, _, d := modelToGroup(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	createDefault, _, _ := DefaultTimeouts()
	createTimeout, d := plan.Timeouts.Create(ctx, createDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := contextWithDeadline(ctx, createTimeout)
	defer cancel()

	progress := newProgressFn(ctx, &plan, hashFull, &resp.State, &resp.Diagnostics)

	rec := reconciler.New(r.Client)
	state, applyErr := rec.Apply(ctx, group, reconciler.State{}, progress)

	plan.ID = types.StringValue(group.Name)
	plan.CurrentReplaceHash = types.StringValue(hashFull)
	slotsVal, sd := stateToSlotsValue(ctx, state)
	resp.Diagnostics.Append(sd...)
	plan.Slots = slotsVal

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	if applyErr != nil {
		appendApplyError(&resp.Diagnostics, applyErr)
	}
}

// Read reconstructs the resource's state from labels on the live hcloud
// servers. It does not destroy orphans or mutate hcloud state.
func (r *ServerGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	priorState, d := slotsValueToState(ctx, prior.Slots)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, hashFull, _, d := modelToGroup(ctx, prior)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	rec := reconciler.New(r.Client)
	observed, err := rec.Observe(ctx, group, priorState)
	if err != nil {
		resp.Diagnostics.AddError("read failed", err.Error())
		return
	}

	if shouldRemoveResource(observed, priorState) {
		resp.State.RemoveResource(ctx)
		return
	}

	prior.ID = types.StringValue(group.Name)
	prior.CurrentReplaceHash = types.StringValue(hashFull)
	slotsVal, sd := stateToSlotsValue(ctx, observed)
	resp.Diagnostics.Append(sd...)
	prior.Slots = slotsVal

	// Normalize ReplaceMethod to the schema default when state never had
	// one set. Two paths land here with a null value:
	//   1. Post-import: ImportState only seeds name/id/replicas; the
	//      attribute is null until the framework's auto-Read populates it.
	//      Without this, ImportStateVerify diffs `replace_method` between
	//      the imported state (null) and a fresh Read on the same config
	//      (default-populated), and acctest fails.
	//   2. v0.1.x state upgrade: pre-attribute state has no value; the
	//      first Read after upgrade must set it so the next plan doesn't
	//      see a phantom diff. Documented silent-switch tradeoff in the
	//      v0.4.0 CHANGELOG / README §6.2.
	if prior.ReplaceMethod.IsNull() || prior.ReplaceMethod.IsUnknown() {
		prior.ReplaceMethod = types.StringValue(reconciler.ReplaceMethodCreateBeforeDestroy)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &prior)...)
}

// Update reconciles the desired plan against current state, walking the
// preflight + remove + replace + create phases.
func (r *ServerGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	priorState, d := slotsValueToState(ctx, prior.Slots)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, hashFull, _, d := modelToGroup(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, updateDefault, _ := DefaultTimeouts()
	updateTimeout, d := plan.Timeouts.Update(ctx, updateDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := contextWithDeadline(ctx, updateTimeout)
	defer cancel()

	progress := newProgressFn(ctx, &plan, hashFull, &resp.State, &resp.Diagnostics)
	rec := reconciler.New(r.Client)
	state, applyErr := rec.Apply(ctx, group, priorState, progress)

	plan.ID = types.StringValue(group.Name)
	plan.CurrentReplaceHash = types.StringValue(hashFull)
	slotsVal, sd := stateToSlotsValue(ctx, state)
	resp.Diagnostics.Append(sd...)
	plan.Slots = slotsVal

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	if applyErr != nil {
		appendApplyError(&resp.Diagnostics, applyErr)
	}
}

// Delete walks every slot through the REMOVE FLOW, then sweeps any
// orphans the labels reveal.
func (r *ServerGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var prior resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	priorState, d := slotsValueToState(ctx, prior.Slots)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, _, _, d := modelToGroup(ctx, prior)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, _, deleteDefault := DefaultTimeouts()
	deleteTimeout, d := prior.Timeouts.Delete(ctx, deleteDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := contextWithDeadline(ctx, deleteTimeout)
	defer cancel()

	rec := reconciler.New(r.Client)
	_, err := rec.Destroy(ctx, group, priorState, nil)
	if err != nil {
		appendApplyError(&resp.Diagnostics, err)
		return
	}
}

// ImportState supports `terraform import` by group name. The import id is
// the group name. We discover existing servers via the labels selector,
// derive `replicas` from the highest slot label, and seed name/id/replicas
// so the framework's subsequent Read can populate `slots` correctly.
//
// Without seeding replicas, Read would call Observe with replicas=0 and
// produce an empty slot list; the next apply would treat the existing
// servers as foreign (they're labeled complete=true so pre-flight does
// not destroy them) and create fresh servers, leaving the operator with
// double the servers per slot.
//
// The user-required attributes (image, server_type, location, network_id,
// etc.) are not seeded — those must be supplied via HCL after import,
// which is the standard tofu import flow ("import then write the resource
// block"). Plan will then surface any drift between HCL and the imported
// servers as a rolling-replace diff.
func (r *ServerGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.Client == nil {
		resp.Diagnostics.AddError("import: provider not configured",
			"the provider must be configured before importing; this typically means HCLOUD_TOKEN is missing")
		return
	}
	servers, err := r.Client.ListByGroup(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("import: list servers", err.Error())
		return
	}
	replicas := importedReplicaCount(hcloudx.PartitionBySlot(servers))
	if replicas == 0 {
		resp.Diagnostics.AddError("import: no servers found",
			"no hcloud servers carry the labels for group "+req.ID+
				" (managed-by=hcloudgroup-provider, group="+req.ID+"); nothing to import")
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, importNamePath(), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, importIDPath(), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("replicas"), int64(replicas))...)
}

// shouldRemoveResource decides whether Read should drop the resource
// from tofu state because every managed server has been deleted
// out-of-band. The guard on prior.Slots prevents accidental removal
// during a freshly-imported resource whose slots attribute is not yet
// populated (ImportState seeds only name/id/replicas; the framework's
// subsequent Read populates slots from labels). Idiomatic Terraform
// behavior on "resource gone" is RemoveResource so the next plan
// presents as `+ create` rather than `~ update`.
func shouldRemoveResource(observed reconciler.State, prior reconciler.State) bool {
	return len(observed.Slots) == 0 && len(prior.Slots) > 0
}

// importedReplicaCount derives the desired replica count from the highest
// slot index seen across the imported servers. Slot indices are zero-based
// so a single server at slot 0 yields replicas=1. Servers missing a slot
// label are ignored (they may be foreign even though the selector matched
// the group label).
func importedReplicaCount(observed map[int][]hcloudx.Observation) int {
	highest := -1
	for slotID := range observed {
		if slotID > highest {
			highest = slotID
		}
	}
	return highest + 1
}

func appendApplyError(diags *diag.Diagnostics, err error) {
	var slotErr *reconciler.SlotError
	if errors.As(err, &slotErr) {
		diags.AddError(
			slotErr.Phase+" failed on slot "+itoa(slotErr.SlotID),
			"cause: "+slotErr.Cause.Error()+
				diagAppendIfNotEmpty("\nstdout:\n", slotErr.Stdout)+
				diagAppendIfNotEmpty("\nstderr:\n", slotErr.Stderr),
		)
		return
	}
	diags.AddError("apply failed", err.Error())
}

func diagAppendIfNotEmpty(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}
