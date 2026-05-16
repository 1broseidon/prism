# Stage 1: build the SPA — output goes into internal/admin/web/dist/
# and is embedded into the Go binary in stage 2 via go:embed.
FROM node:22-alpine AS spa
WORKDIR /spa
COPY internal/admin/web/package.json internal/admin/web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY internal/admin/web/ ./
RUN npm run build

# Stage 2: build the Go binary with the mcp_go_client_oauth tag.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the source dist/ with the freshly built SPA so the embed is current.
COPY --from=spa /spa/dist ./internal/admin/web/dist
RUN CGO_ENABLED=0 go build -tags mcp_go_client_oauth -o /prism ./cmd/prism

# Stage 3: minimal runtime.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /prism /usr/local/bin/prism
WORKDIR /etc/prism
EXPOSE 8080 9086
ENTRYPOINT ["prism"]
CMD ["-config", "/etc/prism/config.json"]
