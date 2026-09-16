---
title: Development setup
sidebar_position: 9
---

# Development setup

Two paths, both supported. The container is never the only one: if you have Go, you do not need
Docker to work on Gravix.

## Native

```bash
git clone https://github.com/lgreene03/gravix-dashboards
cd gravix-dashboards
./scripts/dev_setup.sh
```

That is the whole setup. The script checks your toolchain, fetches modules, verifies the generated
files are current, and runs the fast suite once — so it is not finished until a test has passed on
your machine.

**It installs nothing.** Where something is missing it prints the exact command for the platform it
detected and stops. A setup script that silently installs software on your machine is not a good
first impression, and it is not something you can undo afterwards.

| Tool | Needed for |
|---|---|
| Go (at the `go.mod` version) | everything |
| git | everything |
| `jq` | `scripts/gfi_audit.sh` |
| DuckDB CLI | the bare-Parquet and exit-path tests |
| `protoc` + `protoc-gen-go` | only if you change `proto/` |

Docker is needed for the full stack and for some end-to-end tests. It is **not** needed for the
suite you run before pushing.

## Dev container

Open the repository in VS Code and reopen in the container, or use any devcontainer-compatible tool.
`.devcontainer/` pins Go, the DuckDB CLI, `jq`, `protoc` with `protoc-gen-go`, `staticcheck` and
`govulncheck`, and runs `./scripts/dev_setup.sh` on create.

The container carries the toolchain and no Gravix services. When you want a running stack, use the
lean one:

```bash
docker-compose -f docker-compose.bootstrap.yml up -d
```

The full `docker-compose.yml` wants about 7.8 GB of RAM, which is not a reasonable thing to ask of
someone looking at this project for the first time.

## The two test suites

```bash
make test-fast    # before you push. Budget: 5 minutes. No Docker.
make test-full    # everything, including the slow-tagged suites
make test         # the same as test-full
```

| Suite | Contains | Budget |
|---|---|---|
| **fast** | unit tests for every package, `schemas/` at 100%, `make check-boundary`, the golden path | **5 minutes** |
| **full** | fast, plus `tests/e2e/`, `tests/correctness/`, `bench/`, `make build-oss`, `make test-oss` | 20 minutes |

**The split is by speed and dependency, never by importance.** No test was deleted, skipped, or
shortened to make the fast suite fast: the slow suites carry a `//go:build slow` tag, and CI runs
everything on every pull request. What the split buys is that the suite you run before pushing
finishes while you are still looking at it.

`--timing` prints the slowest packages, so a suite that has crept over budget tells you what did it:

```bash
./scripts/test_fast.sh --timing
```

### Exit codes

| Code | Meaning |
|---|---|
| 0 | passed within budget |
| 1 | a test failed |
| 2 | the toolchain is missing |
| **4** | **every test passed, and the suite was over budget** |

Exit 4 is deliberately its own code. Over-budget is a real regression and CI fails on it, but it is
not the same thing as a broken test — somebody who cannot tell them apart will fix the wrong one.

### Running a slow suite on its own

```bash
go test -tags=slow ./tests/correctness/...   # or: make test-correctness
go test -tags=slow ./tests/e2e/...
go test -tags=slow ./bench/...
```

## What CI runs

| Job | What it proves |
|---|---|
| `fast-suite-budget` | the contributor suite still fits in 5 minutes |
| `test` (Go 1.24 and 1.25) | every test, slow ones included, under the race detector |
| `correctness` | the Phase 8 properties, and the public `prove_it.sh` demonstration |
| `e2e` | the end-to-end path, including the exit path with Gravix stopped |
| `oss-integrity` | the core builds and tests with `ee/` deleted |

## Your first change

Pick something from the
[`good first issue` label](https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue)
and comment on it to claim it. Every one names the file, what done looks like, and the command that
verifies it — see [`docs/oss/good-first-issues.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/good-first-issues.md).
