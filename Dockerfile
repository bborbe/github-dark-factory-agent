ARG DOCKER_REGISTRY=docker.quant.benjamin-borbe.de:443

# --- Build the agent binary -------------------------------------------------
FROM ${DOCKER_REGISTRY}/golang:1.26.4 AS build
ARG BUILD_GIT_VERSION=dev
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY . /workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor -ldflags "-s" -a -installsuffix cgo -o /main

# --- Runtime = the claude-yolo execution environment ------------------------
# The agent Job pod IS the execution environment (no nested containers / no DinD).
# claude-yolo already bakes in the pieces dark-factory's lifecycle needs:
#   - PINNED claude-code (deliberately not `latest` — see claude-yolo Dockerfile;
#     floating latest caused a real headless-plugin regression)
#   - Go toolchain + golangci-lint / ginkgo / counterfeiter / goimports
#     (so `make precommit` on target Go repos works)
#   - git, gh, jq, ripgrep, trivy, yq, uv + updater
# We add only: the dark-factory CLI (the lifecycle driver) + the agent binary.
ARG CLAUDE_YOLO_IMAGE=docker.io/bborbe/claude-yolo:v0.13.2
FROM ${CLAUDE_YOLO_IMAGE}
ARG BUILD_GIT_VERSION=dev
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.version="${BUILD_GIT_VERSION}"

# dark-factory CLI. PIN. v0.192.0 ships the in-process `backend: local` execution
# mode (spec 104) this agent depends on: it runs claude as a local subprocess in
# cwd instead of `docker run`, so the agent does NOT spawn nested containers
# (no DinD) — the Job pod is already a claude-yolo container. Select it at runtime
# with `dark-factory run --set backend=local`.
ARG DARK_FACTORY_VERSION=v0.192.0
USER node
RUN go install github.com/bborbe/dark-factory@${DARK_FACTORY_VERSION}

# dark-factory Claude PLUGIN — the /dark-factory:* slash commands
# (generate-prompts-for-spec, audit-prompt) that the backend:local lifecycle
# invokes INSIDE claude. The CLI binary above is NOT sufficient on its own:
# spec generation and prompt audit are run as these slash commands, and an
# un-provisioned config dir reports "Unknown command:
# /dark-factory:generate-prompts-for-spec" → zero prompts generated → the spec
# resets to approved and the lifecycle idles at "nothing to do" (E2E root cause,
# 2026-07-13). Install into the image's CLAUDE_CONFIG_DIR so the commands
# resolve at runtime without depending on a mounted PVC. Mirrors
# github-pr-review-agent's build-time `coding` plugin install; auth stays
# runtime (env token).
#
# PINNED to the SAME ${DARK_FACTORY_VERSION} tag as the CLI above: the plugin
# and CLI ship from one repo (bborbe/dark-factory), so we clone that exact tag
# and add it as a LOCAL marketplace instead of `marketplace add bborbe/dark-factory`
# (which resolves to marketplace HEAD and drifts from the pinned CLI minor). The
# clone is kept at a stable path — the marketplace source must persist for the
# installed plugin to keep resolving.
RUN set -eux \
 && git clone --depth 1 --branch "${DARK_FACTORY_VERSION}" https://github.com/bborbe/dark-factory /home/node/dark-factory-marketplace \
 && claude plugin marketplace add /home/node/dark-factory-marketplace \
 && claude plugin install dark-factory@dark-factory \
 && claude plugin list | grep -q dark-factory

COPY --from=build /main /main
COPY agent/ /agent/
ENV BUILD_GIT_VERSION=${BUILD_GIT_VERSION}
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}

# claude-yolo's entrypoint.sh (tinyproxy firewall + node-UID remap, needs
# NET_ADMIN/NET_RAW) is skipped — impractical under k8s. Run the agent directly;
# it orchestrates dark-factory in-process (executor=local).
USER node
ENTRYPOINT ["/main", "-v=2"]
