# gha-timeout-guard

[![ci](https://github.com/moveeeax/gha-timeout-guard/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/gha-timeout-guard/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Lint GitHub Actions workflows for the reliability settings nobody sets until it hurts: jobs with no
`timeout-minutes` that hang for the full six-hour runner limit, missing `concurrency` groups that let
pushes stack identical runs, blanket `continue-on-error` that hides failures, and network steps with
no timeout. Exits non-zero in CI.

A job without `timeout-minutes` does not fail. It hangs for six hours, holds the concurrency slot, and
the PR author goes home. `actionlint` checks syntax and expressions; nothing checks for the three
lines of YAML that are missing.

## Rules

| Rule | Severity | What it catches |
|---|---|---|
| `job-timeout` | error | A job with no `timeout-minutes`. Jobs that call a reusable workflow are exempt, because GitHub rejects `timeout-minutes` there. |
| `max-timeout` | warning | An explicit `timeout-minutes` above `--max-timeout` (default 60). |
| `concurrency` | warning | A `push`/`pull_request` workflow with no `concurrency` group, or a group without `cancel-in-progress: true`. Schedule- and dispatch-only workflows are exempt. |
| `job-continue-on-error` | error | `continue-on-error: true` on a job — the whole job reports success when it fails. |
| `step-continue-on-error` | warning | `continue-on-error: true` on a single step. |
| `step-timeout` | warning | A step running `curl`, `wget`, `docker pull` or `apt-get` with no step `timeout-minutes`. |

Every finding carries the file, line, job id, step index where relevant, and the exact YAML line to add.

## Install

```sh
go install github.com/moveeeax/gha-timeout-guard/cmd/gha-timeout-guard@latest
```

## Usage

```sh
gha-timeout-guard --dir .github/workflows
gha-timeout-guard --dir . --format markdown --fail-on error
gha-timeout-guard --max-timeout 30 --ignore step-timeout
```

`--dir` accepts either a repository root (it finds `.github/workflows` itself) or a workflows
directory directly.

Against the workflows in `examples/`:

```console
$ gha-timeout-guard --dir examples/workflows
SEVERITY  RULE                   LOCATION                                   FINDING
warning   concurrency            examples/workflows/deploy.yml:1            workflow triggers on push or pull_request with no concurrency group, so every push runs a full duplicate pipeline
error     job-timeout            examples/workflows/deploy.yml:7 build      job "build" has no timeout-minutes and can hang for the 6h runner default
warning   step-timeout           examples/workflows/deploy.yml:11 build[1]  step "fetch toolchain" in job "build" runs curl with no step timeout-minutes
warning   max-timeout            examples/workflows/deploy.yml:17 deploy    job "deploy" sets timeout-minutes: 720, above the 60 minute ceiling
error     job-continue-on-error  examples/workflows/deploy.yml:18 deploy    job "deploy" sets continue-on-error: true, so the whole job reports success when it fails

5 finding(s) in 2 workflow(s). Add:

# examples/workflows/deploy.yml:1 (concurrency)
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
# examples/workflows/deploy.yml:7 (job-timeout)
    timeout-minutes: 15
# examples/workflows/deploy.yml:11 (step-timeout)
        timeout-minutes: 5
# examples/workflows/deploy.yml:17 (max-timeout)
    timeout-minutes: 60
# examples/workflows/deploy.yml:18 (job-continue-on-error)
    continue-on-error: false

$ echo $?
1
```

`nightly.yml` in the same directory is a cron workflow with a timeout and no concurrency group, and
is correctly left alone.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `.` | Repository root or workflows directory. |
| `--max-timeout` | `60` | Ceiling for an explicit `timeout-minutes`. `0` disables the rule. |
| `--suggest-timeout` | `15` | Value written into suggested `timeout-minutes` lines. |
| `--min-severity` | `warning` | Lowest severity to report: `warning` or `error`. |
| `--ignore` | — | Rule id to suppress. Repeatable or comma-separated. |
| `--format` | `table` | `table`, `json` or `markdown`. |
| `--fail-on` | `warning` | Exit 1 on findings at or above this level, or `never`. |

Exit codes: `0` clean, `1` findings at or above `--fail-on`, `2` a parse or usage error.

## As a GitHub Action

```yaml
name: workflow-hygiene

on:
  pull_request:
  push:
    branches: [main]

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  guard:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v7
      - uses: moveeeax/gha-timeout-guard@v0
        with:
          dir: .
          max-timeout: '30'
          fail-on: error
```

The report lands in the job summary. The action exposes `exit-code` as an output, so a soft rollout
can set `fail-on: never` and gate on the value later.

## How it works

Workflows are parsed with `go.yaml.in/yaml/v3` as a raw node tree rather than unmarshalled into
structs. Nodes carry line and column, which is what makes two things possible: findings point at the
real line in the file, and the suggested YAML is indented to match the block it belongs in, so it can
be pasted in without reformatting.

Reading the trigger list needs the node tree for a second reason. YAML 1.1 resolves a bare `on` key to
the boolean `true`, so a struct-based parser has to be told about it; walking the mapping and matching
the key's literal text sidesteps the problem entirely.

The `step-timeout` rule tokenises each `run` line rather than substring-matching, so `./curl-helper.sh`
and `echo wgetlog` do not produce findings, and comment lines are skipped.

## Development

```sh
go test ./...
go run ./cmd/gha-timeout-guard --dir .
```

Fixtures live in `testdata/`: a clean workflow, a job with no timeout, a stacking push workflow, a
`continue-on-error` job, a cron workflow that must not be flagged, a reusable-workflow call, and a
deliberately broken file for the exit-2 path. One test lints this repository's own workflows and fails
if they ever stop being clean.

## License

MIT
