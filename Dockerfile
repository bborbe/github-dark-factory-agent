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

# dark-factory CLI. PIN — and BUMP to the version that ships the in-process
# `executor: local` mode (the execution backend this agent depends on so it
# does NOT spawn nested containers) once that feature lands. v0.191.4 predates
# it; the agent build is gated on that dark-factory feature.
ARG DARK_FACTORY_VERSION=v0.191.4
USER node
RUN go install github.com/bborbe/dark-factory@${DARK_FACTORY_VERSION}

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
