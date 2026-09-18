# Continuous Integration

## goreleaser

goreleaser is used to build and release the shizo binary. It is configured via the
`.goreleaser.yml` file in the root of the repository.

### GitLab CI

Required Scopes for a Personal Access Token (PAT) to use goreleaser with the GitLab CI:
- Release: Create
- Markdown Upload: Create
- Release Link: Create
