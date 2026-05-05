// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup_test

import "regexp"

// regexpReservedLabel is the diagnostic message we expect when an HCL
// config uses a hcloudgroup.io/* label key.
func regexpReservedLabel() *regexp.Regexp {
	return regexp.MustCompile(`(?s)Reserved label namespace.*hcloudgroup\.io`)
}

// regexpReadinessFailed matches the surface diagnostic when a slot
// fails its readiness probe (`SlotError.Phase == "readiness_probe"`).
// The framework reports it as: 'readiness_probe failed on slot N'.
func regexpReadinessFailed() *regexp.Regexp {
	return regexp.MustCompile(`(?s)readiness_probe failed on slot`)
}
