package resource_server_group

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

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

	prior.ID = types.StringValue(group.Name)
	prior.CurrentReplaceHash = types.StringValue(hashFull)
	slotsVal, sd := stateToSlotsValue(ctx, observed)
	resp.Diagnostics.Append(sd...)
	prior.Slots = slotsVal

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

// ImportState supports `terraform import` by group name. We treat the
// import id as the group name; subsequent Read populates the rest from
// labels.
func (r *ServerGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, importNamePath(), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, importIDPath(), req.ID)...)
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
