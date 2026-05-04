// Package acctest holds the shared scaffolding for acceptance tests
// against real Hetzner Cloud: the provider factory map, the per-suite
// fixtures (Network, SSH key, jump host), and the global sweepers.
//
// Acceptance tests are gated by TF_ACC=1 (terraform-plugin-testing
// convention) AND a non-empty HCLOUD_TOKEN. Without both, every test in
// this package and the per-resource _acc_test.go files skips with an
// informative message rather than failing.
package acctest

import (
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/provider"
)

// ProviderName is the short name plugin-testing uses to register the
// in-process provider. The framework builds the full address as
// host/namespace/name; both terraform and opentofu resolve a bare
// "hcloudgroup" reference in HCL to "<host>/<namespace>/hcloudgroup".
//
// For opentofu compatibility, set TF_ACC_PROVIDER_HOST=registry.opentofu.org
// before running the suite — opentofu uses that as its default registry
// host, while terraform uses registry.terraform.io.
const ProviderName = "hcloudgroup"

// ProviderFactories returns the protov6 factory map every acceptance test
// passes to resource.Test. The provider is constructed in-process by the
// framework's providerserver, so terraform/tofu communicates over the
// plugin protocol just as it would with a published binary.
func ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		ProviderName: providerserver.NewProtocol6WithError(provider.New("acctest")()),
	}
}
