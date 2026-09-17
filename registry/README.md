# The Gravix plugin registry

`plugins.json` is the list of known Gravix plugins. It is a file in this repository, updated by pull
request. There is no registry service, no account, and no API to be down — `git clone` is the whole
distribution mechanism, and `gravix plugin list` reads the copy you already have.

## Listing here is not endorsement

```
Listing here is not endorsement.

Gravix does not review, audit, test, or vouch for third-party plugins. A plugin
is a subprocess with network access running on your infrastructure, with the
configuration you give it. Treat installing one exactly as you would treat
adding any other dependency: read the source, check the licence, and decide
whether you trust its maintainer.

We check that the repository exists, that it has a licence, that it declares a
supported ABI version, and that its README says what data it sends where. That
is all we check.
```

## How to list a plugin

Open a **Plugin listing request** issue (`.github/ISSUE_TEMPLATE/plugin_listing.yml`) with the six
facts below, or send a pull request adding your entry to `plugins.json` directly. Either way the
same six checks run, and nothing else is asked of you.

| # | Check | How it is verified |
|---|---|---|
| 1 | `repository` resolves and is public | Opened by a reviewer |
| 2 | The repository has a `LICENSE`, and `license` matches it | Read by a reviewer |
| 3 | `abi_version` equals the ABI this Gravix speaks | `pkg/registry` and the JSON Schema |
| 4 | `name` is unique and starts with `gravix-` | `pkg/registry` and the JSON Schema |
| 5 | `kind` is `notifier`, `exporter`, or `adapter` | `pkg/registry` and the JSON Schema |
| 6 | The README has a **Data flow** section saying what data the plugin sends where | Read by a reviewer |

Checks 3, 4 and 5 are enforced by CI on every pull request. Checks 1, 2 and 6 are about your
repository rather than about this file, so a reviewer performs them once, when your entry is added.
CI does not re-run them, because a registry that goes red whenever an unrelated third-party
repository has an outage is a registry nobody can merge into.

There is no quality bar. A plugin is not rejected for being small, unpopular, unfinished, or written
in a language we do not use. Subjective gates would make us responsible for what we list; mechanical
checks do not.

### Check 6, specifically

Your README needs a section headed `Data flow` — `## Data flow` is fine — saying what the plugin
sends and where it sends it. For example:

```markdown
## Data flow

Sends the alert id, rule name, metric name, threshold and fired-at timestamp to
the configured Mattermost server. Sends nothing anywhere else. Reads no facts.
```

This is the only check that is about the reader rather than the mechanics. Installing a notifier
that forwards alert contents to a third party is a data-flow decision, and nobody can make that
decision from a README that does not describe it.

## What is in the file

| Field | Meaning |
|---|---|
| `name` | Unique, starts with `gravix-` |
| `kind` | `notifier`, `exporter`, or `adapter` |
| `description` | One sentence, under 200 characters |
| `repository` | Public `https://` URL |
| `license` | SPDX identifier matching the repository's `LICENSE` |
| `abi_version` | The plugin ABI the plugin speaks; currently `1` |
| `maintainer` | Who to contact — not us, for a third-party plugin |
| `added` | `YYYY-MM-DD` |
| `first_party` | `true` only for plugins in this repository |

`first_party: true` means the code ships in this repository under the same licence and the same CI
as Gravix itself. Every other entry is third-party and carries no Gravix warranty of any kind.

## Nothing here is installed for you

`gravix plugin list` prints entries. It does not fetch, download, build, or run anything from a
listed repository, and no Gravix command does. Installing a plugin is a deliberate act: you clone
it, you read it, you build it, and you point Gravix at the binary. A tool that ran third-party code
because it appeared in a list would be a supply-chain hazard wearing a convenience feature's
clothing.

## Writing one

See [Writing a plugin](../docs-site/docs/writing-a-plugin.md), or run:

```bash
gravix plugin new --name gravix-notifier-example --kind notifier
```

The scaffold builds and passes its tests with no edits.
