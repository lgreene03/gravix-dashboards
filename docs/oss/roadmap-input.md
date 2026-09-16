# How roadmap input is weighed

Open a [**Roadmap input**](https://github.com/lgreene03/gravix-dashboards/issues/new?template=roadmap_input.yml)
issue and tell us the **problem**. Not the feature you have in mind — the thing that is going wrong
for you. The feature is your first guess at a solution, and it is usually not the best one available
to us.

Every input appears on the [roadmap](../../docs-site/docs/roadmap.md) with a disposition, and nothing
sits unlabelled.

## What voting does

```
Reactions on a roadmap issue tell us what people want. They do not decide what
gets built.

We read them at the monthly goal review, alongside our own incident data, the
adoption funnel, and what the charter allows. A heavily-supported request that
crosses a non-goal is still declined, and we will say so in that issue rather
than leaving it open to rot.

What voting genuinely changes: the order of things we were already going to
build, and our sense of which problem is most painful. That is a real influence,
and it is the honest description of it.
```

That is the whole claim. It is deliberately smaller than "we build what you vote for", because that
promise is not one this project can keep — the charter forbids some of what people will ask for, and
pretending otherwise wastes their time and then disappoints them.

## The four dispositions

| Disposition | What it means | Who sets it |
|---|---|---|
| `under consideration` | Arrived, not yet reviewed | `oss-steward` |
| `accepted as SPEC <id>` | Prioritised, and a spec exists you can read | `cpo` |
| `declined — non-goal §<n>` | It falls in a permanently rejected category | `cpo` |
| `declined — <reason>` | Considered and rejected, with the reason stated | `cpo` |

**An item may sit `under consideration` for at most two goal reviews.** After that it is accepted or
declined with a reason, and `scripts/gen_roadmap_board.py` fails the build if one has not been.
Indefinite consideration is a decline that nobody has to defend, and it is the outcome that wastes
the most of a requester's hope.

## What will be declined, every time

The seven non-goals in [`docs/04-non-goals.md`](../04-non-goals.md). They are permanent, they are not
a backlog, and changing one needs the charter's §6 amendment procedure rather than a vote — see
[`GOVERNANCE.md`](../../GOVERNANCE.md).

When we decline on that basis we will cite the section and **name the tool that does do it**. Each
non-goal carries one. Being told no is annoying; being told no with nowhere to go is worse, and it is
avoidable.

## What helps most

- **The problem, with numbers.** "Our p99 checkout latency doubled and it took four hours to find out" is worth more than "please add anomaly detection".
- **What you tried.** Including the thing that nearly worked.
- **What you would accept.** Often there is a smaller answer that ships sooner.
- **Whether you would build it.** Nothing obliges you to, and it changes what we can promise.

## What we will not do with your input

- Close it silently because it is inconvenient.
- Leave it open indefinitely so that nobody has to say no.
- Ask you to justify caring about it.
- Treat a reaction count as a mandate, in either direction.
