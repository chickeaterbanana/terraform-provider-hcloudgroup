package resource_server_group

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

// modelToGroup builds the desired-state input for the reconciler from the
// HCL plan. It returns the group together with the (full, prefix) hash
// pair so the caller can also write current_replace_hash back to state.
func modelToGroup(ctx context.Context, m resourceModel) (reconciler.Group, string, string, diag.Diagnostics) {
	var diags diag.Diagnostics

	sshKeys, d := stringList(ctx, m.SSHKeys)
	diags.Append(d...)
	labels, d := stringMap(ctx, m.Labels)
	diags.Append(d...)
	replaceOnChange, d := stringSet(ctx, m.ReplaceOnChange)
	diags.Append(d...)

	if diags.HasError() {
		return reconciler.Group{}, "", "", diags
	}

	g := reconciler.Group{
		Name:             m.Name.ValueString(),
		Count:            int(m.Count.ValueInt64()),
		Image:            m.Image.ValueString(),
		ServerType:       m.ServerType.ValueString(),
		Location:         m.Location.ValueString(),
		NetworkID:        m.NetworkID.ValueInt64(),
		SSHKeyNames:      sshKeys,
		UserLabels:       labels,
		UserDataTemplate: m.UserDataTemplate.ValueString(),
	}

	hi := reconciler.HashInputs{
		Image:            g.Image,
		ServerType:       g.ServerType,
		UserDataTemplate: g.UserDataTemplate,
		NetworkID:        g.NetworkID,
		Location:         g.Location,
		SSHKeys:          append([]string(nil), sshKeys...),
		Labels:           cloneMap(labels),
		Extras:           extrasFromReplaceOnChange(replaceOnChange),
	}
	full, prefix := hi.Hash()
	g.HashFull = full
	g.HashPrefix = prefix

	g.Actions = reconciler.ActionSet{
		BeforeCreate:  actionFromBlock(m.BeforeCreate),
		PostCreate:    actionFromBlock(m.PostCreate),
		BeforeReplace: actionFromBlock(m.BeforeReplace),
		PostReplace:   actionFromBlock(m.PostReplace),
		BeforeRemove:  actionFromBlock(m.BeforeRemove),
		PostRemove:    actionFromBlock(m.PostRemove),
	}

	probe, d := readinessFromBlock(ctx, m.ReadinessProbe)
	diags.Append(d...)
	g.ReadinessProbe = probe

	return g, full, prefix, diags
}

// extrasFromReplaceOnChange turns the user-supplied list of attribute
// names into a map suitable for HashInputs.Extras. Each name becomes a
// hash-affecting key; the value side is the literal string "1" so the
// presence/absence (and ordering) of the list itself flips the hash.
//
// In v1 every attribute the operator might list is already in the
// always-on hash set, so this exists primarily as an explicit "force
// replace" knob: adding or removing names from the list flips the hash
// even when nothing else changed.
func extrasFromReplaceOnChange(names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, n := range sorted {
		out[n] = "1"
	}
	return out
}

func actionFromBlock(b *actionBlock) actions.Action {
	if b == nil || b.Command == nil {
		return actions.Null{}
	}
	return commandActionFromBlock(b.Command)
}

func commandActionFromBlock(c *commandBlock) actions.Action {
	if c == nil {
		return actions.Null{}
	}
	timeout := mustParseDuration(c.Timeout.ValueString())
	return &actions.Command{
		Command:      c.Command.ValueString(),
		Env:          stringMapValueOrNil(c.Env),
		Stdin:        c.Stdin.ValueString(),
		WorkingDir:   c.WorkingDir.ValueString(),
		ExpectedExit: int64SetToInts(c.ExpectedExit),
		Timeout:      timeout,
	}
}

func readinessFromBlock(_ context.Context, b *readinessProbeBlock) (*actions.ReadinessProbe, diag.Diagnostics) {
	var diags diag.Diagnostics
	if b == nil || b.Command == nil {
		return nil, diags
	}
	c := b.Command
	timeout := mustParseDuration(c.Timeout.ValueString())
	interval := mustParseDuration(c.Interval.ValueString())
	totalTimeout := mustParseDuration(c.TotalTimeout.ValueString())
	threshold := int(c.SuccessThreshold.ValueInt64())
	if threshold < 1 {
		threshold = 1
	}
	return &actions.ReadinessProbe{
		Command: actions.Command{
			Command:      c.Command.ValueString(),
			Env:          stringMapValueOrNil(c.Env),
			Stdin:        c.Stdin.ValueString(),
			WorkingDir:   c.WorkingDir.ValueString(),
			ExpectedExit: int64SetToInts(c.ExpectedExit),
			Timeout:      timeout,
		},
		Interval:         interval,
		SuccessThreshold: threshold,
		TotalTimeout:     totalTimeout,
	}, diags
}

func mustParseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// Validators reject this at plan time; if we somehow get here,
		// fail loudly via a sentinel that the runner will surface.
		return 0
	}
	return d
}

// --- low-level conversions -------------------------------------------

func stringList(ctx context.Context, l types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if l.IsNull() || l.IsUnknown() {
		return nil, diags
	}
	out := make([]string, 0, len(l.Elements()))
	d := l.ElementsAs(ctx, &out, false)
	diags.Append(d...)
	return out, diags
}

func stringSet(ctx context.Context, s types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if s.IsNull() || s.IsUnknown() {
		return nil, diags
	}
	out := make([]string, 0, len(s.Elements()))
	d := s.ElementsAs(ctx, &out, false)
	diags.Append(d...)
	return out, diags
}

func stringMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if m.IsNull() || m.IsUnknown() {
		return nil, diags
	}
	out := make(map[string]string, len(m.Elements()))
	d := m.ElementsAs(ctx, &out, false)
	diags.Append(d...)
	return out, diags
}

func stringMapValueOrNil(m types.Map) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := make(map[string]string, len(m.Elements()))
	for k, v := range m.Elements() {
		sv, ok := v.(types.String)
		if !ok {
			continue
		}
		out[k] = sv.ValueString()
	}
	return out
}

func int64SetToInts(s types.Set) []int {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	out := make([]int, 0, len(s.Elements()))
	for _, e := range s.Elements() {
		iv, ok := e.(types.Int64)
		if !ok {
			continue
		}
		out = append(out, int(iv.ValueInt64()))
	}
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// --- state encoding helpers ------------------------------------------

// slotsAttrTypes returns the attr.Type description of a single slot
// object. Used wherever we serialize the computed `slots` list back into
// terraform state.
func slotsAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"slot_id":      types.Int64Type,
		"server_id":    types.Int64Type,
		"server_name":  types.StringType,
		"generation":   types.Int64Type,
		"replace_hash": types.StringType,
		"ip_private":   types.StringType,
		"status":       types.StringType,
		"last_error":   types.StringType,
	}
}

// stateToSlotsValue converts a reconciler.State into the types.List value
// that goes into the resource's `slots` attribute.
func stateToSlotsValue(ctx context.Context, st reconciler.State) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	objType := types.ObjectType{AttrTypes: slotsAttrTypes()}
	if len(st.Slots) == 0 {
		v, d := types.ListValue(objType, []attr.Value{})
		diags.Append(d...)
		return v, diags
	}
	values := make([]attr.Value, 0, len(st.Slots))
	for _, sl := range st.Slots {
		obj, d := types.ObjectValue(slotsAttrTypes(), map[string]attr.Value{
			"slot_id":      types.Int64Value(int64(sl.SlotID)),
			"server_id":    types.Int64Value(sl.ServerID),
			"server_name":  types.StringValue(sl.ServerName),
			"generation":   types.Int64Value(int64(sl.Generation)),
			"replace_hash": types.StringValue(sl.ReplaceHash),
			"ip_private":   types.StringValue(sl.PrivateIP),
			"status":       types.StringValue(sl.Status),
			"last_error":   types.StringValue(sl.LastError),
		})
		diags.Append(d...)
		values = append(values, obj)
	}
	v, d := types.ListValue(objType, values)
	diags.Append(d...)
	return v, diags
}

// slotsValueToState parses a previously-stored types.List back into a
// reconciler.State - used by Update and Delete to reconstruct prior
// state on entry.
func slotsValueToState(ctx context.Context, v types.List) (reconciler.State, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := reconciler.State{}
	if v.IsNull() || v.IsUnknown() {
		return out, diags
	}
	for i, elem := range v.Elements() {
		obj, ok := elem.(types.Object)
		if !ok {
			diags.AddError("slots decode", fmt.Sprintf("element %d is not an object", i))
			continue
		}
		attrs := obj.Attributes()
		out.Slots = append(out.Slots, reconciler.SlotState{
			SlotID:      int(intFromAttr(attrs["slot_id"])),
			ServerID:    intFromAttr(attrs["server_id"]),
			ServerName:  stringFromAttr(attrs["server_name"]),
			Generation:  int(intFromAttr(attrs["generation"])),
			ReplaceHash: stringFromAttr(attrs["replace_hash"]),
			PrivateIP:   stringFromAttr(attrs["ip_private"]),
			Status:      stringFromAttr(attrs["status"]),
			LastError:   stringFromAttr(attrs["last_error"]),
		})
	}
	return out, diags
}

func intFromAttr(a attr.Value) int64 {
	if a == nil {
		return 0
	}
	if iv, ok := a.(types.Int64); ok && !iv.IsNull() && !iv.IsUnknown() {
		return iv.ValueInt64()
	}
	return 0
}

func stringFromAttr(a attr.Value) string {
	if a == nil {
		return ""
	}
	if sv, ok := a.(types.String); ok && !sv.IsNull() && !sv.IsUnknown() {
		return sv.ValueString()
	}
	return ""
}
