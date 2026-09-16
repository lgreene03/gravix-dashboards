# Case studies

What a Gravix case study must contain, and why one section is **mandatory**.

## The rule that matters

**Every case study contains a "What did not work" section, with at least one real item.**

It is mandatory, and it is not politeness. A case study without friction is an advertisement, and
technical readers discount advertisements entirely — so a study that omits it does not achieve less
than one that includes it, it achieves nothing. Worse, it teaches the reader that this project
publishes marketing, which then colours how they read the correctness claims, which are the thing
Gravix actually stands on.

If a subject genuinely has nothing, ask again. Friction is always there: a confusing default, an
error message that did not say enough, an hour lost to a misunderstanding, a feature they expected
and had to work around. Any of those counts. "Nothing" means the question was not asked properly.

## Required sections

| # | Section | What it holds |
|---|---|---|
| 1 | `## Who` | The organisation and what they run |
| 2 | `## The problem before Gravix` | In their words, not ours |
| 3 | `## What they deployed` | The actual configuration, with numbers |
| 4 | `## What worked` | Specific outcomes, with figures where they have them |
| 5 | `## What did not work` | **Mandatory, minimum one item** |
| 6 | `## What they would tell someone evaluating Gravix` | Unedited |
| 7 | `## Approval` | The date the subject approved this text, and where they did |

## Approval is recorded, not assumed

Section 7 carries a date and a link to the issue or pull request where the subject said yes to
**this text**, not to the idea of a case study. Approving a conversation is not approving a
publication.

A case study with no recorded approval is not published. If the subject later wants it removed or
changed, that happens, without argument — the same 48-hour promise that covers
[`ADOPTERS.md`](../../ADOPTERS.md).

## What we do not do

- **Write one from telemetry.** There is none. Gravix collects no usage data (charter §7.4), so the only way we know anything about a deployment is that somebody told us.
- **Quote someone from a private conversation.** Section 6 is unedited and approved, or it is absent.
- **Edit section 5 down.** Trimming the friction is the one edit that makes the whole document worthless.
- **Publish figures the subject cannot share.** Approximate is fine; absent is fine.
- **Imply endorsement.** A case study says what one organisation did. It is not a recommendation from them.

## Writing one

Copy [`_TEMPLATE.md`](_TEMPLATE.md) to `<organisation>.md`, fill it in with the subject, and open a
pull request. The subject approves that pull request, or comments on the issue it references, and
that is what section 7 records.

Nothing is published before that approval lands.
