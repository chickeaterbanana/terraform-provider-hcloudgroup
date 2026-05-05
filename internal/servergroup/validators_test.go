// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

// runStringValidator constructs a validator.StringRequest with the given
// value and returns the diagnostics produced by every validator in vs.
func runStringValidator(vs []validator.String, val string) (out []string) {
	req := validator.StringRequest{
		Path:        path.Root("test"),
		ConfigValue: types.StringValue(val),
	}
	for _, v := range vs {
		resp := &validator.StringResponse{}
		v.ValidateString(context.Background(), req, resp)
		for _, d := range resp.Diagnostics.Errors() {
			out = append(out, d.Summary()+": "+d.Detail())
		}
	}
	return out
}

func runMapValidator(vs []validator.Map, m map[string]string) (out []string) {
	elements := map[string]attr.Value{}
	for k, v := range m {
		elements[k] = types.StringValue(v)
	}
	mv, _ := types.MapValue(types.StringType, elements)
	req := validator.MapRequest{
		Path:        path.Root("test"),
		ConfigValue: mv,
	}
	for _, v := range vs {
		resp := &validator.MapResponse{}
		v.ValidateMap(context.Background(), req, resp)
		for _, d := range resp.Diagnostics.Errors() {
			out = append(out, d.Summary()+": "+d.Detail())
		}
	}
	return out
}

func TestGroupNameValidator_AcceptsRFC1123(t *testing.T) {
	for _, name := range []string{"a", "consul", "consul-prod", "g0", "abc-123-def"} {
		require.Empty(t, runStringValidator(groupNameValidators(), name), "expected %q to be accepted", name)
	}
}

func TestGroupNameValidator_RejectsInvalidChars(t *testing.T) {
	for _, name := range []string{
		"-leading-dash",
		"trailing-dash-",
		"UPPERCASE",
		"under_score",
		"with.dot",
		"with space",
		"",
	} {
		require.NotEmpty(t, runStringValidator(groupNameValidators(), name), "expected %q to be rejected", name)
	}
}

func TestGroupNameValidator_LengthBoundary(t *testing.T) {
	// Budget: 63 - len("-") - 3 (slot digits) - len("-") - 6 (gen digits) = 52.
	atBoundary := strings.Repeat("a", 52)
	require.Empty(t, runStringValidator(groupNameValidators(), atBoundary),
		"name exactly at the 52-char boundary must be accepted")

	overBoundary := strings.Repeat("a", 53)
	errs := runStringValidator(groupNameValidators(), overBoundary)
	require.NotEmpty(t, errs, "53-char name must be rejected")
	require.Contains(t, strings.Join(errs, "\n"), "too long")
}

func TestUserLabelValidator_AcceptsUserNamespacedKeys(t *testing.T) {
	require.Empty(t, runMapValidator(userLabelValidators(), map[string]string{
		"env":  "prod",
		"role": "consul",
	}))
}

func TestUserLabelValidator_RejectsProviderNamespacedKeys(t *testing.T) {
	errs := runMapValidator(userLabelValidators(), map[string]string{
		"env":                       "prod",
		"hcloudgroup.io/managed-by": "spoof",
	})
	require.Len(t, errs, 1, "exactly one rejection")
	require.Contains(t, errs[0], "hcloudgroup.io/")
}

func TestEnvKeyNamespaceValidator_AcceptsRegularKeys(t *testing.T) {
	require.Empty(t, runMapValidator(envKeyNamespaceValidators(), map[string]string{
		"PATH":  "/bin",
		"HOME":  "/root",
		"TOKEN": "secret",
	}))
}

func TestEnvKeyNamespaceValidator_RejectsHCLOUDGROUPPrefix(t *testing.T) {
	errs := runMapValidator(envKeyNamespaceValidators(), map[string]string{
		"PATH":                "/bin",
		"HCLOUDGROUP_SLOT_ID": "999",
	})
	require.Len(t, errs, 1)
	require.Contains(t, errs[0], "HCLOUDGROUP_")
}

func TestDurationStringValidator_AcceptsParseDurationStrings(t *testing.T) {
	for _, s := range []string{"30s", "5m", "1h30m", "500ms", "1m30s", "1h"} {
		require.Empty(t, runStringValidator(durationStringValidators(), s), "expected %q to be accepted", s)
	}
}

func TestDurationStringValidator_RejectsBadInputs(t *testing.T) {
	for _, s := range []string{"", "5", "forever", "-1s but extra", "5 minutes"} {
		require.NotEmpty(t, runStringValidator(durationStringValidators(), s), "expected %q to be rejected", s)
	}
}
