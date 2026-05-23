#!/usr/bin/env python3
import argparse
import re
import statistics
import sys
from dataclasses import dataclass
from pathlib import Path


@dataclass
class BenchSamples:
    ns_op: list[float]
    bytes_op: list[float]
    allocs_op: list[float]


def normalize_name(name: str) -> str:
    return re.sub(r"-\d+$", "", name)


def parse_benchmarks(path: Path, benchmark: str) -> BenchSamples:
    samples = BenchSamples(ns_op=[], bytes_op=[], allocs_op=[])
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.startswith("Benchmark"):
            continue
        fields = line.split()
        if len(fields) < 4 or normalize_name(fields[0]) != benchmark:
            continue
        for idx, field in enumerate(fields):
            if field == "ns/op" and idx > 0:
                samples.ns_op.append(float(fields[idx - 1]))
            elif field == "B/op" and idx > 0:
                samples.bytes_op.append(float(fields[idx - 1]))
            elif field == "allocs/op" and idx > 0:
                samples.allocs_op.append(float(fields[idx - 1]))
    if not samples.ns_op or not samples.allocs_op:
        raise ValueError(f"{path} has no complete samples for {benchmark}")
    return samples


def median(values: list[float]) -> float:
    if not values:
        return 0
    return statistics.median(values)


def ratio(candidate: float, baseline: float) -> float:
    if baseline == 0:
        return float("inf") if candidate > 0 else 1
    return candidate / baseline


def fmt(value: float) -> str:
    if value == float("inf"):
        return "inf"
    return f"{value:,.0f}"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Fail when benchmark medians regress past a threshold."
    )
    parser.add_argument("--baseline", required=True, type=Path)
    parser.add_argument("--candidate", required=True, type=Path)
    parser.add_argument("--benchmark", required=True)
    parser.add_argument("--threshold", required=True, type=float)
    args = parser.parse_args()

    baseline = parse_benchmarks(args.baseline, args.benchmark)
    candidate = parse_benchmarks(args.candidate, args.benchmark)

    base_ns = median(baseline.ns_op)
    cand_ns = median(candidate.ns_op)
    base_bytes = median(baseline.bytes_op)
    cand_bytes = median(candidate.bytes_op)
    base_allocs = median(baseline.allocs_op)
    cand_allocs = median(candidate.allocs_op)
    ns_ratio = ratio(cand_ns, base_ns)
    allocs_ratio = ratio(cand_allocs, base_allocs)

    print(f"## Renderer benchmark regression check")
    print()
    print(f"Benchmark: `{args.benchmark}`")
    print(
        f"Threshold: fail when `ns/op` or `allocs/op` exceeds baseline by more than {(args.threshold - 1) * 100:.0f}%"
    )
    print()
    print("| Metric | Main baseline median | PR median | Ratio |")
    print("| --- | ---: | ---: | ---: |")
    print(f"| ns/op | {fmt(base_ns)} | {fmt(cand_ns)} | {ns_ratio:.2f}x |")
    print(
        f"| B/op | {fmt(base_bytes)} | {fmt(cand_bytes)} | {ratio(cand_bytes, base_bytes):.2f}x |"
    )
    print(
        f"| allocs/op | {fmt(base_allocs)} | {fmt(cand_allocs)} | {allocs_ratio:.2f}x |"
    )

    failed = ns_ratio > args.threshold or allocs_ratio > args.threshold
    if failed:
        print()
        if ns_ratio > args.threshold:
            print(f"::error::{args.benchmark} ns/op regressed by {ns_ratio:.2f}x")
        if allocs_ratio > args.threshold:
            print(
                f"::error::{args.benchmark} allocs/op regressed by {allocs_ratio:.2f}x"
            )
        return 1
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"::error::{exc}", file=sys.stderr)
        raise SystemExit(2)
