#!/usr/bin/env python3
"""Fail-closed Go line and branch coverage gates."""
import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

MODULE = "github.com/aalsanie/distroplane/"
PROFILE = re.compile(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")
BRANCHES = re.compile(r"(?m)^Branch coverage: (\d+)/(\d+)\s*$")


def require_above(covered, total, minimum, label):
    if total <= 0:
        raise ValueError(f"{label}: no coverable items")
    percent = 100 * covered / total
    print(f"{label}: {covered}/{total} ({percent:.2f}%, required > {minimum:.2f}%)")
    if percent <= minimum:
        raise ValueError(f"{label}: {percent:.2f}% does not exceed {minimum:.2f}%")


def local_file(name, root):
    if not name.startswith(MODULE):
        raise ValueError(f"unexpected module source in profile: {name}")
    relative = Path(name.removeprefix(MODULE))
    if relative.is_absolute() or ".." in relative.parts:
        raise ValueError(f"unsafe source path: {name}")
    path = root / relative
    if not path.is_file() or path.suffix != ".go":
        raise ValueError(f"source missing or not Go: {relative}")
    return path


def line_counts(profile, root):
    lines = profile.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0] not in ("mode: atomic", "mode: count", "mode: set"):
        raise ValueError("invalid Go coverage profile header")
    covered, possible = set(), set()
    sources = {}
    for item in lines[1:]:
        found = PROFILE.fullmatch(item)
        if found is None:
            raise ValueError(f"malformed Go coverage record: {item}")
        filename, start, _sc, end, _ec, statements, hits = found.groups()
        begin, finish, nstmt, count = int(start), int(end), int(statements), int(hits)
        if nstmt <= 0 or finish < begin:
            continue
        source = local_file(filename, root)
        if filename not in sources:
            sources[filename] = source.read_text(encoding="utf-8").splitlines()
        body = sources[filename]
        if finish > len(body):
            raise ValueError(f"coverage range outside source: {filename}")
        for number in range(begin, finish + 1):
            content = body[number - 1].strip()
            if not content or content in ("{", "}", "};") or content.startswith(("//", "/*", "*")):
                continue
            entry = (filename, number)
            possible.add(entry)
            if count:
                covered.add(entry)
    if not possible:
        raise ValueError("Go profile had no coverable source lines")
    return len(covered), len(possible)


def go_packages(root):
    process = subprocess.run(
        ["go", "list", "-json", "./..."], cwd=root, capture_output=True, text=True,
        check=True, timeout=180)
    decoder = json.JSONDecoder()
    result = []
    data = process.stdout
    pos = 0
    while pos < len(data):
        while pos < len(data) and data[pos].isspace():
            pos += 1
        if pos == len(data):
            break
        package, pos = decoder.raw_decode(data, pos)
        directory = Path(package["Dir"]).resolve().relative_to(root.resolve())
        if directory.as_posix().startswith("maven-plugin/it/fixture-cli"):
            continue  # Go executable fixture, not a production package.
        if not package.get("TestGoFiles") and not package.get("XTestGoFiles"):
            raise ValueError(f"production package has no tests: {package['ImportPath']}")
        result.append((directory, package))
    if not result:
        raise ValueError("go list found no tested production packages")
    return result


def sanitized_checkout(root, destination, packages):
    tracked = subprocess.run(["git", "ls-files", "-z"], cwd=root,
                             capture_output=True, check=True, timeout=30).stdout
    paths = [Path(value.decode("utf-8")) for value in tracked.split(b"\0") if value]
    for relative in paths:
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("unsafe Git-tracked source path")
        src, dst = root / relative, destination / relative
        if src.is_symlink():
            raise ValueError(f"cannot instrument symlink: {relative}")
        if src.is_file():
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
    for directory, package in packages:
        allowed = set().union(*[set(package.get(key, [])) for key in
                                ("GoFiles", "CgoFiles", "TestGoFiles", "XTestGoFiles")])
        folder = destination / directory
        for file in folder.glob("*.go"):
            if file.name not in allowed:
                file.unlink()
    return destination


def branch_counts(root, tool):
    packages = go_packages(root)
    if not shutil.which(tool) and not Path(tool).is_file():
        raise ValueError(f"gobco executable not found: {tool}")
    total_hit = total_arms = 0
    failures = []
    with tempfile.TemporaryDirectory(prefix="distroplane-branch-") as location:
        checkout = sanitized_checkout(root, Path(location), packages)
        for directory, package in packages:
            # Measure branches reached by consumers in other packages as well as
            # the package's own unit tests; integration suites exercise the same
            # instrumented production code inside this isolated module copy.
            integrated = (directory.parts[0] == "internal" and len(directory.parts) == 2
                          and directory.parts[1] in {
                              "config", "evidence", "executor", "journal", "planner",
                              "providerhost", "runstate",
                          })
            invocation = [tool, "-branch", "."]
            if integrated:
                pattern = "/".join([".."] * len(directory.parts) + ["..."])
                invocation = [tool, "-branch", "-test", pattern, "."]
            result = subprocess.run(
                invocation, cwd=checkout / directory, text=True,
                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False, timeout=240)
            if result.returncode and integrated:
                # Preserve the valid package-level result if an unrelated
                # cross-package test cannot run in the instrumented checkout.
                print(f"cross-package branch run failed for {package['ImportPath']}; retrying package tests",
                      file=sys.stderr, flush=True)
                result = subprocess.run(
                    [tool, "-branch", "."], cwd=checkout / directory, text=True,
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False, timeout=240)
            if result.returncode:
                failures.append(package["ImportPath"])
                print(f"branch instrumentation failed for {package['ImportPath']}:", file=sys.stderr)
                print(result.stdout[:9000], file=sys.stderr)
                continue
            found = BRANCHES.search(result.stdout)
            if not found:
                if "nothing to instrument" in result.stdout:
                    print(f"{package['ImportPath']}: no conditional branches")
                    continue
                raise ValueError(f"missing branch report for {package['ImportPath']}:\n"
                                 + result.stdout[-1000:])
            hit, arms = int(found[1]), int(found[2])
            if arms < 0 or hit > arms or arms % 2:
                raise ValueError("invalid branch counter output")
            total_hit += hit
            total_arms += arms
            if arms:
                print(f"{package['ImportPath']}: {hit}/{arms} ({100 * hit / arms:.2f}%)", flush=True)
                if hit * 10 <= arms * 9:
                    missed = [line for line in result.stdout.splitlines()
                              if ": condition " in line and ("never" in line or "not evaluated" in line)]
                    for line in missed[:20]:
                        print("  " + line, flush=True)
            else:
                print(f"{package['ImportPath']}: no branch decisions", flush=True)
    if failures:
        print(f"partial Go branch totals: {total_hit}/{total_arms}", file=sys.stderr)
        raise ValueError("branch instrumentation failed: " + ", ".join(failures))
    return total_hit, total_arms


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("lines", "branches"))
    parser.add_argument("--profile", type=Path, default=Path("coverage.out"))
    parser.add_argument("--gobco", default="gobco")
    parser.add_argument("--minimum", type=float, default=90.0)
    args = parser.parse_args()
    if not 0 <= args.minimum < 100:
        parser.error("coverage minimum must be between 0 and 100")
    root = Path(__file__).resolve().parents[1]
    try:
        hit, all_items = (line_counts(args.profile, root) if args.mode == "lines"
                          else branch_counts(root, args.gobco))
        require_above(hit, all_items, args.minimum, f"Go {args.mode}")
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        parser.exit(1, f"coverage gate failed: {exc}\n")


if __name__ == "__main__":
    main()
