// Package servergroup implements the hcloudgroup_server_group terraform
// resource. It is a thin adapter between the framework's CRUD callbacks
// and the reconciler package, which owns the actual lifecycle logic.
package servergroup

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	tmpl "github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/template"
)

// Compile-time interface checks ensure the resource implements the
// framework optional interfaces we rely on.
var (
	_ resource.Resource                   = (*ServerGroupResource)(nil)
	_ resource.ResourceWithConfigure      = (*ServerGroupResource)(nil)
	_ resource.ResourceWithImportState    = (*ServerGroupResource)(nil)
	_ resource.ResourceWithValidateConfig = (*ServerGroupResource)(nil)
)

// ServerGroupResource is the framework resource type. The hcloud client
// is injected at Configure time from the provider.
type ServerGroupResource struct {
	Client hcloudx.Client
}

// New constructs the resource. Used in provider.Resources.
func New() resource.Resource { return &ServerGroupResource{} }

// Metadata sets the type name visible in HCL: hcloudgroup_server_group.
func (r *ServerGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_group"
}

// Configure receives the shared hcloud client from the provider. It is
// called before any CRUD method; nil ProviderData means the framework is
// validating only and we should leave Client unset.
func (r *ServerGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(hcloudx.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type",
			fmt.Sprintf("expected hcloudx.Client, got %T", req.ProviderData))
		return
	}
	r.Client = c
}

// ValidateConfig surfaces errors that don't depend on plan/state values
// at plan time, before any server is created. Currently this parses
// user_data_template (no rendering) so a syntactic error in the template
// fails the plan rather than failing mid-apply after some slots have
// already been created (README §11).
func (r *ServerGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m resourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.UserDataTemplate.IsNull() || m.UserDataTemplate.IsUnknown() {
		return
	}
	if err := tmpl.Parse(m.UserDataTemplate.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("user_data_template"),
			"Invalid user_data_template",
			err.Error(),
		)
	}
}
