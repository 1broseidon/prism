# syntax=docker/dockerfile:1.7
#
# Multi-arch build for ghcr.io/<owner>/prism-bridge. Build stage pinned to
# $BUILDPLATFORM so Go's native cross-compile runs natively; only the small
# runtime stage runs under QEMU when cross-building.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags mcp_go_client_oauth -trimpath -ldflags "-s -w" \
    -o /prism-bridge ./cmd/prism-bridge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates bash python3 nodejs npm uv su-exec
COPY --from=build /prism-bridge /usr/local/bin/prism-bridge
ENTRYPOINT ["prism-bridge"]
