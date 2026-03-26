# Release

## Tag Flow

1. Run local verification:

```bash
gofmt -w $(rg --files -g '*.go')
go test ./...
go vet ./...
go test -race ./...
```

2. Create and push a version tag:

```bash
git tag v0.2.0
git push origin v0.2.0
```

3. GitHub Actions runs the `release` job and invokes GoReleaser.

## What Is Automated

- GitHub release artifacts for `darwin`, `linux`, and `windows`
- Checksums
- Optional Homebrew formula publishing through GoReleaser

## Homebrew Prerequisites

Homebrew publishing is not self-contained in this repository. It additionally requires:

- a tap repository, currently configured as `tt-a1i/homebrew-tap`
- a `HOMEBREW_TAP_GITHUB_TOKEN` secret with permission to push to that repo

If the secret is missing, CI now skips the Homebrew publish step and still creates the GitHub release assets.

## CI Matrix

- `ubuntu-latest`: format, vet, test, build
- `windows-latest`: vet, test, build

## Notes

- `.goreleaser.yml` already targets `darwin`, `linux`, and `windows`.
- Homebrew publication should be treated as an external integration dependency, not a blocker for binary releases.
