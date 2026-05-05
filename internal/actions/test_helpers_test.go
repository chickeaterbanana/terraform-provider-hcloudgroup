// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package actions

import "os"

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
