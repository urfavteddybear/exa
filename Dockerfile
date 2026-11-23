# Build stages run on the build host's platform and cross-compile, so multi-arch
# images don't run npm or the Go compiler under QEMU emulation.

# Stage 1: Build the Preact SPA (platform-independent output)
FROM --platform=$BUILDPLATFORM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Cross-compile the static Go binary with embedded web/dist
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=web-builder /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/exa ./cmd/exa

# Stage 3: Minimal runtime image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata iputils-ping su-exec \
    && addgroup -S exa \
    && adduser -S -G exa -h /data exa \
    && mkdir -p /data \
    && chown -R exa:exa /data

COPY --from=go-builder /out/exa /usr/local/bin/exa
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

VOLUME ["/data"]
ENV PORT=8080 HOST=0.0.0.0 DATA_DIR=/data
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/api/v1/health" >/dev/null || exit 1

# No USER here: the entrypoint starts as root only to fix /data ownership, then runs exa as the exa user.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
