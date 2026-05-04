.PHONY: test testrace testacc lint sweep tidy docs

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
