// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0

// Package provider declares the hcloudgroup terraform/opentofu provider:
// schema, configuration, and resource registration.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/servergroup"
)

// HCloudGroupProvider is the framework provider type.
type HCloudGroupProvider struct {
	Version string
}

// providerModel mirrors the provider's HCL schema. Token is sensitive;
// endpoint is optional and defaults to the public hcloud API.
type providerModel struct {
	HCloudToken    types.String `tfsdk:"hcloud_token"`
	HCloudEndpoint types.String `tfsdk:"hcloud_endpoint"`
}

// New returns a provider factory bound to a release version string. Used
// from main.go.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &HCloudGroupProvider{Version: version} }
}

// Metadata sets the provider type name (governs the resource type prefix
// "hcloudgroup_*").
func (p *HCloudGroupProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hcloudgroup"
	resp.Version = p.Version
}

// Schema declares provider-level configuration.
func (p *HCloudGroupProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages groups of Hetzner Cloud servers as a single rolling-replace unit.",
		Attributes: map[string]schema.Attribute{
			"hcloud_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Hetzner Cloud API token. Defaults to the HCLOUD_TOKEN env var.",
			},
			"hcloud_endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Hetzner Cloud API endpoint. Defaults to https://api.hetzner.cloud/v1.",
			},
		},
	}
}

// Configure constructs the hcloud client. It pulls the token from HCL
// first, falling back to the HCLOUD_TOKEN env var. Missing tokens are a
// configuration error.
func (p *HCloudGroupProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := cfg.HCloudToken.ValueString()
	if token == "" {
		token = os.Getenv("HCLOUD_TOKEN")
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("hcloud_token"),
			"Missing hcloud_token",
			"Set the hcloud_token provider attribute or the HCLOUD_TOKEN environment variable.",
		)
		return
	}

	opts := []hcloud.ClientOption{hcloud.WithToken(token)}
	if ep := cfg.HCloudEndpoint.ValueString(); ep != "" {
		opts = append(opts, hcloud.WithEndpoint(ep))
	}

	client := hcloudx.NewReal(hcloud.NewClient(opts...))

	// Resources receive the hcloudx.Client interface; tests substitute a
	// fake by replacing the resource's Client field directly.
	var iface hcloudx.Client = client
	resp.ResourceData = iface
	resp.DataSourceData = iface
}

// Resources lists every concrete resource type the provider exposes.
func (p *HCloudGroupProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		servergroup.New,
	}
}

// DataSources is empty in v1; the read-only data source is a follow-up.
func (p *HCloudGroupProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
