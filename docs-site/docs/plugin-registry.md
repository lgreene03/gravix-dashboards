---
title: Finding and scaffolding plugins
sidebar_position: 7
---

# Finding and scaffolding plugins

Gravix has a plugin registry and a scaffold generator. Neither of them is a service, and neither of
them installs anything for you.

## Finding one

```bash
gravix plugin list
gravix plugin list --kind exporter
gravix plugin list --json
```

The registry is [`registry/plugins.json`](https://github.com/lgreene03/gravix-dashboards/blob/main/registry/plugins.json)
in this repository, embedded in the `gravix` binary. There is no registry service to be down, no
account to create, and `gravix plugin list` works offline because the answer was compiled in.

### Listing is not endorsement

Gravix does not review, audit, test, or vouch for third-party plugins. A plugin is a subprocess with
network access running on your infrastructure, with the configuration you give it. Treat installing
one exactly as you would treat adding any other dependency: read the source, check the licence, and
decide whether you trust its maintainer.

What is checked before an entry is listed is mechanical and short: the repository exists, it has a
licence matching what it claims, it declares a supported ABI version, its name is unique and
prefixed, its kind is one Gravix calls, and its README has a **Data flow** section saying what data
it sends where. Nothing else. There is no quality bar, because a subjective gate would make Gravix
responsible for what it lists.

### Nothing is installed for you

No Gravix command downloads, builds, or runs a plugin from the registry. `gravix plugin list` prints
entries; installing is a deliberate act — you clone it, read it, build it, and point Gravix at the
binary. A tool that ran third-party code because it appeared in a list would be a supply-chain
hazard wearing a convenience feature's clothing.

## Writing one

```bash
gravix plugin new --name gravix-notifier-mattermost --kind notifier
cd gravix-notifier-mattermost
make test
```

The scaffold builds and passes its tests with no edits. If it does not, that is our bug, not yours —
please [open an issue](https://github.com/lgreene03/gravix-dashboards/issues/new/choose).

| Flag | Default | Meaning |
|---|---|---|
| `--name` | *(required)* | Plugin name; must start with `gravix-` |
| `--kind` | `notifier` | `notifier`, `exporter`, or `adapter` |
| `--out` | `./<name>` | Output directory; refuses to write into a non-empty one |
| `--language` | `go` | `go` or `python` |

You get the plugin source with a working stub for the kind you chose, a test that exercises the ABI
handshake and one call, a `Makefile` with `build` and `test`, a `README.md` with the **Data flow**
section a listing needs, and a `LICENSE` placeholder to replace.

For what the interfaces mean and what the host guarantees, see [Writing a plugin](./writing-a-plugin.md).

## Checking one

```bash
gravix plugin validate --path ./bin/gravix-notifier-mattermost
```

`validate` starts the plugin, asks it for its manifest exactly the way Gravix does at load time, and
reports its name, version, kind and configuration fields. A plugin built against a different ABI
fails here with both version numbers, so you know which build to fetch:

```
plugin declares ABI 2, this Gravix supports 1
```

It runs the binary you named, so point it at a plugin you have already decided to trust.

## Getting listed

Open a **Plugin listing request** issue, or send a pull request adding your entry to
`registry/plugins.json`. Either way the same six checks apply, and
[`registry/README.md`](https://github.com/lgreene03/gravix-dashboards/blob/main/registry/README.md)
spells out each one.
