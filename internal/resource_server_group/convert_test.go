package resource_server_group

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

func mkStringList(vs ...string) types.List {
	elems := make([]attr.Value, 0, len(vs))
	for _, v := range vs {
		elems = append(elems, types.StringValue(v))
	}
	l, _ := types.ListValue(types.StringType, elems)
	return l
}

func mkStringMap(m map[string]string) types.Map {
	if m == nil {
		return types.MapNull(types.StringType)
	}
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	mv, _ := types.MapValue(types.StringType, elems)
	return mv
}

func mkStringSet(vs ...string) types.Set {
	elems := make([]attr.Value, 0, len(vs))
	for _, v := range vs {
		elems = append(elems, types.StringValue(v))
	}
	s, _ := types.SetValue(types.StringType, elems)
	return s
}

func TestModelToGroup_PopulatesFields(t *testing.T) {
	m := resourceModel{
		Name:             types.StringValue("consul"),
		Count:            types.Int64Value(3),
		Image:            types.StringValue("debian-13"),
		ServerType:       types.StringValue("cx22"),
		Location:         types.StringValue("fsn1"),
		NetworkID:        types.Int64Value(42),
		SSHKeys:          mkStringList("alice", "ops"),
		Labels:           mkStringMap(map[string]string{"env": "prod"}),
		UserDataTemplate: types.StringValue("#cloud-config\n"),
		ReplaceOnChange:  mkStringSet("user_data_template"),
	}

	group, hashFull, hashPrefix, diags := modelToGroup(context.Background(), m)
	require.False(t, diags.HasError())

	require.Equal(t, "consul", group.Name)
	require.Equal(t, 3, group.Count)
	require.Equal(t, "debian-13", group.Image)
	require.Equal(t, "cx22", group.ServerType)
	require.Equal(t, "fsn1", group.Location)
	require.Equal(t, int64(42), group.NetworkID)
	require.Equal(t, []string{"alice", "ops"}, group.SSHKeyNames)
	require.Equal(t, map[string]string{"env": "prod"}, group.UserLabels)
	require.Equal(t, "#cloud-config\n", group.UserDataTemplate)

	require.Len(t, hashFull, 64, "sha256 hex is 64 chars")
	require.Len(t, hashPrefix, reconciler.HashLabelLength)
	require.Equal(t, hashFull, group.HashFull)
	require.Equal(t, hashPrefix, group.HashPrefix)

	// All actions absent → all Null.
	require.IsType(t, actions.Null{}, group.Actions.BeforeCreate)
	require.IsType(t, actions.Null{}, group.Actions.PostCreate)
	require.IsType(t, actions.Null{}, group.Actions.BeforeReplace)
	require.IsType(t, actions.Null{}, group.Actions.PostReplace)
	require.IsType(t, actions.Null{}, group.Actions.BeforeRemove)
	require.IsType(t, actions.Null{}, group.Actions.PostRemove)
	require.Nil(t, group.ReadinessProbe, "absent readiness_probe → nil pointer")
}

func TestActionFromBlock_NilBecomesNull(t *testing.T) {
	a, diags := actionFromBlock(nil, path.Root("before_create"))
	require.False(t, diags.HasError())
	require.IsType(t, actions.Null{}, a)

	a, diags = actionFromBlock(&actionBlock{}, path.Root("before_create"))
	require.False(t, diags.HasError())
	require.IsType(t, actions.Null{}, a)
}

func TestActionFromBlock_CommandPopulatesFields(t *testing.T) {
	expectedExit, _ := types.SetValue(types.Int64Type, []attr.Value{types.Int64Value(0), types.Int64Value(2)})
	a, diags := actionFromBlock(&actionBlock{
		Command: &commandBlock{
			Command:      types.StringValue("echo hi"),
			Env:          mkStringMap(map[string]string{"PATH": "/bin"}),
			Stdin:        types.StringValue("input"),
			WorkingDir:   types.StringValue("/tmp"),
			ExpectedExit: expectedExit,
			Timeout:      types.StringValue("30s"),
		},
	}, path.Root("before_create"))
	require.False(t, diags.HasError())
	cmd, ok := a.(*actions.Command)
	require.True(t, ok, "command block must produce *actions.Command")
	require.Equal(t, "echo hi", cmd.Command)
	require.Equal(t, "/bin", cmd.Env["PATH"])
	require.Equal(t, "input", cmd.Stdin)
	require.Equal(t, "/tmp", cmd.WorkingDir)
	require.ElementsMatch(t, []int{0, 2}, cmd.ExpectedExit)
	require.Equal(t, 30*time.Second, cmd.Timeout)
}

func TestActionFromBlock_RejectsEmptyCommandOrTimeout(t *testing.T) {
	// Inner command block present but missing required-when-set fields.
	_, diags := actionFromBlock(&actionBlock{Command: &commandBlock{}}, path.Root("before_create"))
	require.True(t, diags.HasError(), "empty command + timeout must produce diagnostics")

	// Each diagnostic must carry a precise path so operators can locate
	// the offending HCL block instead of getting a resource-level error.
	for _, d := range diags.Errors() {
		require.NotNil(t, d, "diagnostic must not be nil")
		// AddAttributeError produces *attribute* diagnostics; their
		// Path() is non-empty (.Equal would return false against
		// path.Empty()). Spot-check by reflection through the framework:
		require.NotEmpty(t, d.Detail(), "every error needs a detail line operators can act on")
	}
}

// Command-block validation must surface AddAttributeError with a
// precise path.Path, not a path-less AddError. Without the path,
// operators see a resource-level error with no pointer to which of the
// six hook blocks is at fault.
func TestActionFromBlock_DiagnosticHasAttributePath(t *testing.T) {
	hookPath := path.Root("post_replace")
	_, diags := actionFromBlock(&actionBlock{Command: &commandBlock{}}, hookPath)
	require.True(t, diags.HasError())

	expected := hookPath.AtName("command").AtName("command").String()
	found := false
	for _, d := range diags.Errors() {
		// AttributeErrorDiagnostic implements .Path(); check via the
		// concrete type to avoid coupling tests to the framework's
		// internal interface taxonomy.
		type attrPathed interface{ Path() path.Path }
		if pd, ok := d.(attrPathed); ok && pd.Path().String() == expected {
			found = true
		}
	}
	require.True(t, found,
		"expected an attribute diagnostic at %q; got %#v", expected, diags.Errors())
}

func TestReadinessFromBlock_NilReturnsNil(t *testing.T) {
	probe, diags := readinessFromBlock(context.Background(), nil, path.Root("readiness_probe"))
	require.False(t, diags.HasError())
	require.Nil(t, probe)

	probe, diags = readinessFromBlock(context.Background(), &readinessProbeBlock{}, path.Root("readiness_probe"))
	require.False(t, diags.HasError())
	require.Nil(t, probe, "absent inner command → nil probe")
}

func TestReadinessFromBlock_PopulatesAllFields(t *testing.T) {
	probe, diags := readinessFromBlock(context.Background(), &readinessProbeBlock{
		Command: &probeCommandBlock{
			Command:          types.StringValue("true"),
			Env:              mkStringMap(map[string]string{"PATH": "/bin"}),
			Timeout:          types.StringValue("5s"),
			Interval:         types.StringValue("2s"),
			SuccessThreshold: types.Int64Value(3),
			TotalTimeout:     types.StringValue("1m"),
		},
	}, path.Root("readiness_probe"))
	require.False(t, diags.HasError())
	require.NotNil(t, probe)
	require.Equal(t, "true", probe.Command.Command)
	require.Equal(t, 5*time.Second, probe.Command.Timeout)
	require.Equal(t, 2*time.Second, probe.Interval)
	require.Equal(t, 3, probe.SuccessThreshold)
	require.Equal(t, time.Minute, probe.TotalTimeout)
}

func TestReadinessFromBlock_ClampsLowSuccessThreshold(t *testing.T) {
	probe, _ := readinessFromBlock(context.Background(), &readinessProbeBlock{
		Command: &probeCommandBlock{
			Command:          types.StringValue("true"),
			Timeout:          types.StringValue("1s"),
			Interval:         types.StringValue("1s"),
			SuccessThreshold: types.Int64Value(0),
			TotalTimeout:     types.StringValue("1m"),
		},
	}, path.Root("readiness_probe"))
	require.NotNil(t, probe)
	require.Equal(t, 1, probe.SuccessThreshold, "success_threshold < 1 must be clamped to 1")
}

// Inputs that vary only in attribute *order* must produce identical
// extras. Unsupported names are rejected by the resolver with a
// diagnostic, so callers don't accidentally pin a hash on a typo.
func TestExtrasFromReplaceOnChange_OrderIndependent(t *testing.T) {
	m := resourceModel{
		Image:      types.StringValue("debian-13"),
		ServerType: types.StringValue("cx22"),
	}
	a, da := extrasFromReplaceOnChange([]string{"image", "server_type"}, m, nil, nil)
	require.False(t, da.HasError())
	b, db := extrasFromReplaceOnChange([]string{"server_type", "image"}, m, nil, nil)
	require.False(t, db.HasError())
	require.Equal(t, a, b, "extras must produce a stable map regardless of input order")
}

func TestExtrasFromReplaceOnChange_EmptyIsNil(t *testing.T) {
	x, d := extrasFromReplaceOnChange(nil, resourceModel{}, nil, nil)
	require.False(t, d.HasError())
	require.Nil(t, x)
	x, d = extrasFromReplaceOnChange([]string{}, resourceModel{}, nil, nil)
	require.False(t, d.HasError())
	require.Nil(t, x)
}

// Changing the value of a listed attribute must change the extras map
// (and therefore the hash). The schema description promises "when
// changed, trigger a rolling replace", which requires hashing values,
// not just names.
func TestExtrasFromReplaceOnChange_ValueChangeFlipsExtras(t *testing.T) {
	m1 := resourceModel{Image: types.StringValue("debian-13")}
	m2 := resourceModel{Image: types.StringValue("ubuntu-24.04")}
	a, _ := extrasFromReplaceOnChange([]string{"image"}, m1, nil, nil)
	b, _ := extrasFromReplaceOnChange([]string{"image"}, m2, nil, nil)
	require.NotEqual(t, a, b, "different attribute values must produce different extras")
}

// Unknown attribute names must be rejected with a plan-time diagnostic so
// a typo in `replace_on_change` doesn't silently no-op.
func TestExtrasFromReplaceOnChange_UnknownNameDiagnoses(t *testing.T) {
	_, d := extrasFromReplaceOnChange([]string{"flavor_of_the_week"}, resourceModel{}, nil, nil)
	require.True(t, d.HasError(), "unknown attribute names must surface a plan-time error")
}

// Listing ssh_keys and labels must hash their *values*, not just their
// presence. Maps and slices serialize through the canonical-* helpers so
// iteration order doesn't perturb the hash.
func TestExtrasFromReplaceOnChange_ListAndMapValuesContribute(t *testing.T) {
	m := resourceModel{}
	a, _ := extrasFromReplaceOnChange([]string{"ssh_keys"}, m, []string{"alice", "bob"}, nil)
	b, _ := extrasFromReplaceOnChange([]string{"ssh_keys"}, m, []string{"bob", "alice"}, nil)
	require.Equal(t, a, b, "ssh_keys value must be canonicalized: order doesn't matter")

	c, _ := extrasFromReplaceOnChange([]string{"ssh_keys"}, m, []string{"alice", "carol"}, nil)
	require.NotEqual(t, a, c, "ssh_keys value change must flip extras")

	d, _ := extrasFromReplaceOnChange([]string{"labels"}, m, nil, map[string]string{"env": "prod"})
	e, _ := extrasFromReplaceOnChange([]string{"labels"}, m, nil, map[string]string{"env": "staging"})
	require.NotEqual(t, d, e, "labels value change must flip extras")
}

func TestSlotsValueRoundTrip_PreservesEveryField(t *testing.T) {
	in := reconciler.State{Slots: []reconciler.SlotState{
		{
			SlotID: 0, ServerID: 100, ServerName: "g-0-1",
			Generation: 1, ReplaceHash: "abc123",
			PrivateIP: "10.0.0.10", Status: reconciler.StatusReady,
			LastError: "",
		},
		{
			SlotID: 1, ServerID: 101, ServerName: "g-1-2",
			Generation: 2, ReplaceHash: "def456",
			PrivateIP: "10.0.0.11", Status: reconciler.StatusFailed,
			LastError: "boom",
		},
	}}

	v, diags := stateToSlotsValue(context.Background(), in)
	require.False(t, diags.HasError())

	out, diags := slotsValueToState(context.Background(), v)
	require.False(t, diags.HasError())
	require.Equal(t, in, out, "round-trip through types.List must preserve every field")
}

func TestSlotsValueRoundTrip_EmptyState(t *testing.T) {
	in := reconciler.State{}
	v, diags := stateToSlotsValue(context.Background(), in)
	require.False(t, diags.HasError())
	require.False(t, v.IsNull())

	out, diags := slotsValueToState(context.Background(), v)
	require.False(t, diags.HasError())
	require.Empty(t, out.Slots)
}
