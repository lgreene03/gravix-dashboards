# Verifying a Gravix release

Every Gravix release is signed and reproducible. This page shows how to check both, so you do not
have to take our word for what you are running.

## Verify a binary

```bash
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/lgreene03/gravix-dashboards/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate <file>.pem \
  --signature <file>.sig \
  <file>
```

Gravix uses **keyless** signing. There is no long-lived private key to steal, escrow, or hand over
at succession — the identity being attested is the repository's release workflow itself, recorded in
a public transparency log.

## Verify a container image

```bash
cosign verify \
  --certificate-identity-regexp 'https://github.com/lgreene03/gravix-dashboards/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/lgreene03/gravix-<component>@sha256:<digest>
```

Verify by **digest**, not by tag. A tag can be moved to point at different content; a digest cannot.

## Verify the checksums

```bash
sha256sum -c checksums.txt
```

## Read the SBOM

Each release publishes `sbom.json` in [CycloneDX](https://cyclonedx.org/) format — a machine-readable
inventory of every dependency and its licence.

```bash
# What is in this build?
jq -r '.components[] | "\(.name) \(.version)"' sbom.json | sort

# What licences am I shipping?
jq -r '.components[] | .licenses[]?.license.id // "unknown"' sbom.json | sort | uniq -c

# Am I affected by a CVE in some package?
jq -r '.components[] | select(.name | test("PACKAGE")) | "\(.name) \(.version)"' sbom.json
```

That last query is the one that matters at 2am, and it is why the SBOM exists.

## Reproduce the build yourself

```bash
make verify-reproducible
```

This builds every binary twice and compares SHA-256 digests. All six are reproducible today.

If you build from a release tag and get a different digest from the published one, **that is worth
reporting**. It may be a toolchain difference — or it may not be. Report it through
[`SECURITY.md`](../SECURITY.md).

## What signing does and does not prove

> A signature proves this artefact was built by this repository's release workflow from a specific
> commit. It does not prove the code is free of vulnerabilities. Both matter; they are different
> questions.

A signed artefact from a compromised source tree is still a compromised artefact. Signing addresses
"did this come from where it claims", reproducibility addresses "does it match the source", and the
SBOM addresses "what is inside it". Use all three.

## Known gap

Two compiled binaries are currently committed to this repository (`cli` and
`service_events_detail`). **They are not signed, not reproducible, and not covered by anything on
this page.** Do not treat them as verified artefacts. See
[`oss/findings.md`](oss/findings.md) F-001.
