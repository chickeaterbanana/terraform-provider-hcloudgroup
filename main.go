// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
// terraform-provider-hcloudgroup is the provider binary entry point. It
// hands off to the framework's providerserver, which speaks the
// terraform-plugin protocol on stdin/stdout. The Address must match the
// canonical registry path so terraform/tofu locate the provider via
// `required_providers`.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/provider"
)

// version is overwritten by goreleaser via -ldflags at release time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/chickeaterbanana/hcloudgroup",
		Debug:   debug,
	}
	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err)
	}
}
