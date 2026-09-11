# Add GoReleaser and GitLab CI

> TODO: "Add go releaser and github actions" (adjusted to GitLab CI since the
> repo lives on gitlab.ssc-os.local)

## Problem

Builds and releases are manual (`make test`, `make release`). No CI runs tests
on push or MR; no automation creates cross-platform release archives from tags.

## Shape

Three new files, no changes to existing code.

### 1. `.goreleaser.yaml` (repo root)

GoReleaser v2 config:

- `builds[].dir: src` — module root is `src/`, not repo root
- ldflags: `-s -w -X main.version={{.Version}}` (matches Makefile)
- flags: `-trimpath` (matches `make release`)
- Platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- Archives: tar.gz (zip for windows), include `src/gate.sh` and `conf/*`
- Checksums: SHA256
- Changelog: auto from commits, filter `docs:`, `test:`, `ci:` prefixes
- Release: `gitlab` provider, `gitlab_urls` pointing to `https://gitlab.ssc-os.local`

### 2. `.gitlab-ci.yml` (repo root)

Two stages: `test`, `release`.

**test** — every push and MR, `golang:1.27` image, `make test`.

**release** — `v*` tag pushes only, installs GoReleaser,
`goreleaser release --clean`. Requires `GITLAB_TOKEN` CI/CD variable
(project-level, `api` scope).

### 3. No existing-file changes

- `.gitignore` already covers `/dist`
- `Makefile` stays (still useful for local dev)
- No linter added (separate concern)

## Tag convention

Future releases use v-prefixed semver (`v0.2.0`, `v1.0.0`). Existing `0.1` tag
stays as-is.

## Prerequisites

- `GITLAB_TOKEN` CI/CD variable in project settings (api scope)
- GitLab runner available (Docker executor for the golang image)

## Verification

1. `goreleaser check` — validate config syntax
2. `goreleaser build --snapshot --clean` — local dry-run
3. Push branch, open MR — test job runs and passes
4. `git tag v0.2.0 && git push origin v0.2.0` — release job creates GitLab
   release with 5 platform archives + checksums
