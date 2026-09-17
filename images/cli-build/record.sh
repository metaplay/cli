#!/usr/bin/env bash
# Records images/cli-build.gif with the latest CLI release. See DEVELOPMENT.md.
set -euo pipefail

# Docker needs Windows paths on Git Bash.
hostpath() { (cd "$1" && (pwd -W 2>/dev/null || pwd)); }

SDK=$(hostpath "${1:?Usage: record.sh <path-to-sdk>}")
HERE=$(hostpath "$(dirname "$0")")
export MSYS_NO_PATHCONV=1

# Build the recorder image with the latest CLI release.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
gh release download --repo metaplay/cli --pattern MetaplayCLI_Linux_x86_64.tar.gz --dir "$(hostpath "$TMP")"
tar xzf "$TMP/MetaplayCLI_Linux_x86_64.tar.gz" -C "$TMP" metaplay
cp "$HERE/Dockerfile" "$TMP/"
docker build -t metaplay-vhs "$(hostpath "$TMP")"

# Copy the build inputs into a volume. Reading them from a Windows bind mount is too slow.
docker volume rm -f metaplay-sdk-ctx >/dev/null
docker volume create metaplay-sdk-ctx >/dev/null
(cd "$SDK" && tar -cf - --exclude={.git,node_modules,bin,obj,out,dist,Debug,Release,'*.meta'} \
    --exclude=Samples/Idler/{Library,Temp,Logs,Packages,Build} \
    $(find . -maxdepth 1 -type f) MetaplaySDK Samples/Idler) |
  docker run -i --rm -v metaplay-sdk-ctx:/work/sdk --entrypoint tar metaplay-vhs -xf - -C /work/sdk

# A fixed commit ID and build number keep the recorded build fully cached.
run() {
  docker run --rm -e GIT_COMMIT="$(git -C "$SDK" rev-parse --short HEAD)" -e BUILD_NUMBER=142 \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v metaplay-sdk-ctx:/work/sdk -w /work/sdk/Samples/Idler \
    -v "$HERE":/rec -v "$(hostpath "$HERE/..")":/out "$@"
}
run --entrypoint metaplay metaplay-vhs build image lovely-wombats-build:warmup # Slow the first time.
run metaplay-vhs /rec/cli-build.tape

docker volume rm metaplay-sdk-ctx >/dev/null
docker image rm lovely-wombats-build:warmup >/dev/null
