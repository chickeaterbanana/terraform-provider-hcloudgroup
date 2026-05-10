.PHONY: test testrace testacc lint sweep tidy docs smoke

# Hermetic unit + reconciler tests. Fast — gates every PR.
test:
	go test ./...

testrace:
	go test -race -count=1 ./...

# Acceptance tests against real Hetzner Cloud.
# Requires: HCLOUD_TOKEN, TF_ACC=1, ssh on PATH.
# `-p 1` serializes test-binary execution across packages because every
# binary's TestMain bootstraps + tears down the same shared fixtures.
# `-parallel=1` then serializes within one binary. Two layers of "no
# concurrency" — race-free fixture lifecycle by construction.
testacc:
	TF_ACC=1 go test -timeout 120m -p 1 -parallel=1 -count=1 ./...

lint:
	golangci-lint run

# Manual sweep — wipes any test fixtures left behind on the sandbox project.
sweep:
	TF_ACC=1 HCLOUD_SWEEP=1 go test -timeout 30m -count=1 ./internal/acctest -run '^TestSweep$$' -v

tidy:
	go mod tidy

# Regenerate the registry-format docs under docs/{index,resources,data-sources}.md
# Re-run whenever the schema changes. Requires:
#   go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest
docs:
	tfplugindocs generate --provider-name hcloudgroup

# Build the provider with release-equivalent flags and run a real-Hetzner
# end-to-end apply/destroy. Catches binary-level breakage the in-process
# acctest cannot. Requires HCLOUD_TOKEN. Set RUNTIME=terraform to use
# terraform instead of tofu.
#
# Two consecutive applies with different image values exercise the
# rolling-replace path under the v0.2.0 default replace_method =
# "create_before_destroy". The destroy_before_create variant is
# exercised by the acceptance suite (see internal/servergroup/
# server_group_advanced_acc_test.go: TestAccServerGroup_HookOrdering_DestroyFirst).
# Smoking only one mode keeps the release-leg matrix bounded; the binary
# is identical regardless of attribute value.
RUNTIME ?= tofu
smoke:
	@test -n "$$HCLOUD_TOKEN" || (echo "HCLOUD_TOKEN unset" && exit 2)
	goreleaser build --single-target --snapshot --clean
	@GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) sh ./.github/scripts/stage-provider.sh
	@SUFFIX="local-$$(date +%s)"; \
	  export TF_CLI_CONFIG_FILE="$$PWD/dist/dev_overrides.tfrc"; \
	  cd internal/smoketest && \
	  $(RUNTIME) init && \
	  $(RUNTIME) apply -auto-approve -var "suffix=$$SUFFIX" -var "image=debian-13" && \
	  $(RUNTIME) apply -auto-approve -var "suffix=$$SUFFIX" -var "image=debian-12"; \
	  rc=$$?; \
	  $(RUNTIME) destroy -auto-approve -var "suffix=$$SUFFIX" -var "image=debian-12" || true; \
	  exit $$rc
