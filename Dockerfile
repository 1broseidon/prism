# syntax=docker/dockerfile:1.7
#
# Multi-arch build for ghcr.io/<owner>/prism. The heavy stages are pinned to
# $BUILDPLATFORM so the SPA build (npm) and Go compile run natively on the
# runner; only the tiny final runtime stage runs under QEMU when cross-
# building. Without this, a `linux/arm64` build under QEMU runs `npm ci`
# emulated and exceeds GitHub Actions' 6-hour job limit.

# Stage 1: build the SPA — output goes into internal/admin/web/dist/
# and is embedded into the Go binary in stage 2 via go:embed.
FROM --platform=$BUILDPLATFORM node:22-alpine AS spa
WORKDIR /spa
COPY internal/admin/web/package.json internal/admin/web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY internal/admin/web/ ./
RUN npm run build

# Stage 2: cross-compile the Go binaries with the mcp_go_client_oauth tag.
# Go's native cross-compile produces a static binary for any GOOS/GOARCH
# without needing the target arch's toolchain — much faster than running
# the compiler under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the source dist/ with the freshly built SPA so the embed is current.
COPY --from=spa /spa/dist ./internal/admin/web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags mcp_go_client_oauth -trimpath -ldflags "-s -w" \
    -o /prism ./cmd/prism
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags mcp_go_client_oauth -trimpath -ldflags "-s -w" \
    -o /prism-bridge ./cmd/prism-bridge

# Stage 3: minimal runtime. Runs under the target platform; only `apk add`
# of prebuilt packages happens under QEMU when cross-building.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata bash python3 nodejs npm uv
COPY --from=build /prism /usr/local/bin/prism
COPY --from=build /prism-bridge /usr/local/bin/prism-bridge
COPY deploy/config.container.json /etc/prism/config.json
WORKDIR /etc/prism
ENV PRISM_IN_CONTAINER=1 \
    PRISM_DATA_DIR=/data \
    PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key \
    PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem \
    PRISM_SANDBOX_IMAGE=ghcr.io/1broseidon/prism:latest
VOLUME ["/data"]
EXPOSE 8080 9086
ENTRYPOINT ["prism"]
CMD ["-config", "/etc/prism/config.json"]
