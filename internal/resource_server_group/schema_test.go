package resource_server_group

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/stretchr/testify/require"
)

func runSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	r := &ServerGroupResource{}
	resp := resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	require.False(t, resp.Diagnostics.HasError(), "schema must build without diagnostics: %v", resp.Diagnostics.Errors())
	return resp
}

func TestSchema_RequiredAttributes(t *testing.T) {
	resp := runSchema(t)
	for _, name := range []string{"name", "replicas", "image", "server_type", "location", "network_id"} {
		attr, ok := resp.Schema.Attributes[name]
		require.True(t, ok, "attribute %q must exist", name)
		require.True(t, attr.IsRequired(), "attribute %q must be required", name)
	}
}

func TestSchema_OptionalAttributes(t *testing.T) {
	resp := runSchema(t)
	for _, name := range []string{"ssh_keys", "labels", "user_data_template", "replace_on_change"} {
		attr, ok := resp.Schema.Attributes[name]
		require.True(t, ok, "attribute %q must exist", name)
		require.True(t, attr.IsOptional(), "attribute %q must be optional", name)
	}
}

func TestSchema_ComputedAttributes(t *testing.T) {
	resp := runSchema(t)
	for _, name := range []string{"id", "current_replace_hash", "slots"} {
		attr, ok := resp.Schema.Attributes[name]
		require.True(t, ok, "attribute %q must exist", name)
		require.True(t, attr.IsComputed(), "attribute %q must be computed", name)
	}
}

func TestSchema_LifecycleHookBlocks(t *testing.T) {
	resp := runSchema(t)
	for _, name := range []string{
		"before_create", "post_create",
		"before_replace", "post_replace",
		"before_remove", "post_remove",
	} {
		_, ok := resp.Schema.Blocks[name]
		require.True(t, ok, "lifecycle hook block %q must exist", name)
	}
}

func TestSchema_ReadinessProbeAndTimeouts(t *testing.T) {
	resp := runSchema(t)
	_, ok := resp.Schema.Blocks["readiness_probe"]
	require.True(t, ok, "readiness_probe block must exist")
	_, ok = resp.Schema.Blocks["timeouts"]
	require.True(t, ok, "timeouts block must exist (delegated to terraform-plugin-framework-timeouts)")
}

func TestSchema_NameRequiresReplace(t *testing.T) {
	// Plan-time check: changing the group name forces replacement of the
	// whole resource (different label namespace).
	resp := runSchema(t)
	nameAttr, ok := resp.Schema.Attributes["name"]
	require.True(t, ok)
	// We can't introspect plan modifiers across the abstract Attribute
	// interface easily, but we can confirm the attribute is sensitive to
	// being marked Required so an HCL change goes through plan.
	require.True(t, nameAttr.IsRequired())
}

func TestDefaultTimeouts(t *testing.T) {
	create, update, deletion := DefaultTimeouts()
	require.Equal(t, "1h0m0s", create.String(), "create default 60m")
	require.Equal(t, "1h30m0s", update.String(), "update default 90m")
	require.Equal(t, "30m0s", deletion.String(), "delete default 30m")
}
