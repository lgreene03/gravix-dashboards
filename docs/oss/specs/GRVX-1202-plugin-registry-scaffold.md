# SPEC GRVX-1202: Plugin registry and `gravix plugin new` scaffold

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1202 | **Phase** | 12 | **Goal** | G6.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. Discovery and scaffolding are how an ecosystem starts; gating either kills it. |
| **Implementer role** | `senior-engineer`, with `oss-steward` |
| **Depends on** | GRVX-1201 |
| **Blocks** | none |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make writing a Gravix plugin a fifteen-minute exercise: `gravix plugin new` scaffolds a working,
tested, buildable plugin, and a registry file lets users find plugins others have written — without
Gravix operating a service or vouching for third-party code.

## 2. Context the implementer needs

- `pkg/plugin` (GRVX-1201) defines the ABI, `Manifest`, the three interfaces and the subprocess host.
- `cmd/cli/` holds the `gravix` CLI. Read its subcommand registration before adding one.
- `docs-site/docs/writing-a-plugin.md` (GRVX-1201) is the guide the scaffold must match.
- G6.6 targets ≥5 third-party plugins by Phase 12 exit.
- Gravix has no server infrastructure for a registry service, and adding one would create an
  availability dependency the charter's offline-operation rule (§7.4) does not want.

## 3. Non-goals for this spec

- Do NOT operate a registry **service**. A JSON file in the repository, updated by pull request, has no uptime, no cost, and no single point of failure.
- Do NOT endorse, review, or certify third-party plugins. Listing is not vouching, and the registry must say so.
- Do NOT auto-install plugins from the registry. The user installs deliberately; a tool that downloads and runs third-party code on a listing entry is a supply-chain hazard.
- Do NOT gate registry inclusion on anything but the mechanical checks in §5.2.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `registry/plugins.json` | The registry |
| `registry/schema.json` | JSON Schema for entries |
| `registry/README.md` | How to list a plugin, and what listing does not mean |
| `pkg/registry/registry.go` | Parser and validator |
| `pkg/registry/registry_test.go` | Tests |
| `cmd/cli/cmd_plugin.go` | `gravix plugin new`, `list`, `validate` |
| `cmd/cli/cmd_plugin_test.go` | Tests |
| `cmd/cli/templates/plugin/` | Scaffold templates |
| `.github/ISSUE_TEMPLATE/plugin_listing.yml` | Listing request template |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/main.go` | Register `plugin`. Change no existing subcommand. |
| `.github/workflows/ci.yml` | Validate `registry/plugins.json` against its schema in the existing test job |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/plugin/**` | The ABI is settled by GRVX-1201 |
| `docs-site/docs/writing-a-plugin.md` | GRVX-1201 owns the guide; the scaffold matches it |

## 5. Interface contract

### 5.1 `registry/plugins.json`

```json
{
  "schema_version": 1,
  "plugins": [
    {
      "name": "gravix-notifier-mattermost",
      "kind": "notifier",
      "description": "Sends Gravix alerts to a Mattermost channel.",
      "repository": "https://github.com/example/gravix-notifier-mattermost",
      "license": "Apache-2.0",
      "abi_version": "1",
      "maintainer": "@example",
      "added": "2026-11-04",
      "first_party": false
    }
  ]
}
```

`first_party` is `true` only for plugins in this repository. Every other entry is third-party and
carries no Gravix warranty.

### 5.2 Listing checks — mechanical only

An entry is accepted when all of these hold, and rejected otherwise. No subjective quality bar:

1. `repository` resolves and is public.
2. The repository contains a `LICENSE` file, and `license` matches it.
3. `abi_version` equals the current `plugin.ABIVersion`.
4. `name` is unique in the registry and starts with `gravix-`.
5. `kind` is one of `notifier`, `exporter`, `adapter`.
6. The repository's README states what data the plugin sends where.

Check 6 is the only one that is about the user rather than the mechanics. A notifier that forwards
alert contents to a third party must say so, because installing it is a data-flow decision.

### 5.3 `registry/README.md` — the disclaimer, verbatim

```
Listing here is not endorsement.

Gravix does not review, audit, test, or vouch for third-party plugins. A plugin
is a subprocess with network access running on your infrastructure, with the
configuration you give it. Treat installing one exactly as you would treat
adding any other dependency: read the source, check the licence, and decide
whether you trust its maintainer.

We check that the repository exists, that it has a licence, that it declares a
supported ABI version, and that its README says what data it sends where. That
is all we check.
```

### 5.4 `gravix plugin` subcommands

| Subcommand | Flags | Purpose |
|---|---|---|
| `new` | `--name`, `--kind`, `--out`, `--language` | Scaffold a plugin |
| `list` | `--kind`, `--json` | List registry entries |
| `validate` | `--path` | Validate a plugin's manifest and ABI against this Gravix |

`new` flags:

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--name` | `string` | *(required)* | `plugin name; must start with gravix-` |
| `--kind` | `string` | `notifier` | `notifier, exporter, or adapter` |
| `--out` | `string` | `./<name>` | `output directory` |
| `--language` | `string` | `go` | `go or python` |

### 5.5 What the scaffold produces

A directory that **builds and passes its tests immediately**, containing: the plugin source
implementing the chosen interface with a working stub; a `manifest` response; a test that exercises
the ABI handshake and one call; a README with the data-flow statement check 6 requires; a `LICENSE`
placeholder; and a `Makefile` with `build` and `test`.

`cd <out> && make test` must pass on a freshly scaffolded plugin with no edits. A scaffold that does
not build is worse than no scaffold, because the author's first experience is debugging our template.

## 6. Behaviour

1. Implement `pkg/registry` with the §5.2 checks.
2. Write `registry/plugins.json` containing only first-party entries — the four existing notifiers and the three export formats — with `first_party: true`.
3. Write `registry/README.md` with the §5.3 disclaimer verbatim.
4. Implement `gravix plugin new` for Go and Python; both scaffolds build and pass tests unedited.
5. Implement `list` and `validate`.
6. Add the CI schema validation.
7. Write the listing issue template requiring the six §5.2 facts.
8. Verify `gravix plugin new` output passes `make test` with no edits, for every kind and both languages.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `--name` missing the prefix | exit 2 | `plugin name must start with "gravix-", got "<name>"` |
| Unknown `--kind` | exit 2 | `--kind must be notifier, exporter, or adapter` |
| Output directory not empty | exit 2 | `<dir> is not empty; refusing to overwrite` |
| Registry entry fails a check | CI fails | `registry: "<name>" failed check <n>: <detail>` |
| `validate` finds an ABI mismatch | exit 1 | `plugin declares ABI <a>, this Gravix supports <b>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A scaffolded Go plugin builds and passes tests with no edits | `TestGoScaffoldBuildsAndTests` |
| AC-2 | A scaffolded Python plugin runs its tests with no edits | `TestPythonScaffoldTests` |
| AC-3 | All three kinds scaffold correctly | `TestScaffoldAllKinds` |
| AC-4 | A scaffolded plugin loads under `pkg/plugin`'s host | `TestScaffoldLoadsInHost` |
| AC-5 | The registry validates against its schema | `TestRegistryValidates` |
| AC-6 | Each of the six §5.2 checks rejects a violating entry | `TestRegistryChecksEnforced` |
| AC-7 | The disclaimer appears verbatim in `registry/README.md` | `TestDisclaimerVerbatim` |
| AC-8 | No command downloads or installs a plugin from the registry | `TestNoAutoInstall` |
| AC-9 | `first_party` is true only for in-repo plugins | `TestFirstPartyFlagAccurate` |
| AC-10 | `validate` catches an ABI mismatch | `TestValidateCatchesABIMismatch` |
| AC-11 | A non-empty output directory is refused | `TestScaffoldRefusesNonEmptyDir` |

## 8. Verification

```bash
# 1. The scaffold works with zero edits — the whole point
go run ./cmd/cli plugin new --name gravix-notifier-demo --kind notifier --out /tmp/demo
cd /tmp/demo && make test && cd -
# expect: tests pass

# 2. It loads under the real host
go test ./cmd/cli/... -run TestScaffoldLoadsInHost -v
# expect: PASS

# 3. Registry checks
go test ./pkg/registry/... -v -cover
# expect: PASS, coverage >= 95%

# 4. The disclaimer is exact
grep -c "Listing here is not endorsement." registry/README.md
grep -c "That is all we check." registry/README.md
# expect: 1 each

# 5. No auto-install
go test ./cmd/cli/... -run TestNoAutoInstall -v
# expect: PASS

# 6. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] A freshly scaffolded plugin of every kind, in both languages, builds and tests unedited
- [ ] The disclaimer present verbatim
- [ ] No command installs third-party code
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to operate a registry service | Refuse, citing §3. A JSON file has no uptime to lose and no bill to pay. |
| Pressure to add a quality bar to listing | Route to `cpo`. Subjective gates make us responsible for what we list; mechanical checks do not. |
| A scaffold that does not build unedited | STOP. Fix the template. The author's first experience must not be debugging ours. |
