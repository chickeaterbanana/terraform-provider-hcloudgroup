package resource_server_group

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
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

	extras, ed := extrasFromReplaceOnChange(replaceOnChange, m, sshKeys, labels)
	diags.Append(ed...)
	hi := reconciler.HashInputs{
		Image:            g.Image,
		ServerType:       g.ServerType,
		UserDataTemplate: g.UserDataTemplate,
		NetworkID:        g.NetworkID,
		Location:         g.Location,
		SSHKeys:          append([]string(nil), sshKeys...),
		Labels:           cloneMap(labels),
		Extras:           extras,
	}
	full, prefix := hi.Hash()
	g.HashFull = full
	g.HashPrefix = prefix

	for _, hook := range []struct {
		name string
		blk  *actionBlock
		dst  *actions.Action
	}{
		{"before_create", m.BeforeCreate, &g.Actions.BeforeCreate},
		{"post_create", m.PostCreate, &g.Actions.PostCreate},
		{"before_replace", m.BeforeReplace, &g.Actions.BeforeReplace},
		{"post_replace", m.PostReplace, &g.Actions.PostReplace},
		{"before_remove", m.BeforeRemove, &g.Actions.BeforeRemove},
		{"post_remove", m.PostRemove, &g.Actions.PostRemove},
	} {
		a, ad := actionFromBlock(hook.blk, path.Root(hook.name))
		diags.Append(ad...)
		*hook.dst = a
	}

	probe, d := readinessFromBlock(ctx, m.ReadinessProbe, path.Root("readiness_probe"))
	diags.Append(d...)
	g.ReadinessProbe = probe

	return g, full, prefix, diags
}

// replaceOnChangeResolvers maps each attribute name accepted in
// `replace_on_change` to a function that serializes the model's current
// value to a deterministic string. The resulting (name, serialized-value)
// pair is fed into HashInputs.Extras so the hash flips when the *value*
// of the listed attribute changes.
//
// Every attribute named here is also in the always-on hash set, so listing
// it in replace_on_change is currently redundant. The knob is kept as the
// documented extension point: future attributes added outside the always-
// on set must be plumbed through here so listing them works as advertised.
var replaceOnChangeResolvers = map[string]func(m resourceModel, sshKeys []string, labels map[string]string) string{
	"image":       func(m resourceModel, _ []string, _ map[string]string) string { return m.Image.ValueString() },
	"server_type": func(m resourceModel, _ []string, _ map[string]string) string { return m.ServerType.ValueString() },
	"location":    func(m resourceModel, _ []string, _ map[string]string) string { return m.Location.ValueString() },
	"network_id": func(m resourceModel, _ []string, _ map[string]string) string {
		return strconv.FormatInt(m.NetworkID.ValueInt64(), 10)
	},
	"user_data_template": func(m resourceModel, _ []string, _ map[string]string) string { return m.UserDataTemplate.ValueString() },
	"ssh_keys": func(_ resourceModel, sshKeys []string, _ map[string]string) string {
		return canonicalStringSlice(sshKeys)
	},
	"labels": func(_ resourceModel, _ []string, labels map[string]string) string { return canonicalStringMap(labels) },
}

// extrasFromReplaceOnChange resolves each name in replace_on_change to its
// current value via replaceOnChangeResolvers and produces the
// HashInputs.Extras map. Unknown names are rejected at plan time — the
// schema description promises "when changed, trigger a rolling replace",
// so silently accepting a typoed attribute name would be a footgun (the
// hash would never flip and the operator would assume the rolling replace
// is wired up when it isn't).
func extrasFromReplaceOnChange(names []string, m resourceModel, sshKeys []string, labels map[string]string) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(names) == 0 {
		return nil, diags
	}
	out := make(map[string]string, len(names))
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, n := range sorted {
		resolver, ok := replaceOnChangeResolvers[n]
		if !ok {
			diags.AddAttributeError(
				path.Root("replace_on_change"),
				"Unknown attribute in replace_on_change",
				fmt.Sprintf("%q is not a recognized attribute. Supported names: %s",
					n, knownReplaceOnChangeNames()),
			)
			continue
		}
		out[n] = resolver(m, sshKeys, labels)
	}
	return out, diags
}

func knownReplaceOnChangeNames() string {
	names := make([]string, 0, len(replaceOnChangeResolvers))
	for k := range replaceOnChangeResolvers {
		names = append(names, k)
	}
	sort.Strings(names)
	return "[" + strings.Join(names, ", ") + "]"
}

// canonicalStringSlice produces a stable representation of a string slice
// independent of input ordering.
//
// The marshal error path is unreachable for []string but consuming it
// silently would let two different inputs collapse to the empty string
// and produce identical hashes — so fall back to the error text the way
// hash.go does for the same reason.
func canonicalStringSlice(in []string) string {
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	b, err := json.Marshal(cp)
	if err != nil {
		return "marshal-error:" + err.Error()
	}
	return string(b)
}

// canonicalStringMap produces a stable representation of a string map
// independent of map iteration order.
func canonicalStringMap(in map[string]string) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([][2]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, [2]string{k, in[k]})
	}
	b, err := json.Marshal(pairs)
	if err != nil {
		return "marshal-error:" + err.Error()
	}
	return string(b)
}

// actionFromBlock returns actions.Null{} when the outer block or its
// inner command block is absent. When the inner command block is set,
// the schema treats `command` and `timeout` as Optional (a workaround
// for the framework's Required-attribute upward propagation); we
// validate their presence here at convert time. blockPath identifies the
// outer block (e.g., path.Root("before_create")) so diagnostics can
// point operators to the offending HCL.
func actionFromBlock(b *actionBlock, blockPath path.Path) (actions.Action, diag.Diagnostics) {
	var diags diag.Diagnostics
	if b == nil || b.Command == nil {
		return actions.Null{}, diags
	}
	cmd, d := commandActionFromBlock(b.Command, blockPath.AtName("command"))
	diags.Append(d...)
	return cmd, diags
}

func commandActionFromBlock(c *commandBlock, cmdPath path.Path) (actions.Action, diag.Diagnostics) {
	var diags diag.Diagnostics
	if c == nil {
		return actions.Null{}, diags
	}
	command := c.Command.ValueString()
	timeoutStr := c.Timeout.ValueString()
	if command == "" {
		diags.AddAttributeError(
			cmdPath.AtName("command"),
			"Missing required attribute: command",
			"the `command` attribute must be set when the command block is configured",
		)
	}
	if timeoutStr == "" {
		diags.AddAttributeError(
			cmdPath.AtName("timeout"),
			"Missing required attribute: timeout",
			"the `timeout` attribute must be set when the command block is configured",
		)
	}
	if diags.HasError() {
		// Don't return a half-built *actions.Command — a caller that ignores
		// diagnostics could end up running an empty command.
		return actions.Null{}, diags
	}
	return &actions.Command{
		Command:      command,
		Env:          stringMapValueOrNil(c.Env),
		Stdin:        c.Stdin.ValueString(),
		WorkingDir:   c.WorkingDir.ValueString(),
		ExpectedExit: int64SetToInts(c.ExpectedExit),
		Timeout:      mustParseDuration(timeoutStr),
	}, diags
}

func readinessFromBlock(_ context.Context, b *readinessProbeBlock, blockPath path.Path) (*actions.ReadinessProbe, diag.Diagnostics) {
	var diags diag.Diagnostics
	if b == nil || b.Command == nil {
		return nil, diags
	}
	c := b.Command
	command := c.Command.ValueString()
	timeoutStr := c.Timeout.ValueString()
	intervalStr := c.Interval.ValueString()
	totalStr := c.TotalTimeout.ValueString()
	cmdPath := blockPath.AtName("command")
	for _, p := range []struct {
		name string
		val  string
	}{
		{"command", command},
		{"timeout", timeoutStr},
		{"interval", intervalStr},
		{"total_timeout", totalStr},
	} {
		if p.val == "" {
			diags.AddAttributeError(
				cmdPath.AtName(p.name),
				"Missing required attribute: "+p.name,
				"the `"+p.name+"` attribute must be set when the readiness_probe.command block is configured",
			)
		}
	}
	if diags.HasError() {
		// Same reason as commandActionFromBlock: don't hand back a half-built
		// probe alongside an error.
		return nil, diags
	}
	threshold := int(c.SuccessThreshold.ValueInt64())
	if threshold < 1 {
		threshold = 1
	}
	return &actions.ReadinessProbe{
		Command: actions.Command{
			Command:      command,
			Env:          stringMapValueOrNil(c.Env),
			Stdin:        c.Stdin.ValueString(),
			WorkingDir:   c.WorkingDir.ValueString(),
			ExpectedExit: int64SetToInts(c.ExpectedExit),
			Timeout:      mustParseDuration(timeoutStr),
		},
		Interval:         mustParseDuration(intervalStr),
		SuccessThreshold: threshold,
		TotalTimeout:     mustParseDuration(totalStr),
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
