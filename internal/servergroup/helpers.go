package servergroup

import (
	"context"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

// contextWithDeadline returns a derived context bounded by d (or the
// original context if d is non-positive).
func contextWithDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func itoa(i int) string { return strconv.Itoa(i) }

// importNamePath returns the path to the `name` attribute used by
// ImportState. Defined as a function to keep the import dependency
// pinned to one place.
func importNamePath() path.Path { return path.Root("name") }
func importIDPath() path.Path   { return path.Root("id") }

// newProgressFn returns a reconciler.ProgressFn that writes the running
// state snapshot back into tofu state after each slot transition. The
// callback writes the slots list and the current_replace_hash; it never
// touches HCL inputs (those are immutable across an apply).
//
// We capture *plan and pointers to the resource framework's response
// state and diagnostics so the closure can mutate them. This is
// expressly the pattern recommended by the framework for partial-state
// reporting on error.
func newProgressFn(_ context.Context, plan *resourceModel, hashFull string, state *tfsdk.State, diags *diag.Diagnostics) reconciler.ProgressFn {
	return func(ctx context.Context, snapshot reconciler.State) error {
		slotsVal, d := stateToSlotsValue(ctx, snapshot)
		diags.Append(d...)
		plan.ID = types.StringValue(plan.Name.ValueString())
		plan.CurrentReplaceHash = types.StringValue(hashFull)
		plan.Slots = slotsVal
		diags.Append(state.Set(ctx, plan)...)
		return nil
	}
}
