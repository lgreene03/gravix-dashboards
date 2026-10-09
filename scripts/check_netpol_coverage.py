#!/usr/bin/env python3
# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
"""Fail when a rendered chart with network policy leaves a workload unselected.

The chart starts from default-deny ingress and egress for every pod, so a
workload no allow-policy selects can neither be reached nor reach anything.
The gateway had no policy, and four CronJobs labelled only the CronJob, not
its pods (F-083). Each was healthy-looking YAML that would have done nothing.

Reads `helm template` output on stdin.
"""
import sys

import yaml


def pod_labels(doc):
    kind = doc.get("kind")
    if kind in ("Deployment", "StatefulSet", "DaemonSet"):
        return doc["spec"]["template"].get("metadata", {}).get("labels") or {}
    if kind == "CronJob":
        return doc["spec"]["jobTemplate"]["spec"]["template"].get("metadata", {}).get("labels") or {}
    return None


def selects(policy, labels):
    sel = policy["spec"].get("podSelector") or {}
    match = sel.get("matchLabels") or {}
    exprs = sel.get("matchExpressions") or []
    if not match and not exprs:
        return False  # the namespace-wide default deny and DNS policies select everything
    if any(labels.get(k) != v for k, v in match.items()):
        return False
    for e in exprs:
        if e.get("operator") == "In" and labels.get(e["key"]) not in e.get("values", []):
            return False
    return True


def main():
    docs = [d for d in yaml.safe_load_all(sys.stdin) if d]
    policies = [d for d in docs if d.get("kind") == "NetworkPolicy"]
    if not policies:
        print("network policy is off in this render; nothing to check")
        return 0
    missing = []
    for d in docs:
        labels = pod_labels(d)
        if labels is None:
            continue
        if not any(selects(p, labels) for p in policies):
            missing.append(f"{d['kind']} {d['metadata']['name']} (pod labels {labels or 'none'})")
    for m in missing:
        print(f"::error::{m} is selected by no allow-policy; the default deny cuts it off")
    if missing:
        return 1
    print(f"every workload is selected by an allow-policy ({len(policies)} policies)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
