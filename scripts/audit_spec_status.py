#!/usr/bin/env python3
"""Checks docs/oss/spec-status.json against the tree it describes.

A spec marked `done` whose files do not exist is worse than one marked `planned`:
it is a published claim that work happened. This file existed for months saying
nine Phase 13, 14 and 15 specs were done while ee/ held nothing but a placeholder,
and the roadmap board renders that status straight onto the docs site. Nobody
lied; a status file maintained by hand drifts, and nothing was checking it.

So this checks it. For every spec marked `done`, every path in its "§4.1 Files to
create" table must exist. A spec that was legitimately executed differently from
its file list gets an entry in docs/oss/spec-status-exceptions.json naming the
paths and the defect record that explains them — never a blanket waiver.

Usage:
  scripts/audit_spec_status.py                     # audit; exit 1 on any finding
  scripts/audit_spec_status.py --quiet             # exit code only
  scripts/audit_spec_status.py --status FILE       # audit a copy, for testing
"""

import argparse
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STATUS = os.path.join(ROOT, "docs", "oss", "spec-status.json")
EXCEPTIONS = os.path.join(ROOT, "docs", "oss", "spec-status-exceptions.json")
SPEC_DIR = os.path.join(ROOT, "docs", "oss", "specs")

SPEC_ID = re.compile(r"^(GRVX-\d+)")
SECTION = re.compile(r"### 4\.1 Files to create(.*?)(?:\n#{2,3} |\Z)", re.S)


def spec_files() -> dict[str, str]:
    """Maps GRVX-nnnn to its spec file path."""
    out = {}
    for name in sorted(os.listdir(SPEC_DIR)):
        m = SPEC_ID.match(name)
        if m and name.endswith(".md"):
            out[m.group(1)] = os.path.join(SPEC_DIR, name)
    return out


def files_to_create(path: str) -> list[str]:
    """Returns the repository-relative paths in a spec's §4.1 table."""
    with open(path, encoding="utf-8") as f:
        section = SECTION.search(f.read())
    if not section:
        return []

    paths = []
    for line in section.group(1).splitlines():
        line = line.strip()
        if not line.startswith("|") or line.startswith("|---") or line.startswith("| Path"):
            continue
        cell = line.split("|")[1].strip().strip("`").strip()
        # A table cell that is prose, a glob, or a directory note is not a path.
        if not cell or "/" not in cell or cell.startswith("(") or "*" in cell or " " in cell:
            continue
        paths.append(cell)
    return paths


def load_exceptions(path: str) -> dict[str, dict]:
    if not os.path.exists(path):
        return {}
    with open(path, encoding="utf-8") as f:
        return {k: v for k, v in json.load(f).items() if not k.startswith("_")}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--quiet", action="store_true")
    # The status and exceptions files are overridable so that the test proving
    # this audit can fail points it at a copy. A test that edited the committed
    # register and put it back would leave the repository dirty the first time
    # it crashed.
    ap.add_argument("--status", default=STATUS)
    ap.add_argument("--exceptions", default=EXCEPTIONS)
    args = ap.parse_args()

    def say(*a):
        if not args.quiet:
            print(*a)

    with open(args.status, encoding="utf-8") as f:
        status = json.load(f)
    specs = spec_files()
    exceptions = load_exceptions(args.exceptions)

    # `make test-oss` deletes ee/ and runs this suite. A spec whose files all live
    # under ee/ is not missing there — ee/ is. pkg/boundary's own path check makes
    # the same exemption for the same reason: a check that failed in the OSS build
    # would make charter §7.1's central invariant unverifiable.
    ee_present = os.path.isdir(os.path.join(ROOT, "ee"))
    if not ee_present:
        say("ee/ is absent: this is an OSS build, so ee/ paths are not checked")

    findings = 0

    # Every status names a real spec, and every spec has a status. A spec missing
    # from the file defaults to "planned" in the board, which hides it.
    for sid in sorted(set(status) - set(specs)):
        say(f"{sid}: has a status but no spec file in docs/oss/specs/")
        findings += 1
    for sid in sorted(set(specs) - set(status)):
        say(f"{sid}: has a spec file but no status; the board will call it planned")
        findings += 1

    for sid in sorted(status):
        if status[sid] != "done" or sid not in specs:
            continue
        declared = files_to_create(specs[sid])
        if not declared:
            continue

        waived = exceptions.get(sid, {})
        waived_paths = set(waived.get("paths", []))
        if waived_paths and not waived.get("reason"):
            say(f"{sid}: has an exception with no reason; a waiver without a reason is a hole")
            findings += 1

        missing = [p for p in declared
                   if not os.path.exists(os.path.join(ROOT, p))
                   and p not in waived_paths
                   and not (not ee_present and p.startswith("ee/"))]
        if missing:
            say(f"{sid}: marked done, but {len(missing)} of its §4.1 files do not exist:")
            for p in missing:
                say(f"    {p}")
            findings += 1

        # A waiver for a path that now exists is stale and should be removed, so
        # the exceptions file does not quietly grow into a permanent blind spot.
        for p in sorted(waived_paths):
            if not ee_present and p.startswith("ee/"):
                continue
            if os.path.exists(os.path.join(ROOT, p)):
                say(f"{sid}: {p} exists; remove its entry from spec-status-exceptions.json")
                findings += 1
            elif p not in declared:
                say(f"{sid}: {p} is waived but is not in the spec's §4.1 table")
                findings += 1

    done = sum(1 for v in status.values() if v == "done")
    if findings == 0:
        say(f"spec status: {done} specs marked done, every §4.1 file present")
        return 0
    say(f"\nspec status: {findings} finding(s) across {len(status)} specs")
    return 1


if __name__ == "__main__":
    sys.exit(main())
