# Contributing

## Setup

- Go 1.25.x
- `golangci-lint` v2.x (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)
- Optional: `tfplugindocs` for regenerating provider documentation

## Workflow

1. Fork and branch from `main`.
2. Make your changes and run the pre-flight checks below.
3. Open a pull request against `main` using a **merge commit** (the default — do not squash).

**Commit subjects** must follow [Conventional Commits](https://www.conventionalcommits.org/) — `feat:`, `fix:`, `chore:`, `docs:`, `ci:`, `test:`, `refactor:`, `deps:`. This is enforced by CI on every PR.

**Commit signing is required.** All commits reaching `main` must carry a valid GPG or SSH signature. Configure your local git before pushing:

```sh
git config commit.gpgsign true
git config user.signingkey <your-key-id>
```

## Pre-flight

```sh
make test   # unit + hermetic tests
make lint   # golangci-lint (includes SPDX-header check and security linters)
```

Acceptance tests hit the real Hetzner Cloud API and are opt-in:

```sh
HCLOUD_TOKEN=<sandbox-token> make testacc
```

## License Headers

Every new `.go` file must carry the SPDX header at the top:

```go
// Copyright (c) <year> The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
```

`golangci-lint` enforces this via the `goheader` linter. Run `golangci-lint run --fix` to auto-add missing headers.
