# Licence map

Gravix is open source. This file records which licence applies to which path.

| Path | Licence |
|---|---|
| `/` (all paths not listed below) | Apache-2.0 |
| `ee/**` | BUSL-1.1, Change Date = release + 2 years, Change Licence = Apache-2.0 |
| `sdk/**` | Apache-2.0 |
| `proto/**` | Apache-2.0 |
| `deploy/**` | Apache-2.0 |
| `terraform-provider-gravix/**` | Apache-2.0 |
| `docs/**`, `docs-site/**` | CC-BY-4.0 |
| `gen/**` | Apache-2.0 (generated from `proto/`) |
| `**/node_modules/**` | Third-party; see each package |

`ee/` is **source-available**, not open source. It holds Gravix Enterprise Edition under BUSL-1.1,
converting to Apache-2.0 two years after each version is published. Everything else in this
repository is open source under an OSI-approved licence and, per the Open-Core Charter §7.3 Q4,
cannot be relicensed.

The core builds and passes its tests with `ee/` deleted — `make build-oss` and `make test-oss`
prove it on every pull request. See [`ee/README.md`](ee/README.md).

See [`LICENSE`](LICENSE), [`NOTICE`](NOTICE), and
[`docs/oss/00-open-core-charter.md`](docs/oss/00-open-core-charter.md).
