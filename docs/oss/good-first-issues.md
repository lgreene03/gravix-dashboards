# `good first issue`

What the label means, what you can expect from us, and what happens if nobody finishes one.

Browse them: [**open `good first issue` items**](https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue).

## What the label promises

```
When we label an issue `good first issue`, we are promising:

  - the file you need to change is named in the issue;
  - what "done" looks like is described, not implied;
  - there is a command that tells you whether you got it right;
  - someone will answer a question on it within 48 hours;
  - the change is genuinely wanted, and a correct PR will be merged.

If an issue with this label fails any of those, that is our bug. Say so on the
issue and we will fix it.
```

That is five promises and none of them is "this is easy". Easy is not the point, and it is not
something we can promise about your evening anyway. What we can promise is that you will not lose
that evening to guessing what we wanted.

## Why the standard is this strict

Small and underspecified is the worst combination an issue can have. It looks approachable, so a
newcomer picks it up; then it turns out nobody knows which file, or what done looks like, or whether
the change was ever wanted. The contributor spends three hours, opens a pull request, and finds out
it was the wrong three hours.

So `good first issue` here does not mean "small". It means **specified**: the file, the change, and
the command. An issue that cannot state its verification command is not a good first issue — it may
be perfectly good work, and it gets a different label.

## The five required headings

Every labelled issue carries these, and `scripts/gfi_audit.sh` fails the inventory if one is missing:

| Heading | What it holds |
|---|---|
| `### The file` | The exact repo-relative path, and the line or function if that helps |
| `### The change` | What should be true afterwards, concretely |
| `### How to verify` | The exact command, and what its output should be |
| `### Why this matters` | One sentence, so the work is not busywork |
| `### If you get stuck` | Where to ask, and the 48-hour commitment |

## Claiming one

Comment on it. That is the whole process — no assignment, no form, and we will not assign an issue
to anyone who has not asked for it.

**A claim holds for 21 days.** After that you get one comment:

```
Hi @<user> — this issue has been claimed for 21 days. If you are still working on
it, just say so and it stays yours. If you have run into a problem, say that
instead and someone will help. If we do not hear back in 7 days we will unclaim
it so someone else can pick it up. Nothing is owed here; life happens.
```

If we do not hear back in another 7 days, the issue is unclaimed and someone else can take it.

The timer is about the queue, not about you. An issue that is claimed and then goes quiet blocks the
next person, and that is the only problem being solved here. Saying "I got stuck" is a completely fine
answer and usually turns into us fixing the issue.

Nothing is ever closed automatically. `scripts/gfi_audit.sh` reports; people decide.

## What we do not do

- **Label something `good first issue` because it is small.** See above.
- **Manufacture issues to hit a number.** The inventory targets ≥15 open and ≥5 unclaimed. If there are only nine genuine ones, the audit says nine. A fabricated task wastes somebody's evening, which is a worse outcome than a short list.
- **Assign issues to people who did not ask.**
- **Auto-close a stale contributor issue.** The workflow opens a tracking issue for maintainers; it never edits or closes yours.

## The inventory

[`docs/oss/good-first-issue-inventory.md`](good-first-issue-inventory.md) is the source list: the
candidate issues, each with its five headings, its real file, and its real verification command.
Every entry is checked by CI — the file must exist and the command's first token must be a real
executable — so an entry cannot rot into a trap without the build going red.

`scripts/gfi_audit.sh` audits it, and audits the live tracker when it can reach one:

```bash
./scripts/gfi_audit.sh                  # the live tracker, if reachable
./scripts/gfi_audit.sh --inventory      # the committed inventory file
./scripts/gfi_audit.sh --json           # machine-readable
```

Exit `0` healthy, `1` below target or an entry fails the standard, `2` the tracker is unreachable.

## If you are here to contribute

Read [`CONTRIBUTING.md`](../../CONTRIBUTING.md) for the build and test commands, pick an unclaimed
issue, and comment on it. If the issue turns out to be wrong — the file moved, the command does not
run, the change is not actually wanted — say so. That is a bug report about us, and fixing it is
worth more than the original issue was.
