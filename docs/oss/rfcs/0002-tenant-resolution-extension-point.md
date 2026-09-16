---
rfc: 0002
title: A tenant-resolution extension point in pkg/extpoint
author: lgreene03
status: draft
tier: design
opened: 2026-09-16
comment_closes: 2026-09-23
decided:
approvals: []
supersedes: 0
touches_entrenched: false
---

# RFC 0002: A tenant-resolution extension point in `pkg/extpoint`

## Summary

`pkg/extpoint` currently offers exactly one thing an `ee/` package can register: an
`http.Handler` mounted at a path prefix. That is enough for a feature that adds routes and not
enough for one that has to change how *existing* routes behave. Multi-tenancy is the second kind: it
needs to say which tenant a request belongs to on ingestion, on the metrics API, on the percentile
endpoint and on export — four core routes an `ee/` package cannot reach. This proposes a second,
deliberately narrow registration: one resolver that maps a request to a tenant identifier, with the
single-tenant identity as the default when nothing is registered.

## Motivation

`GRVX-1304` (`ee/tenancy/`) cannot be implemented. Its §6 step 1 says:

> Confirm `GRVX-1302` exposes a tenant-resolution extension point with a single-tenant default. If
> not, return `EXTENSION POINT REQUIRED`.

It does not. `GRVX-1302` §3 forbids exactly this:

> Do NOT add a second interface type to `pkg/extpoint` beyond `Extension`. Every Phase 13 capability
> that needs request-time behaviour mounts an `http.Handler` … A second interface here would be
> unused surface — the eight subsequent specs are designed against exactly this one.

That rationale is false, and `GRVX-1304` is the proof: it was designed against a different one, and
its §5.1 signature names an `extension.Request` type that no package defines. Two specs, both
marked ready, that cannot both be executed. This is recorded as **SD-040**.

The cost is not one spec. `GRVX-1304` blocks `GRVX-1305` (billing), `GRVX-1306` (identity),
`GRVX-1308` (compliance, via 1306), `GRVX-1310` (white-label) and `GRVX-1312` (packaging, via 1305)
— five of the eleven remaining Phase 13 specs, and `GRVX-1401` in Phase 14.

Nothing about this is urgent for an open-source user. It is entirely a question about how the paid
tier attaches, which is why it goes through the process rather than around it.

## Proposal

Add to `pkg/extpoint`, Apache-2.0, alongside the existing `Extension`:

```go
// SingleTenant is the tenant identity every request resolves to when no
// resolver is registered. It is the empty string because that is what the
// core's storage layout, rollup and queries already use for a single-tenant
// install: registering nothing changes nothing.
const SingleTenant = ""

// TenantResolver maps a request to a tenant identifier. At most one may be
// registered, from an ee/ package's init().
type TenantResolver interface {
    // Name is a short, stable identifier, used only for diagnostics.
    Name() string

    // ResolveTenant returns the tenant a request belongs to. Returning an
    // error refuses the request; it must never return ("", nil) to mean
    // "every tenant".
    ResolveTenant(r *http.Request) (string, error)
}

// RegisterTenantResolver installs the resolver. It panics on a second
// registration, because two resolvers disagreeing about who a request belongs
// to is a data-disclosure bug and there is no safe way to pick.
func RegisterTenantResolver(tr TenantResolver)

// ResolveTenant is what core calls. With no resolver registered it returns
// (SingleTenant, nil) for every request, which is what the core does today.
func ResolveTenant(r *http.Request) (string, error)

// HasTenantResolver reports whether one is registered, so a core call site can
// tell "single-tenant install" from "resolver refused".
func HasTenantResolver() bool
```

Core call sites change from using the single-tenant identity implicitly to calling
`extpoint.ResolveTenant(r)` and refusing the request on an error. The sites are
`pkg/gatewaycore` (the metrics, percentile, lineage and export handlers) and `services/ingestion`.
With nothing registered every one of them gets `("", nil)`, which is the value they use today, so
the OSS build's behaviour is unchanged by construction rather than by a flag.

The fail-closed rule `GRVX-1304` §5.2 asks for lives at the call site and is only reachable when a
resolver is registered: no resolver means single-tenant, which is not a missing context; a
registered resolver that errors means refuse, never "return everything".

A separate core spec would carry this, with the call-site changes, the
`TestSingleTenantUnchangedWithoutEE` byte-comparison, and a test that the OSS binary registers
nothing. `GRVX-1304` would then be executable as written, with no core edit of its own.

## Alternatives considered

1. **Mount tenancy as an `Extension` and leave core alone.** Rejected: an `Extension` only sees
requests under its own path prefix. Ingestion, the metrics API, the percentile endpoint and export
are core routes, so a handler at `/ee/tenancy/` cannot see the requests that need resolving. This is
not a limitation of the design; it is what the design is for.

2. **Let `ee/` wrap the whole `http.Handler` chain.** A `RegisterMiddleware(func(http.Handler)
http.Handler)` extension point would let `ee/` see every request. Rejected as too powerful: it lets
a paid package change any core response, which makes "the paid build is the free build plus
extensions" unverifiable. A resolver returns a string.

3. **Rewrite `GRVX-1304` to run multi-tenancy as a separate `ee/` process in front of the gateway.**
A reverse proxy that resolves the tenant and forwards with a header. This genuinely works and needs
no core change at all — but it moves a security boundary into a deployment topology, where getting
it wrong is silent and the failure mode is one customer seeing another's data. Worth reconsidering
if this RFC is rejected; it should not be adopted by default.

4. **Do nothing.** Five Phase 13 specs and one Phase 14 spec stay blocked, and the paid tier has no
multi-tenant control plane. The open-source product is entirely unaffected — single-tenant Gravix is
the free product's normal operation and does not need any of this. "Do nothing" is a real option
here, not a rhetorical one: it costs revenue, not correctness.

## Non-goals crossed

None. A reader might worry about `docs/04-non-goals.md` §5 (no high-cardinality dimensions), since
a tenant identifier is a new dimension on every request. It is not a crossing: tenant count is
bounded by customers, which is bounded and known, unlike `user_id` or `request_id`. `GRVX-1304`
§5.2 already forbids per-tenant *path* labels for exactly this reason.

## Charter impact

§7.1 (the core builds, tests and runs with `ee/` deleted) and §7.2 (`ee/` code may never be smuggled
into a place core depends on). §7.1 is **entrenched**, and this **strengthens** it rather than
weakening it: the resolver's absence is the default path and the tested one, and the proposed core
spec's acceptance criterion is a byte-identical comparison of single-tenant behaviour with and
without `ee/`. Core gains no import of `ee/`; the registration points the same direction the
existing `Extension` does.

Also §2.2, which already places multi-tenancy in `ee/`. This does not move the boundary — it builds
the door the boundary needs in order to hold.

## Migration

Nothing for any existing user. A single-tenant install registers no resolver, `ResolveTenant`
returns the identity every call site already uses, and no configuration, schema, storage path or
response body changes. That claim is exactly what the proposed core spec's AC-1 must prove by
comparing bytes, because a change that claims to need no migration and does is worse than one that
admits it.

## Unresolved questions

- **Does `ResolveTenant` belong on `*http.Request`, or on a narrower type?** An `*http.Request`
  hands `ee/` the whole request when it needs a host, a header or a path. A narrower struct
  (`{Host, Header, Path string}`) is harder to misuse and harder to extend. `GRVX-1304` §5.1 names
  an `extension.Request`, which suggests whoever wrote it intended the narrow one. Needs deciding
  before implementation, not after.
- **Does ingestion resolve tenants the same way the gateway does?** Ingestion authenticates with
  `X-API-Key` and the gateway with a JWT. One resolver seeing both may need to handle two shapes,
  or there may need to be two registrations. `GRVX-1304` does not say.
- **What happens to a request whose resolver errors during rollup or a background job**, where
  there is no `*http.Request` at all? The four query paths §5.3 names are all request-scoped; the
  rollup is not. This RFC covers the request paths only, and the background case needs its own
  answer before `GRVX-1307` (fleet) lands.
- **Who approves it.** Design tier needs two maintainer approvals. There is one maintainer
  (`MAINTAINERS.md`, bus factor 1). This RFC cannot be accepted under its own rules until that
  changes, which is goal **G9.3** and spec `GRVX-1210`. That is not a reason to lower the bar; it
  is the bar telling the truth about the project's size.
