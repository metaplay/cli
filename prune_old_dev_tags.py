#!/usr/bin/env python3
import argparse
import re
import subprocess
import sys
from typing import List, Tuple, Set

# Regex for tags like:
#  - 1.6.2
#  - 1.6.2-dev.1
VERSION_RE = re.compile(
    r"""
    ^
    (?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+)
    (?:
        -dev\.(?P<dev>\d+)
    )?
    $
    """,
    re.VERBOSE,
)

def run_git(args: List[str]) -> str:
    result = subprocess.run(
        ["git"] + args,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        print(f"git {' '.join(args)} failed:\n{result.stderr}", file=sys.stderr)
        sys.exit(result.returncode)
    return result.stdout.strip()

def parse_tag(tag: str):
    m = VERSION_RE.match(tag)
    if not m:
        return None
    major = int(m.group("major"))
    minor = int(m.group("minor"))
    patch = int(m.group("patch"))
    dev = m.group("dev")
    dev_num = int(dev) if dev is not None else None
    return (major, minor, patch, dev_num)

def main(dry_run: bool = True):
    # 1) Get all tags
    raw = run_git(["tag", "--list"])
    tags = [t for t in raw.splitlines() if t.strip()]
    if not tags:
        print("No tags found.")
        return

    # 2) Filter to version-like tags and categorise
    official_versions: Set[Tuple[int, int, int]] = set()
    dev_tags_by_version = {}  # (major, minor, patch) -> [tag, ...]

    for tag in tags:
        parsed = parse_tag(tag)
        if not parsed:
            # Ignore any non-matching tags
            continue
        major, minor, patch, dev_num = parsed
        ver_key = (major, minor, patch)
        if dev_num is None:
            # Official release tag, e.g. 1.6.2
            official_versions.add(ver_key)
        else:
            # Dev tag, e.g. 1.6.2-dev.10
            dev_tags_by_version.setdefault(ver_key, []).append(tag)

    if not official_versions:
        print("No official release tags found (x.y.z). Nothing to do.")
        return

    # 3) Sort official releases and pick last two
    sorted_official = sorted(official_versions)  # lexicographic (major, minor, patch)
    latest_two = sorted_official[-2:] if len(sorted_official) >= 2 else sorted_official

    print("Official releases found (sorted):")
    for v in sorted_official:
        print(f"  {v[0]}.{v[1]}.{v[2]}")

    print("\nKeeping dev tags ONLY for the following official releases:")
    for v in latest_two:
        print(f"  {v[0]}.{v[1]}.{v[2]}")

    # 4) Determine dev tags to delete
    tags_to_delete: List[str] = []
    for ver_key, dev_tag_list in dev_tags_by_version.items():
        if ver_key in latest_two:
            continue  # Keep dev tags for latest two official releases
        tags_to_delete.extend(dev_tag_list)

    if not tags_to_delete:
        print("\nNo dev tags need to be deleted.")
        return

    print("\nDev tags to delete:")
    for t in sorted(tags_to_delete):
        print(f"  {t}")

    if dry_run:
        print("\nDry-run mode: no tags have been deleted.")
        return

    # 5) Delete tags locally and on remote
    for t in tags_to_delete:
        print(f"Deleting tag: {t}")
        run_git(["tag", "-d", t])
        run_git(["push", "--delete", "origin", t])

    print("\nDone. Deleted dev tags outside the two latest official releases (local and origin).")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Prune old -dev git tags, keeping dev tags only for the two latest official releases.",
    )
    parser.add_argument(
        "--dry-run",
        dest="dry_run",
        action="store_true",
        help="Show what would be done without deleting any tags",
    )
    parser.set_defaults(dry_run=False)

    args = parser.parse_args()
    main(dry_run=args.dry_run)
