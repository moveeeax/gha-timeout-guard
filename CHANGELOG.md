# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- The `concurrency` rule is deploy-aware. A workflow where a job declares
  `environment:` or runs a deploy command (`terraform apply`, `helm upgrade`,
  `kubectl apply`, `docker push`, `npm publish` and the rest of the
  gha-concurrency-audit pattern) now gets a suggested block with
  `cancel-in-progress: false`, and `cancel-in-progress: true` (or an
  expression) on such a workflow or deploy job is reported instead of
  recommended. A job-level group on the deploy job satisfies the rule.

- YAML parsing moved from the archived `gopkg.in/yaml.v3` to its maintained
  successor `go.yaml.in/yaml/v3` (v3.0.6).
- Minimum Go version raised to 1.26; CI tests Go 1.26 and 1.27.
- `actions/checkout` and `actions/setup-go` bumped to v7, including the
  `setup-go` step inside the composite action.
- The action's `version` input now defaults to empty, which installs the ref
  the action is used at (for example `@v0` or `@v0.2.0`), so the default no
  longer has to be bumped by hand on each release. Falls back to `latest`.

### Added

- Dependabot configuration for GitHub Actions and Go modules.

## [0.1.0] - 2026-10-17

### Added

- Initial release: `job-timeout`, `max-timeout`, `concurrency`,
  `job-continue-on-error`, `step-continue-on-error` and `step-timeout` rules,
  table/JSON/markdown output, and a composite GitHub Action.

[Unreleased]: https://github.com/moveeeax/gha-timeout-guard/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/moveeeax/gha-timeout-guard/releases/tag/v0.1.0
