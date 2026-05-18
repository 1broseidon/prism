FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags mcp_go_client_oauth -o /prism ./cmd/prism
RUN CGO_ENABLED=0 go build -tags mcp_go_client_oauth -o /prism-bridge ./cmd/prism-bridge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata bash python3 nodejs npm uv
COPY --from=build /prism /usr/local/bin/prism
COPY --from=build /prism-bridge /usr/local/bin/prism-bridge
COPY deploy/config.container.json /etc/prism/config.json
ENV PRISM_IN_CONTAINER=1 \
    PRISM_DATA_DIR=/data \
    PRISM_KV_KEY_FILE=/data/.prism/kv-encryption.key \
    PRISM_SIGNING_KEY_FILE=/data/.prism/signing-key.pem \
    PRISM_SANDBOX_IMAGE=ghcr.io/1broseidon/prism:latest
VOLUME ["/data"]
WORKDIR /etc/prism
EXPOSE 8080 9086
ENTRYPOINT ["prism"]
CMD ["-config", "/etc/prism/config.json"]
