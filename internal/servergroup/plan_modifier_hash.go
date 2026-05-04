package servergroup

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// newReplaceHashPlanModifier returns the PlanModifier attached to
// current_replace_hash. The framework default for a Computed-only
// attribute is to mark it `(known after apply)` on every plan, which
// produces noisy diffs even when no hash input changed. This modifier
// recomputes the hash from plan inputs at plan time, so the planned
// value is concrete whenever every hash input is known.
//
// UseStateForUnknown is the wrong tool here: it would carry the *prior*
// hash forward when inputs change, hiding a real replace.
func newReplaceHashPlanModifier() planmodifier.String {
	return replaceHashPlanModifier{}
}

type replaceHashPlanModifier struct{}

func (replaceHashPlanModifier) Description(_ context.Context) string {
	return "Recomputes current_replace_hash from plan inputs at plan time so the value is known before apply."
}

func (m replaceHashPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (replaceHashPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Destroy plans have a null Plan; nothing to compute.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If any hash input is unknown (typically because it references
	// another resource's not-yet-known attribute), leave the planned
	// value unknown. The framework will recompute at apply time.
	if hasUnknownHashInputs(plan) {
		return
	}

	inputs, _, _, d := modelHashInputs(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	full, _ := inputs.Hash()
	resp.PlanValue = types.StringValue(full)
}

// hasUnknownHashInputs reports whether any attribute that contributes to
// the replace hash is still unknown. Replicas and Name are excluded — they
// are not hash inputs (replicas drives slot count, name drives the group
// label and is RequiresReplace).
func hasUnknownHashInputs(m resourceModel) bool {
	if m.Image.IsUnknown() ||
		m.ServerType.IsUnknown() ||
		m.Location.IsUnknown() ||
		m.NetworkID.IsUnknown() ||
		m.UserDataTemplate.IsUnknown() ||
		m.SSHKeys.IsUnknown() ||
		m.Labels.IsUnknown() ||
		m.ReplaceOnChange.IsUnknown() {
		return true
	}
	return false
}
