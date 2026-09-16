---
title: Publishing the Grafana plugin
sidebar_position: 6
---

# Submitting the plugin to the Grafana catalog

A human procedure, written down because it cannot be automated from this repository and should not
be reconstructed from memory by whoever gets to it.

**Owner:** `oss-steward`. **Prerequisite:** every GRVX-1104 acceptance criterion passing, including
the two that need a running stack.

## Why this is not in CI

Catalog submission needs a Grafana Cloud organisation account and a plugin signing API key. Neither
exists for this project, and neither should be created just to make a pipeline green — a signing key
in CI is a credential worth stealing, and [`succession.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/succession.md)
already records that this project has nobody to hand its credentials to.

When the key does exist, it becomes an asset in that register **before** it is first used, with two
custodians. Not after.

## The procedure

1. **Create a Grafana Cloud account** for the project, not for a person. It is an organisation
   identity and it outlives whoever registers it.
2. **Generate a plugin signing key** in Grafana Cloud, under Access Policies, scoped to
   `plugins:write`.
3. **Add the key to the succession register** ([`docs/oss/succession.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/docs/oss/succession.md))
   with two custodians and a recovery path, and confirm `./scripts/verify_custody.sh` still passes.
   Do this before step 4.
4. **Sign the build:**
   ```bash
   cd grafana-plugin/gravix-datasource
   go build -o dist/gpx_gravix_datasource ./cmd
   npm install && npm run build
   npx @grafana/sign-plugin@latest --rootUrls https://your-grafana.example.com
   ```
   Signing writes `dist/MANIFEST.txt`. Never commit it — it is specific to one build and one set of
   root URLs.
5. **Cross-build the backend** for every platform the catalog lists. The plugin is CGO-free
   (`TestBuildIsCGOFree` proves it), so this is `GOOS`/`GOARCH` and nothing else:
   ```bash
   for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
     GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
       go build -o dist/gpx_gravix_datasource_${target%/*}_${target#*/} ./cmd
   done
   ```
6. **Submit** at grafana.com/auth/sign-in → My Plugins → Submit Plugin, pointing at a public
   release archive of `dist/`.
7. **Record the outcome** — accepted, or the review's objections — in `docs/oss/findings.md`. A
   rejection is more useful written down than remembered.

## What reviewers ask about

From Grafana's published review criteria, worth pre-empting:

- **A README that shows a screenshot and a working configuration.** [`grafana-plugin.md`](grafana-plugin.md) is that page.
- **No telemetry.** The plugin sends nothing anywhere except queries to the Trino you configure.
  Charter §7.4, and it is worth saying explicitly in the submission.
- **A licence.** Apache-2.0, same as the core.
- **Semantic versioning**, tracked in `plugin.json`'s `info.version`.

## If the answer is no

The plugin still works unsigned, and [`grafana-plugin.md`](grafana-plugin.md) documents that path.
Catalog listing is distribution convenience, not a dependency — which is the point of publishing the
build instructions rather than only a signed artefact.
