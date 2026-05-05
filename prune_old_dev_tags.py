#!/usr/bin/env python3
import argparse
import re
import subprocess
import sys
from typing import List

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

    # 2) Filter to dev tags and group them by (major, minor, patch)
    dev_tags_by_version = {}  # (major, minor, patch) -> [tag, ...]

    for tag in tags:
        parsed = parse_tag(tag)
        if not parsed:
            continue
        major, minor, patch, dev_num = parsed
        if dev_num is None:
            continue
        dev_tags_by_version.setdefault((major, minor, patch), []).append(tag)

    if not dev_tags_by_version:
        print("No dev tags found. Nothing to do.")
        return

    # 3) Sort dev version lines and pick the latest two
    sorted_dev_versions = sorted(dev_tags_by_version.keys())  # sorts numerically by (major, minor, patch)
    latest_two = sorted_dev_versions[-2:]

    print("Dev version lines found (sorted):")
    for v in sorted_dev_versions:
        print(f"  {v[0]}.{v[1]}.{v[2]}")

    # 4) Keep dev tags for the two latest dev version lines; prune the rest.
    tags_to_delete: List[str] = []
    tags_to_keep: List[str] = []
    for ver_key, dev_tag_list in dev_tags_by_version.items():
        if ver_key in latest_two:
            tags_to_keep.extend(dev_tag_list)
        else:
            tags_to_delete.extend(dev_tag_list)

    if tags_to_keep:
        print("\nDev tags to keep:")
        for t in sorted(tags_to_keep):
            print(f"  {t}")

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
        local = subprocess.run(["git", "tag", "-d", t], capture_output=True, text=True)
        if local.returncode != 0:
            if "not found" in local.stderr.lower():
                print("  (local tag already gone)")
            else:
                print(f"  local delete failed: {local.stderr.strip()}", file=sys.stderr)
                sys.exit(local.returncode)

        remote = subprocess.run(["git", "push", "--delete", "origin", t], capture_output=True, text=True)
        if remote.returncode != 0:
            if "remote ref does not exist" in remote.stderr.lower():
                print("  (remote tag already gone)")
            else:
                print(f"  remote delete failed: {remote.stderr.strip()}", file=sys.stderr)
                sys.exit(remote.returncode)

    print("\nDone. Deleted old dev tags (local and origin).")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Prune old -dev git tags, keeping dev tags only for the two latest dev version lines.",
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
