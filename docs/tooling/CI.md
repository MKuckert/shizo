# Continuous Integration

CI runs on GitHub Actions (`.github/workflows/ci.yml`): `make test` on every
push and pull request, the site deploy on `main`, and a release on `v*` tags.

## goreleaser

goreleaser is used to build and release the shizo binary. It is configured via the
`.goreleaser.yaml` file in the root of the repository.

### GitHub Actions

The release job runs `goreleaser release --clean` on `v*` tags using the
workflow's built-in `GITHUB_TOKEN`; no PAT is needed. The job requires
`permissions: contents: write` and a full checkout (`fetch-depth: 0`) so the
changelog can be generated from history.

### GitHub Pages

The `pages` job builds the site with `make site` and publishes `public/` via
`actions/deploy-pages`. In the repository settings, set Pages → Source to
"GitHub Actions".
