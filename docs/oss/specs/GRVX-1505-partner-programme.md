# SPEC GRVX-1505: Partner and reseller programme

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1505 | **Phase** | 15 | **Goal** | G9.4 |
| **Placement** | `ee/` (BUSL-1.1) — partner entitlement plumbing; the programme itself is commercial policy |
| **Charter basis** | §7.3 Q1..Q5 all NO. A partner programme adds a commercial relationship; it withholds no capability, and anyone may build a business on Gravix with no programme at all. |
| **Implementer role** | `pro-engineer`, with `cpo` |
| **Depends on** | GRVX-707, GRVX-1305, GRVX-1310 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let consultancies and managed-service providers build businesses on Gravix with a formal
relationship — while keeping it unambiguous that **no programme membership is required** to do any
of it, because Apache-2.0 already permits everything the programme organises.

## 2. Context the implementer needs

- `TRADEMARK.md` (GRVX-707) already permits, with no permission needed: forking, saying "works with Gravix" or "built on Gravix", writing about it, running it commercially at any scale, and offering consulting, training or support **for** Gravix.
- What the trademark policy requires permission for: naming a product "Gravix", using the logo as your own, a hosted service **named** Gravix, and implying endorsement.
- The `ee/LICENSE` BUSL Additional Use Grant separately restricts offering the **Enterprise code** as a competing hosted service. That is a licence question and distinct from the naming question.
- `GRVX-1310` provides white-labelling, including the non-removable attribution string.
- `GRVX-1305` provides billing for reseller arrangements.

## 3. Non-goals for this spec

- Do NOT make programme membership a prerequisite for anything Apache-2.0 or `TRADEMARK.md` already permits. Doing so would convert a permissive licence into a gated channel.
- Do NOT create a partner tier that receives fixes, patches, or versions ahead of anyone else.
- Do NOT let "certified partner" imply Gravix warrants their work.
- Do NOT require a partner to be exclusive.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/partner/partner.go` | Partner records and entitlements |
| `ee/partner/partner_test.go` | Tests |
| `ee/partner/register.go` | Extension-point registration |
| `docs-site/docs/partners.md` | The programme, and what needs no programme |
| `docs/oss/partner-directory.md` | The public directory |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `TRADEMARK.md` | The programme operates within it; it does not amend it |
| `ee/LICENSE` | The Additional Use Grant is unchanged |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 What needs no programme, stated first

`docs-site/docs/partners.md` opens verbatim, **before** describing any tier:

```
You do not need to be a partner.

Apache-2.0 and our trademark policy already let you: run Gravix for your clients,
charge them for it, deploy and manage it, train people on it, write about it,
fork it, and say truthfully that your product is built on Gravix. None of that
needs our permission, a contract, or a logo from us.

What the programme adds is a commercial relationship: reseller pricing on
Gravix Pro, a listing in our directory, and a direct channel to us. It adds
nothing to the software, and it takes nothing away from anyone who never joins.

If you are already running Gravix for clients and it is working, you can ignore
this page entirely.
```

Putting this first is the substance. A partner page that opens with tiers implies permission is
required, and that implication would misrepresent the licence.

### 5.2 Tiers

| | Listed | Reseller | Managed Service |
|---|---|---|---|
| Directory listing | ✅ | ✅ | ✅ |
| "Built on Gravix" usage | already permitted | already permitted | already permitted |
| Reseller pricing on Pro | — | ✅ | ✅ |
| Bill clients directly | — | ✅ | ✅ |
| White-label (GRVX-1310) | — | — | ✅ |
| Direct channel to maintainers | — | ✅ | ✅ |
| Early access to fixes | **never** | **never** | **never** |
| Endorsement of their work | **never** | **never** | **never** |

The two `never` rows are permanent and appear in the table on purpose: a reader scanning tiers
should see the boundary without reading prose.

### 5.3 What "listed" does and does not mean

`docs/oss/partner-directory.md` states verbatim, adapting the plugin-registry precedent:

```
A listing is not an endorsement.

We check that a partner exists, that they have told us what services they offer,
and that they comply with the trademark policy. We do not audit their work,
verify their competence, or warrant their deployments. If a partner does a bad
job, that is between you and them.

We list them because you asked us who works with Gravix, not because we are
vouching for them.
```

### 5.4 Entitlements

```go
// Package partner implements Gravix partner programme entitlements. Membership
// grants commercial terms and a directory listing. It grants no software
// capability, no earlier access to any fix, and no endorsement.
package partner

// Tier is a partner level.
type Tier string

const (
    TierListed         Tier = "listed"
    TierReseller       Tier = "reseller"
    TierManagedService Tier = "managed_service"
)

// Entitlements returns what a tier grants. The returned set never contains a
// software capability: validation refuses any entitlement whose id appears in
// docs/oss/boundary.yaml, because that would mean partnership gates a feature.
func Entitlements(t Tier) []string

// ErrCapabilityEntitlement is returned when an entitlement names a boundary
// capability, which would make partnership a licensing mechanism.
var ErrCapabilityEntitlement = errors.New("partner: entitlements may not include a software capability")
```

The `boundary.yaml` cross-check is the structural guard. It makes it impossible to quietly add
"partners get feature X" without the validation refusing to load.

### 5.5 Trademark compliance

A listing requires: the partner's use of the Gravix name complies with `TRADEMARK.md`; they do not
name their product "Gravix" or a confusable variant; they do not use the logo as their own; and any
white-labelled deployment retains the attribution string (GRVX-1310 §5.4).

Non-compliance results in a request to correct, a 30-day window, and delisting if unresolved.
Delisting removes the directory entry and the commercial terms; it does not and cannot remove their
right to use the software, which Apache-2.0 grants irrevocably. The page says so, because a partner
should not fear that losing a listing loses them the product.

## 6. Behaviour

1. Write the partner page with the §5.1 statement **first**, before any tier description.
2. Implement tiers and entitlements with the `boundary.yaml` cross-check.
3. Write the directory with the §5.3 statement verbatim.
4. Implement the trademark compliance check and the delisting procedure, including the Apache-2.0 clarification.
5. Apply `degrade.Guard` to partner mutations.
6. Verify no entitlement grants a software capability or earlier access to anything.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Entitlement names a boundary capability | fails at load | `partner: entitlement "<id>" is a software capability; partnership does not gate features` |
| Trademark non-compliance | 30-day correction window, then delist | `partner: <name> has 30 days to correct trademark use in <detail>` |
| Delisting | listing and terms removed; software rights untouched | `partner: delisted; Apache-2.0 rights to the software are unaffected` |
| Early-access request | refused | `partner: fixes ship to everyone in the same release` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | The "you do not need to be a partner" statement appears first, before any tier | `TestNoProgrammeNeededStatedFirst` |
| AC-2 | No entitlement names a boundary capability | `TestEntitlementsGrantNoCapability` |
| AC-3 | No tier receives earlier access to any fix | `TestNoEarlyAccessForPartners` |
| AC-4 | The directory disclaimer appears verbatim | `TestDirectoryDisclaimerVerbatim` |
| AC-5 | Delisting does not affect software rights, and the page says so | `TestDelistingPreservesSoftwareRights` |
| AC-6 | Trademark compliance is checked before listing | `TestTrademarkComplianceChecked` |
| AC-7 | Exclusivity is not required at any tier | `TestNoExclusivityRequired` |
| AC-8 | The two `never` rows appear in the tier table | `TestNeverRowsInTierTable` |
| AC-9 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-10 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. The licence is not converted into a channel
go test ./ee/partner/... -run 'TestNoProgrammeNeededStatedFirst|TestEntitlementsGrantNoCapability|TestNoEarlyAccessForPartners' -v
# expect: PASS

# 2. Both verbatim statements
grep -c "You do not need to be a partner." docs-site/docs/partners.md
grep -c "A listing is not an endorsement." docs/oss/partner-directory.md
# expect: 1 each

# 3. Delisting is not a threat to the software
go test ./ee/partner/... -run TestDelistingPreservesSoftwareRights -v
grep -c "Apache-2.0 rights to the software are unaffected" docs-site/docs/partners.md
# expect: PASS; >= 1

# 4. The never rows are visible
go test ./ee/partner/... -run TestNeverRowsInTierTable -v
# expect: PASS

# 5. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The no-programme-needed statement demonstrably first on the page
- [ ] No entitlement resolves to a boundary capability
- [ ] Both verbatim statements present
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A proposed partner-only feature | Refuse, citing §5.4. Route to `license-boundary-auditor`. Partnership is a commercial relationship, not a licensing mechanism. |
| Pressure to give partners early access to fixes | Refuse, citing §5.2. Route to `cpo`. It would mean holding a fix from everyone else. |
| A partner claiming Gravix endorses their work | Request correction under `TRADEMARK.md`. Endorsement is the one thing a listing explicitly is not. |
