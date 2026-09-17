# Recorder image for cli-build.gif. See record.sh.
# Stay on VHS v0.10.0 (don't accept Renovate upgrades): v0.12.0 writes no output file.
FROM ghcr.io/charmbracelet/vhs:v0.10.0
COPY --from=docker:cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker/buildx-bin /buildx /usr/libexec/docker/cli-plugins/docker-buildx
COPY metaplay /usr/local/bin/metaplay
RUN chmod +x /usr/local/bin/metaplay
