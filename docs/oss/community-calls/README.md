# Community calls

How calls are recorded, and the rule that makes them safe for people who cannot attend.

## No decision is made in a call

```
No decision is made in a call.

Calls surface problems, gather context, and reach rough agreement. The decision
itself happens afterwards, in writing, in an issue or an RFC, where someone who
was asleep in another timezone can read it, disagree with it, and change it.

If these notes ever record a decision with no written follow-up, that is a
process failure and you should say so.
```

This is the whole reason the calls are safe to hold. A distributed project whose real decisions
happen synchronously has quietly restricted its governance to whoever lives in the convenient
timezone and had a free hour — and it usually does not notice, because everyone who did attend
remembers agreeing.

## Notes, within 48 hours

Every call produces notes within **48 hours**, whether or not anyone asks for them. Late notes are
reported by `oss-steward` in the `COMMUNITY HEALTH` report as
`call notes for <date> published <n> hours late`, because the deadline is the part that makes the
promise real.

Notes contain:

| Part | Detail |
|---|---|
| Date and attendee **count** | Not names, unless someone asks to be named |
| Agenda | What was going to be discussed |
| Discussion | What was actually discussed, including what went nowhere |
| **Decisions** | Each one linked to the issue, RFC or spec that carries it forward |
| Open questions | What nobody could answer |
| Next call | The date |

**Attendee count, not names.** Someone attending a community call should not be creating a public
record of where they work or what they are evaluating. If they want to be named, they can say so.

## Every decision links its follow-up

A decision row with no link is the failure mode this page exists to prevent. The template's
decisions table has a `Written follow-up` column and it is not optional:

| Decision | Written follow-up |
|---|---|
| Move the retention default to 30 days | [#412](…) |

If a call reached rough agreement and nobody opened the issue, the notes say **that** — "agreed in
principle, no issue yet, `oss-steward` to open one" — rather than recording a decision that exists
only in the memories of the people who were there.

## Holding one

Copy [`_TEMPLATE.md`](_TEMPLATE.md) to `YYYY-MM-DD.md`, fill it in, and open a pull request within
48 hours of the call ending. Notes are a record, not a publication: a rough, prompt, honest set of
notes beats a polished set that arrives next week.
