# Asking not to be credited

Release notes name everyone whose work is in a release. If you would rather not be named, add
yourself to the list below and you will appear as `an anonymous contributor` instead.

**No reason is required.** Do not explain, and nobody will ask. People have reasons for keeping
their name off a public page that are nobody else's business, and a process that makes you justify
it is a process that gets you named anyway.

## The list

```
```

One name or handle per line, inside the fence, exactly as it appears on your commits. A leading `@`
is fine and is ignored. The file is read by `pkg/relnotes`, so nothing else on this page affects
anything.

## How to add yourself

Open a pull request adding your line. If you would rather not do that publicly, email a maintainer
through GitHub and someone will open it for you — say only that you want to be added.

The pull request itself is public, which is the one part we cannot avoid: the file lives in the
repository so that the generator can read it offline, and so that anyone can check the generator is
honouring it. What the file does not contain is any reason.

## What it changes

| Where | Before | After |
|---|---|---|
| The Contributors list | your name | you are counted in "…and N who asked not to be credited" |
| A change you authored | your name beside it | `an anonymous contributor` |
| First-contribution welcome | your name | omitted |
| `git log`, `git shortlog` | your name | **unchanged** |

Git history is not rewritten. Your commits keep your authorship and your sign-off, because the DCO
is a legal record and rewriting it would break every signature and every existing checkout. What
changes is what gets published into release notes and the documentation site.

If that distinction matters to you — if what you want is your name out of the commit history and not
only out of the notes — say so before you open the pull request, and we will work out what is
actually possible before anything is merged.

## Removing yourself from the list

Delete your line. The next release credits you normally. There is no waiting period and no
conversation.

## What we will not do

- Ask why.
- Credit you anyway because the change was significant.
- Keep a private list of who is on this list and why.
- Use "anonymous contributor" as a euphemism for anything else. It means one thing: somebody asked,
  and we did as they asked.

`TestNoCreditRespected` fails the build if a name on this list would be rendered, and
`Validate` returns an error rather than notes. The promise is enforced by the same CI that enforces
everything else, rather than by somebody remembering at release time.
