package provider_test

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/stretchr/testify/require"

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
