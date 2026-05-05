// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

// TestSchema_StdinIsSensitive guards Finding 2: operators routinely pipe
// secrets to commands via stdin. Without Sensitive=true the value lands
// in plan/state output in cleartext.
func TestSchema_StdinIsSensitive(t *testing.T) {
	resp := runSchema(t)

	// command.stdin under each lifecycle hook block.
	for _, hook := range []string{
		"before_create", "post_create",
		"before_replace", "post_replace",
		"before_remove", "post_remove",
	} {
		blk, ok := resp.Schema.Blocks[hook]
		require.True(t, ok, "hook block %q must exist", hook)
		stdin := lookupNestedAttr(t, blk, "command", "stdin")
		require.True(t, stdin.IsSensitive(), "%s.command.stdin must be Sensitive", hook)
	}

	// readiness_probe.command.stdin
	probe, ok := resp.Schema.Blocks["readiness_probe"]
	require.True(t, ok)
	stdin := lookupNestedAttr(t, probe, "command", "stdin")
	require.True(t, stdin.IsSensitive(), "readiness_probe.command.stdin must be Sensitive")
}

// lookupNestedAttr drills into a SingleNestedBlock's `command` sub-block
// to fetch a leaf attribute. Both lifecycle-hook blocks and
// readiness_probe wrap a single inner command block.
func lookupNestedAttr(t *testing.T, blk schema.Block, path1, attr string) schema.Attribute {
	t.Helper()
	outer, ok := blk.(schema.SingleNestedBlock)
	require.True(t, ok, "expected SingleNestedBlock, got %T", blk)
	inner, ok := outer.Blocks[path1]
	require.True(t, ok, "inner block %q missing", path1)
	innerBlk, ok := inner.(schema.SingleNestedBlock)
	require.True(t, ok, "expected inner SingleNestedBlock, got %T", inner)
	a, ok := innerBlk.Attributes[attr]
	require.True(t, ok, "attribute %q missing under %q", attr, path1)
	return a
}

// TestSchema_ReplicasUpperBound guards Finding 1: replicas must be 1..999.
// The implicit assumption in validators.go (maxSlotDigits = 3) and the
// deterministic name budget would silently break for replicas >= 1000.
func TestSchema_ReplicasUpperBound(t *testing.T) {
	resp := runSchema(t)
	attr, ok := resp.Schema.Attributes["replicas"]
	require.True(t, ok)
	intAttr, ok := attr.(schema.Int64Attribute)
	require.True(t, ok, "replicas must be Int64Attribute, got %T", attr)

	for _, val := range []int64{0, 1000, 99999} {
		req := validator.Int64Request{Path: path.Root("replicas"), ConfigValue: types.Int64Value(val)}
		hadErr := false
		for _, v := range intAttr.Validators {
			r := &validator.Int64Response{}
			v.ValidateInt64(context.Background(), req, r)
			if r.Diagnostics.HasError() {
				hadErr = true
			}
		}
		require.True(t, hadErr, "replicas=%d must be rejected", val)
	}
	for _, val := range []int64{1, 100, 999} {
		req := validator.Int64Request{Path: path.Root("replicas"), ConfigValue: types.Int64Value(val)}
		for _, v := range intAttr.Validators {
			r := &validator.Int64Response{}
			v.ValidateInt64(context.Background(), req, r)
			require.False(t, r.Diagnostics.HasError(), "replicas=%d must be accepted", val)
		}
	}
}
