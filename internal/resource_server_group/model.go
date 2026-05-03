package resource_server_group

import (
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// resourceModel mirrors the resource's HCL schema in Go. Field tags map
// to attribute names; nested blocks are pointers so absence is
// distinguishable from explicit-nil.
type resourceModel struct {
	ID                types.String   `tfsdk:"id"`
	Name              types.String   `tfsdk:"name"`
	Count             types.Int64    `tfsdk:"count"`
	Image             types.String   `tfsdk:"image"`
	ServerType        types.String   `tfsdk:"server_type"`
	Location          types.String   `tfsdk:"location"`
	NetworkID         types.Int64    `tfsdk:"network_id"`
	SSHKeys           types.List     `tfsdk:"ssh_keys"`
	Labels            types.Map      `tfsdk:"labels"`
	UserDataTemplate  types.String   `tfsdk:"user_data_template"`
	ReplaceOnChange   types.Set      `tfsdk:"replace_on_change"`
	Slots             types.List     `tfsdk:"slots"`
	CurrentReplaceHash types.String  `tfsdk:"current_replace_hash"`

	BeforeCreate   *actionBlock        `tfsdk:"before_create"`
	PostCreate     *actionBlock        `tfsdk:"post_create"`
	BeforeReplace  *actionBlock        `tfsdk:"before_replace"`
	PostReplace    *actionBlock        `tfsdk:"post_replace"`
	BeforeRemove   *actionBlock        `tfsdk:"before_remove"`
	PostRemove     *actionBlock        `tfsdk:"post_remove"`
	ReadinessProbe *readinessProbeBlock `tfsdk:"readiness_probe"`

	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

// actionBlock is a single-nested block holding either a command (the only
// non-null variant in v1) or nothing (the schema treats absent as null
// action).
type actionBlock struct {
	Command *commandBlock `tfsdk:"command"`
}

// commandBlock is the shell-exec action descriptor. Timeout is a string in
// HCL (parsed with time.ParseDuration) so operators can write "30s",
// "5m", etc.
type commandBlock struct {
	Command      types.String `tfsdk:"command"`
	Env          types.Map    `tfsdk:"env"`
	Stdin        types.String `tfsdk:"stdin"`
	WorkingDir   types.String `tfsdk:"working_dir"`
	ExpectedExit types.Set    `tfsdk:"expected_exit"`
	Timeout      types.String `tfsdk:"timeout"`
}

// readinessProbeBlock wraps a commandBlock plus the polling parameters.
type readinessProbeBlock struct {
	Command          *probeCommandBlock `tfsdk:"command"`
}

type probeCommandBlock struct {
	Command          types.String `tfsdk:"command"`
	Env              types.Map    `tfsdk:"env"`
	Stdin            types.String `tfsdk:"stdin"`
	WorkingDir       types.String `tfsdk:"working_dir"`
	ExpectedExit     types.Set    `tfsdk:"expected_exit"`
	Timeout          types.String `tfsdk:"timeout"`
	Interval         types.String `tfsdk:"interval"`
	SuccessThreshold types.Int64  `tfsdk:"success_threshold"`
	TotalTimeout     types.String `tfsdk:"total_timeout"`
}

// slotModel is one entry in the computed `slots` list.
type slotModel struct {
	SlotID      types.Int64  `tfsdk:"slot_id"`
	ServerID    types.Int64  `tfsdk:"server_id"`
	ServerName  types.String `tfsdk:"server_name"`
	Generation  types.Int64  `tfsdk:"generation"`
	ReplaceHash types.String `tfsdk:"replace_hash"`
	PrivateIP   types.String `tfsdk:"ip_private"`
	Status      types.String `tfsdk:"status"`
	LastError   types.String `tfsdk:"last_error"`
}
