FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags mcp_go_client_oauth -o /prism-bridge ./cmd/prism-bridge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates bash python3 nodejs npm uv su-exec
COPY --from=build /prism-bridge /usr/local/bin/prism-bridge
ENTRYPOINT ["prism-bridge"]
