# Script for resolving the next development release version based on the current git tags.
# - If latest release is an official one, bump its patch version by one and suffix with '-dev.1', eg, '0.1.2' -> '0.1.3-dev.1'
# - If latest release is a development one, increment the '-dev.N' suffix by one, eg, '0.1.2-dev.4' -> '0.1.2-dev.5'

import os
import re
import subprocess
import sys

# Grab the GITHUB_ENV env variable (if running in GitHub Actions) for writing the output variables.
GITHUB_ENV = os.environ.get('GITHUB_ENV')

# Get all the release tags in the repository.
# Return both official (eg, 0.1.2) and development (eg, 0.1.2-dev.1) tags.
# The tags are sorted in almost semver order, except that the official and
# development tags are not sorted correctly. We handle that later.
def get_git_version_tags():
    try:
        result = subprocess.run(
            ['git', 'tag', '--sort=v:refname'],
            capture_output=True, check=True, text=True
        )
        all_tags = result.stdout.strip().splitlines()
        any_version_re = re.compile(r'^[0-9]+\.[0-9]+\.[0-9]+.*$')
        version_tags = [tag for tag in all_tags if any_version_re.match(tag)]
        return version_tags
    except subprocess.CalledProcessError as e:
        print("Error running git tag:", e, file=sys.stderr)
        sys.exit(1)

# Get all version tags from the repository.
version_tags = get_git_version_tags()
if not version_tags:
    print("Error: No version tags found in repository!")
    sys.exit(1)

# Find latest official release tag (X.Y.Z)
official_re = re.compile(r'^[0-9]+\.[0-9]+\.[0-9]+$')
official_tags = [tag for tag in version_tags if official_re.match(tag)]
if not official_tags:
    print("Error: Latest official release tag (X.Y.Z) not found!")
    sys.exit(1)
latest_official_tag = official_tags[-1]

# Find latest any tag (X.Y.Z or X.Y.Z-...)
# Note: Due to the non-semver sorting by git, we must handle the case here
# where, eg, '1.2.3-dev.1' is considered newer than '1.2.3'.
latest_tag = version_tags[-1]
if latest_tag.startswith(latest_official_tag):
    latest_tag = latest_official_tag
print(f"Latest release tag: {latest_tag}")

# Check for '-dev.N' suffix
dev_re = re.compile(r'^([0-9]+\.[0-9]+\.[0-9]+)-dev\.([0-9]+)$')
dev_match = dev_re.match(latest_tag)
if dev_match:
    base_version = dev_match.group(1)
    dev_num = int(dev_match.group(2))
    next_dev_num = dev_num + 1
    next_dev_tag = f"{base_version}-dev.{next_dev_num}"
    print(f"Computed next dev tag (incrementing dev number): {next_dev_tag}")
else:
    major, minor, patch = latest_tag.split('.')[0:3]
    next_patch = int(patch) + 1
    next_dev_tag = f"{major}.{minor}.{next_patch}-dev.1"
    print(f"Computed next dev tag: {next_dev_tag}")

# Write NEXT_DEV_TAG and LATEST_RELEASE_TAG environment variables to GITHUB_ENV (if running in GitHub Actions).
print(f'NEXT_DEV_TAG={next_dev_tag}')
print(f"LATEST_RELEASE_TAG={latest_official_tag}")
if GITHUB_ENV:
    with open(GITHUB_ENV, 'a') as env_file:
        env_file.write(f"LATEST_RELEASE_TAG={latest_official_tag}\n")
        env_file.write(f"NEXT_DEV_TAG={next_dev_tag}\n")
