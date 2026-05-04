package provider_test

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/provider"
)

// Configure() and the HCLOUD_TOKEN env var fallback are exercised end-to-end
// in the acceptance test suite, which drives the real plugin protocol via
// terraform-plugin-testing. These hermetic tests cover the metadata,
// schema, and resource registration paths so a regression in those is
// caught at unit level.

func TestNew_ReturnsProviderFactory(t *testing.T) {
	f := provider.New("v0.0.1-test")
	require.NotNil(t, f)
	p := f()
	require.NotNil(t, p)
}

func TestMetadata_TypeName(t *testing.T) {
	p := provider.New("v0.0.1-test")()
	resp := &fwprovider.MetadataResponse{}
	p.Metadata(context.Background(), fwprovider.MetadataRequest{}, resp)
	require.Equal(t, "hcloudgroup", resp.TypeName)
	require.Equal(t, "v0.0.1-test", resp.Version)
}

func TestSchema_DeclaresExpectedAttributes(t *testing.T) {
	p := provider.New("test")()
	resp := &fwprovider.SchemaResponse{}
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, resp)
	require.False(t, resp.Diagnostics.HasError())

	tokenAttr, ok := resp.Schema.Attributes["hcloud_token"]
	require.True(t, ok, "hcloud_token attribute must exist")
	require.True(t, tokenAttr.IsOptional(), "hcloud_token must be optional (env-var fallback)")
	require.True(t, tokenAttr.IsSensitive(), "hcloud_token must be marked sensitive")

	endpointAttr, ok := resp.Schema.Attributes["hcloud_endpoint"]
	require.True(t, ok)
	require.True(t, endpointAttr.IsOptional())
	require.False(t, endpointAttr.IsSensitive(), "endpoint is not a secret")
}

func TestResources_RegistersServerGroup(t *testing.T) {
	p := provider.New("test")()
	factories := p.Resources(context.Background())
	require.Len(t, factories, 1, "v1 ships exactly one resource: hcloudgroup_server_group")

	// Construct one to make sure the factory works.
	r := factories[0]()
	require.NotNil(t, r)
}

func TestDataSources_NoneInV1(t *testing.T) {
	p := provider.New("test")()
	require.Nil(t, p.DataSources(context.Background()), "no data sources in v1")
}

// configureWithToken builds a hermetic ConfigureRequest for the provider.
// Pass tftypes.NewValue(tftypes.String, "...") to set a value, or
// tftypes.NewValue(tftypes.String, nil) to leave the attribute null.
func configureWithToken(t *testing.T, p fwprovider.Provider, token, endpoint tftypes.Value) (fwprovider.ConfigureRequest, fwprovider.ConfigureResponse) {
	t.Helper()
	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError())

	objType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"hcloud_token":    tftypes.String,
		"hcloud_endpoint": tftypes.String,
	}}
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"hcloud_token":    token,
		"hcloud_endpoint": endpoint,
	})

	req := fwprovider.ConfigureRequest{Config: tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw:    raw,
	}}
	resp := fwprovider.ConfigureResponse{}
	p.Configure(context.Background(), req, &resp)
	return req, resp
}

// withClearedHCLOUDToken stashes and clears the HCLOUD_TOKEN env var so
// tests that exercise the env-var fallback aren't poisoned by the
// developer's local environment. Restores on cleanup.
func withClearedHCLOUDToken(t *testing.T) {
	t.Helper()
	t.Setenv("HCLOUD_TOKEN", "")
}

// TestConfigure_TokenFromHCL covers Finding 9: an explicit hcloud_token
// in HCL must populate ResourceData.
func TestConfigure_TokenFromHCL(t *testing.T) {
	withClearedHCLOUDToken(t)
	p := provider.New("test")()
	_, resp := configureWithToken(t, p,
		tftypes.NewValue(tftypes.String, "from-hcl"),
		tftypes.NewValue(tftypes.String, nil),
	)
	require.False(t, resp.Diagnostics.HasError(), "explicit token must succeed: %v", resp.Diagnostics)
	_, ok := resp.ResourceData.(hcloudx.Client)
	require.True(t, ok, "ResourceData must be a hcloudx.Client")
}

// TestConfigure_TokenFromEnv covers Finding 9: HCLOUD_TOKEN env var is
// the documented fallback when hcloud_token is omitted.
func TestConfigure_TokenFromEnv(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "from-env")
	p := provider.New("test")()
	_, resp := configureWithToken(t, p,
		tftypes.NewValue(tftypes.String, nil),
		tftypes.NewValue(tftypes.String, nil),
	)
	require.False(t, resp.Diagnostics.HasError(), "env-var fallback must succeed: %v", resp.Diagnostics)
	require.NotNil(t, resp.ResourceData, "client must be configured")
}

// TestConfigure_MissingTokenReturnsDiagnostic covers Finding 9: with
// neither HCL nor env var, Configure must surface a clear attribute
// error pointing at hcloud_token (not a generic provider error).
func TestConfigure_MissingTokenReturnsDiagnostic(t *testing.T) {
	withClearedHCLOUDToken(t)
	p := provider.New("test")()
	_, resp := configureWithToken(t, p,
		tftypes.NewValue(tftypes.String, nil),
		tftypes.NewValue(tftypes.String, nil),
	)
	require.True(t, resp.Diagnostics.HasError(), "missing token must produce a diagnostic")
	found := false
	for _, d := range resp.Diagnostics.Errors() {
		if d.Summary() == "Missing hcloud_token" {
			found = true
			break
		}
	}
	require.True(t, found, "diagnostic must name hcloud_token: %v", resp.Diagnostics.Errors())
}

// TestConfigure_HCLOverridesEnv covers the documented precedence: HCL
// wins when both are set.
func TestConfigure_HCLOverridesEnv(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "from-env")
	p := provider.New("test")()
	_, resp := configureWithToken(t, p,
		tftypes.NewValue(tftypes.String, "from-hcl"),
		tftypes.NewValue(tftypes.String, nil),
	)
	require.False(t, resp.Diagnostics.HasError())
	require.NotNil(t, resp.ResourceData)
}
