#!/usr/bin/env python3
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
"""Mechanical Spec Readiness checks (F-042).

The Readiness Gate's check 1 asks that every file be named by an exact
repo-relative path. It tests the form of a path, not whether it is true, and
Phase 11 dispatched five specs that each passed 12/12 while naming a file that
did not hold the code (SD-029) or omitting files their own steps required
(SD-029, GRVX-1108). Two checks close that, and neither needs judgement:

  M1  Every path a spec names in §2 (context) or §4.2 (files to modify) exists.
      §4.1 paths are being created, so they are exempt.
  M2  Every path named in §6 (behaviour) or §7 (acceptance criteria) is in
      §4.1, §4.2 or §2. A step that changes a file §4 does not list is a spec
      that cannot be followed to a green build.

They gate specs that have not been dispatched yet (status "planned" in
docs/oss/spec-status.json): a defect there is still cheap. For every other
spec they only report, because those specs have executed and their defects are
recorded in docs/oss/spec-defects.md; failing on history would be noise.

Usage: scripts/spec_lint.py [--all]
  --all  also fail on dispatched specs (for auditing, not for CI)
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# Overridable so the checks themselves can be tested against fixture specs.
SPECS = os.environ.get("GRAVIX_SPECS_DIR") or os.path.join(ROOT, "docs", "oss", "specs")
STATUS = os.environ.get("GRAVIX_SPEC_STATUS") or os.path.join(ROOT, "docs", "oss", "spec-status.json")

# A backticked token is a path when it has a directory separator or a known
# extension, and none of the marks of a pattern, placeholder, URL or command.
EXT = (".go", ".js", ".ts", ".tsx", ".py", ".sh", ".md", ".json", ".yaml",
       ".yml", ".sql", ".proto", ".toml", ".html", ".css", ".mod", ".txt")
TOKEN = re.compile(r"`([^`\s]+)`")


def top_level_dirs():
    """Directories tracked at the repository root. A repo path starts with one;
    a Go import path (github.com/..., os/exec) or a runtime directory that is
    never committed (data/) does not, and is not this check's business."""
    import subprocess
    try:
        out = subprocess.run(["git", "-C", ROOT, "ls-files"], capture_output=True, text=True, check=True).stdout
        return {line.split("/", 1)[0] for line in out.splitlines() if "/" in line}
    except Exception:
        return {d for d in os.listdir(ROOT) if os.path.isdir(os.path.join(ROOT, d)) and not d.startswith(".")}


TOP = top_level_dirs()


def is_path(tok):
    if any(c in tok for c in "*<>{}$|=()[],;:'\"") or tok.startswith(("-", "/", "http", "~", ".")):
        return False
    # Only paths with a directory, under a tracked top-level directory, and at
    # least two segments deep: a bare "main.go" is ambiguous and a bare "ee/"
    # names a tree, not a file to change.
    if "@" in tok or "..." in tok:
        return False  # an action reference (actions/checkout@v4) or an elision
    parts = tok.rstrip("/").split("/")
    # pkg/recompute.Run is a Go symbol, not a file.
    if re.search(r"\.[A-Z]", parts[-1]):
        return False
    return len(parts) >= 2 and parts[0] in TOP


def sections(text):
    """Map section ids ("2", "4.1", "4.2", "6", "7") to their text."""
    out, current = {}, None
    for line in text.splitlines():
        m = re.match(r"^(#{2,3})\s+(\d+(?:\.\d+)?)\b", line)
        if m:
            current = m.group(2)
            out.setdefault(current, [])
            continue
        if current is not None:
            out[current].append(line)
            # §6.1 and §7.x belong to §6 and §7 for this purpose.
            top = current.split(".")[0]
            if top in ("6", "7") and current != top:
                out.setdefault(top, []).append(line)
    return {k: "\n".join(v) for k, v in out.items()}


def paths_in(text):
    return {t.rstrip("/") for t in TOKEN.findall(text or "") if is_path(t)}


def exists(path):
    return os.path.exists(os.path.join(ROOT, path))


def lint(name, text):
    s = sections(text)
    created = paths_in(s.get("4.1"))
    modified = paths_in(s.get("4.2"))
    context = paths_in(s.get("2"))
    problems = []
    for p in sorted((context | modified) - created):
        if not exists(p):
            where = "§4.2" if p in modified else "§2"
            problems.append(f"M1 {where} names {p}, which does not exist")
    allowed = created | modified | context
    for p in sorted(paths_in(s.get("6")) | paths_in(s.get("7"))):
        # Covered when listed, when inside a listed directory, or when it is a
        # directory that holds a listed file.
        if p not in allowed and not any(p.startswith(a + "/") or a.startswith(p + "/") for a in allowed):
            problems.append(f"M2 §6/§7 names {p}, which §4.1, §4.2 and §2 do not")
    return problems


def main(argv):
    strict_all = "--all" in argv
    status = json.load(open(STATUS))
    failed, reported = 0, 0
    for fname in sorted(os.listdir(SPECS)):
        m = re.match(r"(GRVX-\d+)", fname)
        if not m or not fname.endswith(".md"):
            continue
        spec = m.group(1)
        problems = lint(spec, open(os.path.join(SPECS, fname)).read())
        if not problems:
            continue
        gating = strict_all or status.get(spec) == "planned"
        for p in problems:
            print(f"{'FAIL' if gating else 'note'} {spec}: {p}")
        if gating:
            failed += 1
        else:
            reported += 1
    print(f"spec-lint: {failed} gating spec(s) failed, {reported} dispatched spec(s) with notes")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
