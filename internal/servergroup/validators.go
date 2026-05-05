// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// maxGenerationDigits is the worst-case generation budget assumed by the
// server-name length validator. With 6 digits, a slot can hit generation
// 999_999 before the deterministic name overflows RFC 1123.
const maxGenerationDigits = 6

// maxSlotDigits assumes count <= 999. The schema's count >= 1 validator
// pairs with this implicit upper bound to keep the budget tractable.
const maxSlotDigits = 3

// rfc1123MaxLen is the hostname length cap. Server names must fit.
const rfc1123MaxLen = 63

// groupNameRE matches characters allowed in an RFC 1123 label - the
// strictest interpretation that also satisfies hcloud's label-selector
// value rules.
var groupNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func groupNameValidators() []validator.String {
	return []validator.String{groupNameValidator{}}
}

type groupNameValidator struct{}

func (groupNameValidator) Description(_ context.Context) string {
	return "must be a valid RFC 1123 label and short enough that group-slot-generation fits in 63 chars"
}
func (v groupNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (groupNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	v := req.ConfigValue.ValueString()
	if !groupNameRE.MatchString(v) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid group name",
			"name must match RFC 1123 label rules: lowercase a-z, 0-9, hyphen; must start and end with alphanumeric")
		return
	}
	// len(name) + len("-") + maxSlotDigits + len("-") + maxGenerationDigits <= 63
	maxNameLen := rfc1123MaxLen - 1 - maxSlotDigits - 1 - maxGenerationDigits
	if len(v) > maxNameLen {
		resp.Diagnostics.AddAttributeError(req.Path, "Group name too long",
			fmt.Sprintf("name must be <= %d chars so '<name>-<slot>-<generation>' fits in 63 chars", maxNameLen))
	}
}

func userLabelValidators() []validator.Map {
	return []validator.Map{userLabelValidator{}}
}

type userLabelValidator struct{}

func (userLabelValidator) Description(_ context.Context) string {
	return "label keys must not begin with " + hcloudx.Namespace
}
func (v userLabelValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (userLabelValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for k := range req.ConfigValue.Elements() {
		if strings.HasPrefix(k, hcloudx.Namespace) {
			resp.Diagnostics.AddAttributeError(req.Path, "Reserved label namespace",
				fmt.Sprintf("label key %q is in the provider-reserved namespace %s", k, hcloudx.Namespace))
		}
	}
}

func envKeyNamespaceValidators() []validator.Map {
	return []validator.Map{envKeyNamespaceValidator{}}
}

type envKeyNamespaceValidator struct{}

func (envKeyNamespaceValidator) Description(_ context.Context) string {
	return "env keys must not begin with " + actions.HCLOUDGROUPPrefix
}
func (v envKeyNamespaceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (envKeyNamespaceValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for k := range req.ConfigValue.Elements() {
		if strings.HasPrefix(k, actions.HCLOUDGROUPPrefix) {
			resp.Diagnostics.AddAttributeError(req.Path, "Reserved env namespace",
				fmt.Sprintf("env key %q is in the provider-reserved namespace %s", k, actions.HCLOUDGROUPPrefix))
		}
	}
}

func durationStringValidators() []validator.String {
	return []validator.String{durationStringValidator{}}
}

type durationStringValidator struct{}

func (durationStringValidator) Description(_ context.Context) string {
	return "must be a Go time.ParseDuration string, e.g. 30s, 5m"
}
func (v durationStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (durationStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration",
			fmt.Sprintf("could not parse %q as duration: %v", req.ConfigValue.ValueString(), err))
	}
}
