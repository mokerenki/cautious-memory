#!/usr/bin/env python3
"""Score ARTEX findings export against eval range ground truth.

Stdlib only. Usage:
  python3 evals/score.py findings.json groundtruth.json [--csv results.csv]
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path


def load_json(path: Path):
    with path.open(encoding="utf-8") as f:
        return json.load(f)


def normalize_findings(export) -> list[dict]:
    """Accept ARTEX UI/API export shapes or a bare list."""
    if isinstance(export, list):
        return export
    if not isinstance(export, dict):
        return []
    for key in ("findings", "items", "data", "results"):
        v = export.get(key)
        if isinstance(v, list):
            return v
    return []


def text_blob(f: dict) -> str:
    parts = [
        str(f.get("id", "")),
        str(f.get("vulnclass", "") or f.get("class", "") or ""),
        str(f.get("name", "") or f.get("title", "") or ""),
        str(f.get("summary", "") or ""),
        str(f.get("evidence", "") or ""),
        str(f.get("description", "") or ""),
        str(f.get("endpoint", "") or ""),
        str(f.get("severity", "") or ""),
        str(f.get("status", "") or ""),
    ]
    return " ".join(parts).lower()


def match_bug(finding: dict, bug: dict) -> bool:
    blob = text_blob(finding)
    tokens = []
    for field in ("id", "class", "endpoint", "description"):
        val = str(bug.get(field, "") or "").lower()
        tokens.append(val)
        for piece in re.split(r"[^a-z0-9/<>_\-]+", val):
            if len(piece) >= 4:
                tokens.append(piece)
    # Strong signals
    bug_id = str(bug.get("id", "")).lower()
    bug_class = str(bug.get("class", "")).lower()
    if bug_id and bug_id in blob:
        return True
    if bug_class and bug_class.replace("_", " ") in blob.replace("_", " "):
        return True
    if bug_class and bug_class in blob:
        return True
    endpoint = str(bug.get("endpoint", "")).lower()
    # e.g. /api/documents
    path_m = re.search(r"(/api/[a-z0-9_/<>-]+)", endpoint)
    if path_m and path_m.group(1).split("<")[0].rstrip("/") in blob:
        # require a class-ish word too to avoid over-match
        class_hints = {
            "bola_idor": ["idor", "bola", "ownership", "document"],
            "price_tampering": ["price", "total", "checkout", "tamper"],
            "race_condition": ["race", "concurrent", "coupon", "atomic"],
            "workflow_skip": ["refund", "approve", "workflow", "skip"],
            "quota_bypass": ["quota", "tier", "project", "limit"],
        }
        hints = class_hints.get(bug_class, [])
        if any(h in blob for h in hints):
            return True
    # description keyword overlap
    desc_words = [w for w in re.findall(r"[a-z]{5,}", str(bug.get("description", "")).lower())]
    hits = sum(1 for w in desc_words if w in blob)
    if hits >= 3:
        return True
    return False


def score(findings: list[dict], ground: list[dict]) -> dict:
    matched_bugs: dict[str, list[int]] = {b["id"]: [] for b in ground}
    fp_indices = []
    for i, f in enumerate(findings):
        hit = False
        for b in ground:
            if match_bug(f, b):
                matched_bugs[b["id"]].append(i)
                hit = True
        if not hit:
            fp_indices.append(i)

    recall = {}
    for bid, idxs in matched_bugs.items():
        recall[bid] = 1.0 if idxs else 0.0

    # verified-ish count: findings whose status looks verified/confirmed
    verified = 0
    for f in findings:
        st = str(f.get("status", "") or "").lower()
        if st in ("verified", "confirmed", "true_positive"):
            verified += 1

    tokens = None
    for f in findings:
        for k in ("tokens", "token_usage", "usage_tokens"):
            if k in f and isinstance(f[k], (int, float)):
                tokens = (tokens or 0) + int(f[k])
    # top-level export tokens
    return {
        "n_findings": len(findings),
        "n_ground_truth": len(ground),
        "recall_per_bug": recall,
        "bugs_found": sum(1 for v in recall.values() if v > 0),
        "recall_macro": (sum(recall.values()) / len(recall)) if recall else 0.0,
        "true_positive_findings": sum(len(v) for v in matched_bugs.values()),
        "duplicate_tp_extra": sum(max(0, len(v) - 1) for v in matched_bugs.values()),
        "false_positive_count": len(fp_indices),
        "verified_findings": verified,
        "tokens_spent": tokens,
        "matched_bug_ids": {k: v for k, v in matched_bugs.items() if v},
        "fp_indices": fp_indices,
    }


def main() -> int:
    ap = argparse.ArgumentParser(description="Score ARTEX findings vs ground truth")
    ap.add_argument("findings", type=Path)
    ap.add_argument("groundtruth", type=Path)
    ap.add_argument("--csv", type=Path, default=None, help="Append metrics row to CSV")
    ap.add_argument("--label", default="", help="Optional run label for CSV")
    args = ap.parse_args()

    export = load_json(args.findings)
    findings = normalize_findings(export)
    ground = load_json(args.groundtruth)
    if isinstance(ground, dict) and "bugs" in ground:
        ground = ground["bugs"]

    metrics = score(findings, ground)
    # tokens from export root
    if isinstance(export, dict):
        for k in ("tokens_spent", "total_tokens", "tokens"):
            if k in export and metrics["tokens_spent"] is None:
                metrics["tokens_spent"] = export[k]

    print(json.dumps(metrics, indent=2, ensure_ascii=False))

    if args.csv:
        args.csv.parent.mkdir(parents=True, exist_ok=True)
        write_header = not args.csv.exists()
        row = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "label": args.label,
            "n_findings": metrics["n_findings"],
            "bugs_found": metrics["bugs_found"],
            "n_ground_truth": metrics["n_ground_truth"],
            "recall_macro": f"{metrics['recall_macro']:.3f}",
            "true_positive_findings": metrics["true_positive_findings"],
            "duplicate_tp_extra": metrics["duplicate_tp_extra"],
            "false_positive_count": metrics["false_positive_count"],
            "verified_findings": metrics["verified_findings"],
            "tokens_spent": metrics["tokens_spent"] or "",
        }
        # per-bug recall columns
        for bid, val in metrics["recall_per_bug"].items():
            row[f"recall_{bid}"] = val
        fieldnames = list(row.keys())
        with args.csv.open("a", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=fieldnames)
            if write_header:
                w.writeheader()
            w.writerow(row)
        print(f"Appended metrics to {args.csv}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
