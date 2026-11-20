# Stage 1: Build the Preact SPA
FROM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build the static Go binary with embedded web/dist
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=web-builder /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/exa ./cmd/exa

# Stage 3: Minimal runtime image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata iputils-ping \
    && addgroup -S exa \
    && adduser -S -G exa -h /data exa \
    && mkdir -p /data \
    && chown -R exa:exa /data

COPY --from=go-builder /out/exa /usr/local/bin/exa

VOLUME ["/data"]
ENV PORT=8080 HOST=0.0.0.0 DATA_DIR=/data
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/api/v1/health" >/dev/null || exit 1

USER exa
ENTRYPOINT ["/usr/local/bin/exa"]
