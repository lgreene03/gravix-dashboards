## What this changes

<!-- One or two sentences. The diff shows what; say why. -->

## Spec

<!-- The GRVX-nnn spec this implements, or "none — <reason>". -->

## How this was verified

<!-- Paste the real output of the commands you ran. Not a description of them. -->

```
```

## Checklist

- [ ] Commits are signed off (`git commit -s`)
- [ ] `go test ./...` passes
- [ ] `make check-boundary` prints `boundary: 0 violations`
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] Only files named by the spec are modified
- [ ] Docs updated, or no user-visible change
- [ ] No test skipped, quarantined, or deleted to make CI green
